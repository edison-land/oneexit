package signaling

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type RemoteRoom struct {
	ID             string    `json:"id"`
	RoomKey        string    `json:"roomKey"`
	State          string    `json:"state"`
	ExitIP         string    `json:"exitIp,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
	ProviderOnline bool      `json:"providerOnline"`
	OperatorJoined bool      `json:"operatorJoined"`
	Token          string    `json:"token,omitempty"`
	Role           string    `json:"role,omitempty"`
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("signaling HTTP %d: %s", e.Status, e.Body)
	}
	return fmt.Sprintf("signaling HTTP %d", e.Status)
}

func status(err error) int {
	var e *HTTPError
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// IsGone means the room no longer exists for this client (deleted, expired,
// our token is no longer valid, or a host's Quick Tunnel was torn down -> 530).
func IsGone(err error) bool {
	s := status(err)
	return s == 404 || s == 410 || s == 403 || s == 530
}

func IsConflict(err error) bool { return status(err) == 409 }

type Client struct {
	httpClient *http.Client
	wsClient   *http.Client
	mu         sync.Mutex
	serverURL  string
	cancelWS   context.CancelFunc
	onEvent    func(string)
}

// compactCurves avoids Go 1.24+'s default X25519MLKEM768 ~1.6KB fragmented
// ClientHello, which Clash TUN Fake-IP / SNI middleboxes drop with EOF.
var compactCurves = []tls.CurveID{tls.X25519, tls.CurveP256}

func NewClient(serverURL string, onEvent func(string)) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{CurvePreferences: compactCurves}
	tr.ForceAttemptHTTP2 = true

	wsTr := http.DefaultTransport.(*http.Transport).Clone()
	wsTr.TLSClientConfig = &tls.Config{CurvePreferences: compactCurves}
	wsTr.ForceAttemptHTTP2 = false

	return &Client{
		serverURL:  strings.TrimRight(serverURL, "/"),
		httpClient: &http.Client{Transport: tr, Timeout: 8 * time.Second},
		wsClient:   &http.Client{Transport: wsTr},
		onEvent:    onEvent,
	}
}

// URL is the relay the current session uses (public server or a private Worker).
func (c *Client) URL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverURL
}

// SetURL switches relays between sessions.
func (c *Client) SetURL(u string) {
	c.mu.Lock()
	c.serverURL = strings.TrimRight(u, "/")
	c.mu.Unlock()
}

func (c *Client) IsConfigured() bool { return c.URL() != "" }

func (c *Client) request(ctx context.Context, method, path, token string, body any, out any) error {
	return c.requestAt(ctx, c.URL(), method, path, token, body, out)
}

func (c *Client) requestAt(ctx context.Context, base, method, path, token string, body any, out any) error {
	if base == "" {
		return errors.New("no signaling server configured")
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		var rd io.Reader
		if data != nil {
			rd = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, base+"/api/v1/rooms/"+path, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err = c.httpClient.Do(req)
		if err == nil {
			break
		}
		if attempt == 0 && ctx.Err() == nil && isTransientNetErr(err) {
			select {
			case <-ctx.Done():
				return err
			case <-time.After(350 * time.Millisecond):
				continue
			}
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func isTransientNetErr(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, ": EOF") || strings.Contains(s, "connection reset")
}

// CreateRoom reuses requestID on retries so a lost response does not leave a second room.
func (c *Client) CreateRoom(ctx context.Context, exitIP, requestID string) (*RemoteRoom, error) {
	var r RemoteRoom
	err := c.request(ctx, "POST", "create", "", map[string]string{"exitIp": exitIP, "requestId": requestID}, &r)
	return &r, err
}

// JoinRoom reuses joinID on retries; the server returns the same operator token.
func (c *Client) JoinRoom(ctx context.Context, key, joinID string) (*RemoteRoom, error) {
	var r RemoteRoom
	err := c.request(ctx, "POST", "join", "", map[string]string{"roomKey": key, "joinId": joinID}, &r)
	return &r, err
}

func (c *Client) ApproveRoom(ctx context.Context, key, token string) error {
	return c.request(ctx, "POST", "approve", token, map[string]string{"roomKey": key}, nil)
}

func (c *Client) LeaveRoom(ctx context.Context, key, token string) error {
	return c.request(ctx, "POST", "leave", token, map[string]string{"roomKey": key}, nil)
}

// LeaveRoomAt closes a room on a specific relay (used for retries after the
// client has moved on to another relay).
func (c *Client) LeaveRoomAt(ctx context.Context, base, key, token string) error {
	return c.requestAt(ctx, strings.TrimRight(base, "/"), "POST", "leave", token, map[string]string{"roomKey": key}, nil)
}

// Health is what a relay's /health reports.
type Health struct {
	Status   string `json:"status"`
	Protocol int    `json:"protocol"`
	Worker   bool   `json:"worker"`
}

// CheckHealth verifies that base is a TempRoom relay speaking our protocol.
func (c *Client) CheckHealth(ctx context.Context, base string) (*Health, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: resp.StatusCode}
	}
	var h Health
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&h); err != nil || h.Status != "ok" {
		return nil, errors.New("not a TempRoom relay")
	}
	if h.Protocol != 2 {
		return &h, fmt.Errorf("relay speaks protocol %d, this app needs 2; redeploy it", h.Protocol)
	}
	return &h, nil
}

func (c *Client) GetRoomStatus(ctx context.Context, key, token string) (*RemoteRoom, error) {
	var r RemoteRoom
	err := c.request(ctx, "GET", "status?key="+url.QueryEscape(key), token, nil, &r)
	return &r, err
}

func (c *Client) DisconnectWS() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancelWS != nil {
		c.cancelWS()
		c.cancelWS = nil
	}
}

// Subscribe opens the hint channel. Events only wake the poller; they never carry state.
func (c *Client) Subscribe(key, token string) {
	c.mu.Lock()
	if c.cancelWS != nil {
		c.cancelWS()
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancelWS = cancel
	c.mu.Unlock()
	go c.listen(ctx, key, token)
}

func (c *Client) listen(ctx context.Context, key, token string) {
	base := c.URL()
	wsURL := strings.Replace(strings.Replace(base, "https://", "wss://", 1), "http://", "ws://", 1) + "/ws?key=" + url.QueryEscape(key)
	delay := 500 * time.Millisecond
	for ctx.Err() == nil {
		dialCtx, done := context.WithTimeout(ctx, 8*time.Second)
		conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
			HTTPClient: c.wsClient,
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
		})
		done()
		if err == nil {
			delay = 500 * time.Millisecond
			for ctx.Err() == nil {
				_, data, rerr := conn.Read(ctx)
				if rerr != nil {
					break
				}
				var payload struct {
					Event string `json:"event"`
				}
				if json.Unmarshal(data, &payload) == nil && payload.Event != "" && c.onEvent != nil {
					c.onEvent(payload.Event)
				}
			}
			_ = conn.CloseNow()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 8*time.Second {
			delay *= 2
		}
	}
}

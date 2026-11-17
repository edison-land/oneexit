package tunnel

import (
	"context"
	"crypto/hmac"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const maxFrame = 1 << 20

// WSBase converts http(s)://host to ws(s)://host.
func WSBase(serverURL string) string {
	u := strings.TrimRight(serverURL, "/")
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	return "ws://" + strings.TrimPrefix(u, "http://")
}

func dialWS(ctx context.Context, rawURL, token string) (*websocket.Conn, error) {
	c, _, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(maxFrame)
	return c, nil
}

func dialStream(ctx context.Context, rawURL, token string) (net.Conn, error) {
	c, err := dialWS(ctx, rawURL, token)
	if err != nil {
		return nil, err
	}
	return websocket.NetConn(context.Background(), c, websocket.MessageBinary), nil
}

// Pipe copies both directions and closes both ends when either side ends.
func Pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// connSet tracks live connections so revocation can cut them immediately.
type connSet struct {
	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

func (s *connSet) add(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		_ = c.Close()
		return false
	}
	if s.conns == nil {
		s.conns = map[net.Conn]struct{}{}
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *connSet) remove(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	_ = c.Close()
}

func (s *connSet) closeAll() {
	s.mu.Lock()
	s.closed = true
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for c := range conns {
		_ = c.Close()
	}
}

// Provider runs on A. It keeps a control connection to the relay and, for each
// stream B opens, connects back, authenticates B and forwards bytes to exactly
// one local address: A's chosen proxy. Nothing else on A is reachable.
type Provider struct {
	base, key, token, target string
	secret                   Secret
	tlsCfg                   *tls.Config
	ctx                      context.Context
	cancel                   context.CancelFunc
	streams                  connSet
	active                   atomic.Int32
	mu                       sync.Mutex
	online                   bool
	lastErr                  string
}

func StartProvider(serverURL, roomKey, token string, s Secret, target string) (*Provider, error) {
	cfg, err := s.serverTLS()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Provider{base: WSBase(serverURL), key: roomKey, token: token, target: target, secret: s, tlsCfg: cfg, ctx: ctx, cancel: cancel}
	go p.run()
	return p, nil
}

func (p *Provider) Close() {
	p.cancel()
	p.streams.closeAll()
	p.setOnline(false, "")
}

// Status reports whether the relay control link is up and how many streams are open.
func (p *Provider) Status() (online bool, streams int, lastErr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.online, int(p.active.Load()), p.lastErr
}

func (p *Provider) setOnline(v bool, errText string) {
	p.mu.Lock()
	p.online, p.lastErr = v, errText
	p.mu.Unlock()
}

func (p *Provider) run() {
	delay := 500 * time.Millisecond
	ctlURL := p.base + "/ws/relay/provider?key=" + url.QueryEscape(p.key)
	for p.ctx.Err() == nil {
		dialCtx, done := context.WithTimeout(p.ctx, 8*time.Second)
		c, err := dialWS(dialCtx, ctlURL, p.token)
		done()
		if err == nil {
			p.setOnline(true, "")
			delay = 500 * time.Millisecond
			for {
				_, data, rerr := c.Read(p.ctx)
				if rerr != nil {
					break
				}
				var msg struct {
					Stream string `json:"stream"`
				}
				if json.Unmarshal(data, &msg) == nil && msg.Stream != "" {
					go p.serve(msg.Stream)
				}
			}
			_ = c.CloseNow()
			p.setOnline(false, "relay link lost; reconnecting")
		} else {
			p.setOnline(false, err.Error())
		}
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}

func (p *Provider) serve(id string) {
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	raw, err := dialStream(ctx, p.base+"/ws/relay/accept?key="+url.QueryEscape(p.key)+"&id="+url.QueryEscape(id), p.token)
	if err != nil {
		return
	}
	if !p.streams.add(raw) {
		return
	}
	defer p.streams.remove(raw)
	tc := tls.Server(raw, p.tlsCfg)
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	if err := tc.HandshakeContext(ctx); err != nil {
		return
	}
	want, err := p.secret.proof(tc.ConnectionState())
	if err != nil {
		return
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(tc, got); err != nil || !hmac.Equal(got, want) {
		return
	}
	_ = raw.SetDeadline(time.Time{})
	var d net.Dialer
	local, err := d.DialContext(ctx, "tcp", p.target)
	if err != nil {
		return // fail closed: never fall back to another route
	}
	if !p.streams.add(local) {
		return
	}
	defer p.streams.remove(local)
	p.active.Add(1)
	defer p.active.Add(-1)
	Pipe(tc, local)
}

// Consumer runs on B. It listens on a random loopback port; every connection
// is carried, encrypted, to A's proxy. Chrome is pointed at this port.
type Consumer struct {
	base, key, token string
	secret           Secret
	ln               net.Listener
	conns            connSet
	active           atomic.Int32
	ctx              context.Context
	cancel           context.CancelFunc
}

func StartConsumer(serverURL, roomKey, token string, s Secret) (*Consumer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Consumer{base: WSBase(serverURL), key: roomKey, token: token, secret: s, ln: ln, ctx: ctx, cancel: cancel}
	go c.acceptLoop()
	return c, nil
}

// Addr is the loopback address that speaks A's proxy protocol (SOCKS5/mixed).
func (c *Consumer) Addr() string { return c.ln.Addr().String() }

func (c *Consumer) Streams() int { return int(c.active.Load()) }

func (c *Consumer) Close() {
	c.cancel()
	_ = c.ln.Close()
	c.conns.closeAll()
}

func (c *Consumer) acceptLoop() {
	for {
		local, err := c.ln.Accept()
		if err != nil {
			return
		}
		go c.handle(local)
	}
}

func (c *Consumer) handle(local net.Conn) {
	if !c.conns.add(local) {
		return
	}
	defer c.conns.remove(local)
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	remote, err := c.Dial(ctx)
	cancel()
	if err != nil {
		return
	}
	if !c.conns.add(remote) {
		return
	}
	defer c.conns.remove(remote)
	c.active.Add(1)
	defer c.active.Add(-1)
	Pipe(local, remote)
}

// Dial opens one authenticated, encrypted stream to A's proxy.
func (c *Consumer) Dial(ctx context.Context) (net.Conn, error) {
	raw, err := dialStream(ctx, c.base+"/ws/relay/connect?key="+url.QueryEscape(c.key), c.token)
	if err != nil {
		return nil, err
	}
	tc := tls.Client(raw, c.secret.clientTLS())
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		if errors.Is(err, errWrongHost) || strings.Contains(err.Error(), errWrongHost.Error()) {
			return nil, errWrongHost
		}
		return nil, err
	}
	pr, err := c.secret.proof(tc.ConnectionState())
	if err == nil {
		_, err = tc.Write(pr)
	}
	if err != nil {
		_ = tc.Close()
		return nil, err
	}
	return tc, nil
}

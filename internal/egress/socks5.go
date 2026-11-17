package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var DefaultProbePorts = []int{7890, 7891, 2080, 1080, 10808, 1086}

type ProbeResult struct {
	Address    string `json:"address"`
	Available  bool   `json:"available"`
	UDPSupport bool   `json:"udpSupport"`
	ExitIP     string `json:"exitIp,omitempty"`
	LatencyMs  int64  `json:"latencyMs"`
	Error      string `json:"error,omitempty"`
}

// SOCKS5Probe checks if a target address responds to SOCKS5 handshakes and UDP associate
type SOCKS5Probe struct {
	Timeout time.Duration
	// ExitIPURLs are queried through the proxy; each must return a bare IP.
	ExitIPURLs []string
}

var DefaultExitIPURLs = []string{"https://api.ipify.org", "https://ifconfig.me/ip"}

func NewSOCKS5Probe(timeout time.Duration) *SOCKS5Probe {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &SOCKS5Probe{Timeout: timeout, ExitIPURLs: DefaultExitIPURLs}
}

// DiscoverLocal looks up common local proxy ports on 127.0.0.1
func (p *SOCKS5Probe) DiscoverLocal(ctx context.Context) []ProbeResult {
	var results []ProbeResult
	for _, port := range DefaultProbePorts {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		res := p.Probe(ctx, addr)
		if res.Available {
			results = append(results, res)
		}
	}
	return results
}

// Probe evaluates SOCKS5 TCP handshake, UDP associate capability, and exit IP
func (p *SOCKS5Probe) Probe(ctx context.Context, addr string) ProbeResult {
	start := time.Now()
	res := ProbeResult{Address: addr}

	d := net.Dialer{Timeout: p.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(p.Timeout))

	// 1. SOCKS5 Handshake: [VER=0x05, NMETHODS=1, METHOD=0x00(NO AUTH)]
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		res.Error = "failed to send handshake: " + err.Error()
		return res
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		res.Error = "failed to read handshake response: " + err.Error()
		return res
	}

	if resp[0] != 0x05 || resp[1] != 0x00 {
		res.Error = fmt.Sprintf("unsupported socks5 response: 0x%02x 0x%02x", resp[0], resp[1])
		return res
	}

	res.Available = true
	res.LatencyMs = time.Since(start).Milliseconds()

	// 2. Test UDP ASSOCIATE
	// Command: [VER=0x05, CMD=0x03 (UDP ASSOCIATE), RSV=0x00, ATYP=0x01 (IPv4), DST.ADDR=0.0.0.0, DST.PORT=0]
	udpReq := []byte{0x05, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if _, err := conn.Write(udpReq); err == nil {
		udpResp := make([]byte, 10)
		if n, err := io.ReadAtLeast(conn, udpResp, 4); err == nil && n >= 4 {
			// REP=0x00 means succeeded
			if udpResp[1] == 0x00 {
				res.UDPSupport = true
			}
		}
	}

	// 3. Resolve Exit IP via SOCKS5 proxy
	if exitIP, err := p.fetchExitIP(ctx, addr); err == nil {
		res.ExitIP = exitIP
	}

	return res
}

// FetchExitIP returns the public IP seen by websites when connecting through proxyAddr (SOCKS5).
func (p *SOCKS5Probe) FetchExitIP(ctx context.Context, proxyAddr string) (string, error) {
	return p.fetchExitIP(ctx, proxyAddr)
}

func (p *SOCKS5Probe) fetchExitIP(ctx context.Context, proxyAddr string) (string, error) {
	proxyURL, err := url.Parse("socks5://" + proxyAddr)
	if err != nil {
		return "", err
	}
	transport := &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		DialContext:         (&net.Dialer{Timeout: p.Timeout}).DialContext,
		TLSHandshakeTimeout: p.Timeout,
		DisableKeepAlives:   true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: p.Timeout}
	urls := p.ExitIPURLs
	if len(urls) == 0 {
		urls = DefaultExitIPURLs
	}
	lastErr := errors.New("no exit IP endpoint configured")
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
		resp.Body.Close()
		ip := strings.TrimSpace(string(body))
		if err == nil && resp.StatusCode == http.StatusOK && net.ParseIP(ip) != nil {
			return ip, nil
		}
		lastErr = fmt.Errorf("unexpected exit IP response from %s", u)
	}
	return "", lastErr
}

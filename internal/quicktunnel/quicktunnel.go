// Package quicktunnel spins up an ephemeral, zero-login Cloudflare Quick Tunnel
// (*.trycloudflare.com) pointing at an in-process TempRoom relay bound to
// 127.0.0.1:0. When the session ends, both the cloudflared connector and the
// local loopback server are torn down immediately.
package quicktunnel

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"temproom/internal/server"
)

var tryCloudflareRE = regexp.MustCompile(`https://([a-z0-9-]+)\.trycloudflare\.com`)

// Tunnel owns an in-process relay on 127.0.0.1 and the cloudflared process
// exposing it at a disposable *.trycloudflare.com URL.
type Tunnel struct {
	localURL  string
	publicURL string
	srv       *server.Server
	httpSrv   *http.Server
	ln        net.Listener
	cmd       *exec.Cmd
	exited    chan struct{}
	closeOnce sync.Once
}

func (t *Tunnel) LocalURL() string  { return t.localURL }
func (t *Tunnel) PublicURL() string { return t.publicURL }

func (t *Tunnel) Close() error {
	t.closeOnce.Do(func() {
		if t.cmd != nil && t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
			if t.exited != nil {
				select {
				case <-t.exited:
				case <-time.After(2 * time.Second):
				}
			}
		}
		if t.httpSrv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = t.httpSrv.Shutdown(ctx)
			cancel()
		}
		if t.ln != nil {
			_ = t.ln.Close()
		}
		if t.srv != nil {
			t.srv.Close()
		}
	})
	return nil
}

// Start opens a local in-memory relay on 127.0.0.1:0 and exposes it through a
// temporary Cloudflare Quick Tunnel. If cloudflared is not installed on this
// Mac, it is downloaded once into cacheDir/bin using proxyAddr.
func Start(ctx context.Context, cacheDir, proxyAddr string) (*Tunnel, error) {
	bin, err := EnsureBinary(ctx, cacheDir, proxyAddr)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open local relay port: %w", err)
	}
	srv := server.NewServer()
	httpSrv := &http.Server{
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = httpSrv.Serve(ln) }()

	localURL := "http://" + ln.Addr().String()

	// Try direct edge connection first (works with TUN mode or direct internet);
	// if that times out and a SOCKS5/mixed proxy is available, retry with proxy env.
	firstCtx, cancelFirst := context.WithTimeout(ctx, 14*time.Second)
	cmd, exited, pubURL, err := launchCloudflared(firstCtx, bin, localURL, "")
	cancelFirst()
	if err != nil && proxyAddr != "" && ctx.Err() == nil {
		retryCtx, cancelRetry := context.WithTimeout(ctx, 16*time.Second)
		cmd, exited, pubURL, err = launchCloudflared(retryCtx, bin, localURL, proxyAddr)
		cancelRetry()
	}
	if err != nil {
		_ = httpSrv.Close()
		_ = ln.Close()
		srv.Close()
		return nil, err
	}

	waitPublicReady(ctx, pubURL, proxyAddr)

	return &Tunnel{
		localURL:  localURL,
		publicURL: pubURL,
		srv:       srv,
		httpSrv:   httpSrv,
		ln:        ln,
		cmd:       cmd,
		exited:    exited,
	}, nil
}

// waitPublicReady polls the freshly minted *.trycloudflare.com /health endpoint
// briefly so edge DNS & TLS routing settle before the invite is displayed.
func waitPublicReady(ctx context.Context, pubURL, proxyAddr string) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	direct := httpClientViaProxy("", 2*time.Second)
	var proxied *http.Client
	if proxyAddr != "" {
		proxied = httpClientViaProxy(proxyAddr, 2*time.Second)
	}
	target := strings.TrimRight(pubURL, "/") + "/health"
	for {
		if probeHealth(probeCtx, direct, target) {
			return
		}
		if proxied != nil && probeHealth(probeCtx, proxied, target) {
			return
		}
		select {
		case <-probeCtx.Done():
			return
		case <-time.After(350 * time.Millisecond):
		}
	}
}

func probeHealth(ctx context.Context, client *http.Client, target string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 256))
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func launchCloudflared(ctx context.Context, bin, localURL, proxyAddr string) (*exec.Cmd, chan struct{}, string, error) {
	nullCfg := os.DevNull
	args := []string{
		"tunnel",
		"--config", nullCfg,
		"--no-autoupdate",
		"--protocol", "http2",
		"--url", localURL,
	}
	cmd := exec.Command(bin, args...)
	if proxyAddr != "" {
		pURL := "socks5://" + proxyAddr
		env := filterEnv(os.Environ(), "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy")
		env = append(env,
			"HTTPS_PROXY="+pURL,
			"HTTP_PROXY="+pURL,
			"NO_PROXY=127.0.0.1,localhost",
		)
		cmd.Env = env
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, "", err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, "", fmt.Errorf("start cloudflared: %w", err)
	}

	exited := make(chan struct{})
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			default:
			}
		}
		close(lines)
	}()
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	var pubURL string
	var registered bool
	var lastLine string

	for {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-exited
			if lastLine != "" {
				return nil, nil, "", fmt.Errorf("quick tunnel timed out (%s)", lastLine)
			}
			return nil, nil, "", errors.New("quick tunnel timed out waiting for Cloudflare edge")
		case line, ok := <-lines:
			if !ok {
				_ = cmd.Process.Kill()
				<-exited
				if lastLine != "" {
					return nil, nil, "", fmt.Errorf("cloudflared exited early: %s", lastLine)
				}
				return nil, nil, "", errors.New("cloudflared exited before registering tunnel")
			}
			if strings.TrimSpace(line) != "" {
				lastLine = strings.TrimSpace(line)
			}
			if m := tryCloudflareRE.FindStringSubmatch(line); len(m) == 2 && m[1] != "api" {
				pubURL = m[0]
			}
			if strings.Contains(line, "Registered tunnel connection") {
				registered = true
			}
			if pubURL != "" && registered {
				// Drain future stderr in background so cloudflared never blocks on pipe writes.
				go func() {
					for range lines {
					}
				}()
				return cmd, exited, pubURL, nil
			}
		}
	}
}

func filterEnv(env []string, dropKeys ...string) []string {
	drop := make(map[string]bool, len(dropKeys))
	for _, k := range dropKeys {
		drop[k] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && drop[k] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// EnsureBinary locates an existing cloudflared binary or downloads the official
// Cloudflare release into cacheDir/bin on macOS/Linux.
func EnsureBinary(ctx context.Context, cacheDir, proxyAddr string) (string, error) {
	if p := locateExisting(cacheDir); p != "" {
		return p, nil
	}
	if cacheDir == "" {
		return "", errors.New("cloudflared not found and no cache directory available")
	}
	dlURL, isTGZ, err := downloadSpec()
	if err != nil {
		return "", err
	}
	destDir := filepath.Join(cacheDir, "bin")
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	binName := "cloudflared"
	if runtime.GOOS == "windows" {
		binName = "cloudflared.exe"
	}
	dest := filepath.Join(destDir, binName)
	tmp := dest + ".tmp"
	_ = os.Remove(tmp)

	client := httpClientViaProxy(proxyAddr, 90*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download cloudflared helper: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download cloudflared helper: HTTP %d", resp.StatusCode)
	}

	if isTGZ {
		if err := extractTGZBinary(resp.Body, tmp); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("extract cloudflared: %w", err)
		}
	} else {
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 80<<20))
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("save cloudflared: %v", copyErr)
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

func locateExisting(cacheDir string) string {
	binName := "cloudflared"
	if runtime.GOOS == "windows" {
		binName = "cloudflared.exe"
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), binName))
	}
	if cacheDir != "" {
		candidates = append(candidates, filepath.Join(cacheDir, "bin", binName))
	}
	candidates = append(candidates,
		"/opt/homebrew/bin/cloudflared",
		"/usr/local/bin/cloudflared",
	)
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath(binName); err == nil {
		return p
	}
	return ""
}

func downloadSpec() (string, bool, error) {
	const base = "https://github.com/cloudflare/cloudflared/releases/latest/download/"
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return base + "cloudflared-darwin-arm64.tgz", true, nil
		}
		return base + "cloudflared-darwin-amd64.tgz", true, nil
	case "linux":
		if runtime.GOARCH == "arm64" {
			return base + "cloudflared-linux-arm64", false, nil
		}
		return base + "cloudflared-linux-amd64", false, nil
	default:
		return "", false, fmt.Errorf("automatic cloudflared download is not supported on %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func httpClientViaProxy(proxyAddr string, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		TLSHandshakeTimeout: 15 * time.Second,
		TLSClientConfig:     &tls.Config{CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}},
		ForceAttemptHTTP2:   true,
	}
	if proxyAddr != "" {
		if u, err := url.Parse("socks5://" + proxyAddr); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func extractTGZBinary(r io.Reader, destPath string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("cloudflared binary not found in archive")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "cloudflared" {
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, io.LimitReader(tr, 80<<20))
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}
	}
}

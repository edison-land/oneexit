//go:build live

// Live check on one Mac with a real local proxy and the real internet:
//
//	TEMPROOM_LIVE_PROXY=127.0.0.1:7890 go test -tags live -run Live -v ./internal/app
//
// Set TEMPROOM_LIVE_SERVER=http://host to use a deployed signaling server.
package app

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"temproom/internal/browser"
	"temproom/internal/server"
)

func TestLiveSharing(t *testing.T) {
	proxy := os.Getenv("TEMPROOM_LIVE_PROXY")
	if proxy == "" {
		proxy = "127.0.0.1:7890"
	}
	url := os.Getenv("TEMPROOM_LIVE_SERVER")
	if url == "" {
		srv := server.NewServer()
		ts := httptest.NewServer(srv.Routes())
		defer func() { ts.Close(); srv.Close() }()
		url = ts.URL
	}
	t.Setenv("TEMPROOM_SERVER_URL", url)
	t.Setenv("TEMPROOM_CONFIG_DIR", t.TempDir())
	a, b := NewService(), NewService()
	defer a.Close()
	defer b.Close()

	ra, err := a.CreateRoom(proxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("host proxy exit %s, host direct %s, warning=%q", ra.Network.ExpectedExitIP, ra.Network.DirectIP, ra.Network.Warning)
	if _, err := b.JoinRoom(ra.Invite); err != nil {
		t.Fatal(err)
	}
	a.syncOnce()
	if _, err := a.ApproveJoin(); err != nil {
		t.Fatal(err)
	}
	// Live internet verification has a 20s budget, unlike the local test's
	// 8s waitFor helper. Include relay setup and polling without weakening
	// the required VERIFIED result below.
	deadline := time.Now().Add(30 * time.Second)
	for {
		b.syncOnce()
		s := b.GetSession()
		if s == nil {
			t.Fatal("session ended during network verification")
		}
		if s.Network.State != NetConnecting && s.Network.State != NetIdle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("network verification timed out: state=%s message=%q", s.Network.State, s.Network.Message)
		}
		time.Sleep(250 * time.Millisecond)
	}
	n := b.GetSession().Network
	t.Logf("operator: state=%s exit=%s direct=%s msg=%q warn=%q", n.State, n.ExitIP, n.DirectIP, n.Message, n.Warning)
	if n.State != NetVerified {
		t.Fatalf("not verified: %s", n.Message)
	}

	// Real Chrome with the production flags, headless, must see the host's exit.
	if bin, err := browser.LocateChrome(); err == nil {
		b.mu.Lock()
		port := b.consumer.Addr()
		b.mu.Unlock()
		dir := t.TempDir()
		args := append([]string{"--headless=new", "--dump-dom"}, browser.Args(dir, port, "https://api.ipify.org")...)
		run := func() string {
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			out, _ := exec.CommandContext(ctx, bin, args...).Output()
			return strings.TrimSpace(stripTags(string(out)))
		}
		page := run()
		t.Logf("chrome via tunnel saw: %q", page)
		if !strings.Contains(page, n.ExitIP) {
			t.Fatalf("Chrome did not exit through the tunnel")
		}
		// Fail-closed: with the tunnel port gone Chrome must not load anything.
		b.mu.Lock()
		b.consumer.Close()
		b.mu.Unlock()
		if page := run(); strings.Contains(page, n.ExitIP) || strings.Count(page, ".") == 3 {
			t.Fatalf("Chrome reached the internet without the tunnel: %q", page)
		} else {
			t.Logf("chrome without tunnel saw: %q (blocked, as required)", page)
		}
	}
	if _, err := a.LeaveRoom(); err != nil {
		t.Fatal(err)
	}
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

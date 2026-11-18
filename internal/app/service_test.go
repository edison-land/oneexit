package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"temproom/internal/room"
	"temproom/internal/server"
	"temproom/internal/testutil"
)

const hostExit = "203.0.113.7"    // what websites see through A's proxy
const operatorIP = "198.51.100.1" // B's own broadband

type env struct {
	a, b  *Service
	proxy *testutil.SOCKS5
}

// setup wires two services to a local signaling server. A's "Clash" is a test
// SOCKS5 server; the exit-IP endpoint is local, so nothing touches the internet.
func setup(t *testing.T) env {
	t.Helper()
	return setupRelays(t, startRelay(t), "")
}

func startRelay(t *testing.T) string {
	srv := server.NewServer()
	remote := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { remote.Close(); srv.Close() })
	return remote.URL
}

// setupRelays gives both services publicURL as their default relay. If
// hostRelay is set, A switches to it as "My Cloudflare Worker" and B is left
// unconfigured: B must find A's relay from the invite alone.
func setupRelays(t *testing.T, publicURL, hostRelay string) env {
	t.Helper()
	ipEcho := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(hostExit)) }))
	t.Cleanup(ipEcho.Close)
	t.Setenv("TEMPROOM_SERVER_URL", publicURL)
	mk := func(ip string) *Service {
		t.Setenv("TEMPROOM_CONFIG_DIR", t.TempDir()) // each Mac has its own settings
		s := NewService()
		t.Cleanup(s.Close)
		s.Probe.ExitIPURLs = []string{ipEcho.URL}
		s.TunnelProbe.ExitIPURLs = []string{ipEcho.URL}
		s.DirectIP = func(context.Context) (string, error) { return ip, nil }
		return s
	}
	e := env{a: mk("192.0.2.50"), b: mk(operatorIP), proxy: testutil.StartSOCKS5(t)}
	if hostRelay != "" {
		if _, h, err := e.a.SetRelay("custom", hostRelay); err != nil || !h.OK {
			t.Fatalf("host relay %s: %v %+v", hostRelay, err, h)
		}
	}
	return e
}

// privateRelay is A's own relay: TEMPROOM_TEST_WORKER (e.g. a local
// `wrangler dev` at http://127.0.0.1:8787) or, by default, a second Go relay.
func privateRelay(t *testing.T) string {
	if u := os.Getenv("TEMPROOM_TEST_WORKER"); u != "" {
		return u
	}
	return startRelay(t)
}

const deadRelay = "http://127.0.0.1:1" // B's default relay is unreachable on purpose

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEndToEndSharingVerifiedAndRevoked(t *testing.T) { runSharing(t, setup(t)) }

func TestPrivateRelayFoundFromInvite(t *testing.T) {
	e := setupRelays(t, deadRelay, privateRelay(t))
	ra, err := e.a.CreateRoom(e.proxy.Addr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ra.Invite, "@") || ra.Relay == "" {
		t.Fatalf("invite does not carry the private relay: %+v", ra)
	}
	runSharingFrom(t, e, ra)
}

func TestPrivateRelayRejectAndRetries(t *testing.T) {
	runRejectAndRetries(t, setupRelays(t, deadRelay, privateRelay(t)))
}

func runSharing(t *testing.T, e env) {
	ra, err := e.a.CreateRoom(e.proxy.Addr)
	if err != nil {
		t.Fatal(err)
	}
	runSharingFrom(t, e, ra)
}

func runSharingFrom(t *testing.T, e env, ra *SessionView) {
	a, b := e.a, e.b
	if ra.Invite == "" || !strings.HasPrefix(ra.Invite, ra.RoomKey) {
		t.Fatalf("invite missing: %+v", ra)
	}
	if _, err := b.JoinRoom(ra.RoomKey); err == nil {
		t.Fatal("joined with the room key alone (no secret)")
	}
	if _, err := b.JoinRoom(ra.Invite); err != nil {
		t.Fatal(err)
	}
	if err := b.LaunchChrome(""); err == nil {
		t.Fatal("browser allowed before approval")
	}
	a.syncOnce()
	if a.GetSession().State != room.StatePendingApproval {
		t.Fatal("host did not see the join request")
	}
	if _, err := a.ApproveJoin(); err != nil {
		t.Fatal(err)
	}
	b.syncOnce()
	waitFor(t, "verified exit through tunnel", func() bool {
		b.syncOnce()
		s := b.GetSession()
		return s != nil && s.Network.State == NetVerified
	})
	if got := b.GetSession().Network.ExitIP; got != hostExit {
		t.Fatalf("exit through tunnel = %q, want %q", got, hostExit)
	}

	// Fail closed: A's proxy dies -> B's check fails, browser stays blocked.
	e.proxy.Close()
	b.mu.Lock()
	b.lastCheck = time.Time{}
	b.mu.Unlock()
	waitFor(t, "failure detected", func() bool {
		b.syncOnce()
		return b.GetSession().Network.State == NetFailed
	})
	if err := b.LaunchChrome(""); err == nil {
		t.Fatal("browser allowed while the host network is down")
	}

	// A ends: B's tunnel port is closed and B learns why.
	b.mu.Lock()
	port := b.consumer.Addr()
	b.mu.Unlock()
	results, err := a.LeaveRoom()
	if err != nil || len(results) == 0 {
		t.Fatal(err)
	}
	b.syncOnce()
	if b.GetSession() != nil {
		t.Fatal("operator session survived the host leaving")
	}
	if end := b.LastEnded(); end == nil || end.Reason == "" {
		t.Fatal("operator was not told why the session ended")
	}
	if c, err := (&netDialer{}).dial(port); err == nil {
		c.Close()
		t.Fatal("tunnel port still open after the session ended")
	}
}

func TestCreateRejectsDeadProxy(t *testing.T) {
	e := setup(t)
	if _, err := e.a.CreateRoom("127.0.0.1:1"); err == nil {
		t.Fatal("created a room with a dead proxy")
	}
}

func TestRejectAndRetries(t *testing.T) { runRejectAndRetries(t, setup(t)) }

func runRejectAndRetries(t *testing.T, e env) {
	a, b := e.a, e.b
	ra, err := a.CreateRoom(e.proxy.Addr)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := a.CreateRoom(e.proxy.Addr); again.RoomKey != ra.RoomKey {
		t.Fatal("double create made a second room")
	}
	if _, err := b.JoinRoom(ra.Invite); err != nil {
		t.Fatal(err)
	}
	if again, err := b.JoinRoom(ra.Invite); err != nil || again.RoomKey != ra.RoomKey {
		t.Fatal("join retry failed")
	}
	a.syncOnce()
	if _, err := a.LeaveRoom(); err != nil { // "Decline" closes the room
		t.Fatal(err)
	}
	b.syncOnce()
	if b.GetSession() != nil || b.LastEnded() == nil {
		t.Fatal("declined operator not cleaned up with a reason")
	}
}

type fakeQuickRelay struct {
	url    string
	closed bool
}

func (f *fakeQuickRelay) LocalURL() string  { return f.url }
func (f *fakeQuickRelay) PublicURL() string { return f.url }
func (f *fakeQuickRelay) Close() error      { f.closed = true; return nil }

// The open-source build ships without a hardcoded server: new rooms default to
// an ephemeral Quick Tunnel (zero login), while invites without @relay are
// still rejected with a clear message.
func TestNoDefaultRelayBuildUsesQuickTunnel(t *testing.T) {
	e := setupRelays(t, "", "")
	if v := e.a.RelayConfig(); v.PublicAvailable || v.Mode != "quick" {
		t.Fatalf("unexpected default relay config: %+v", v)
	}
	if _, _, err := e.a.SetRelay("public", ""); err == nil {
		t.Fatal("selected a public relay that does not exist")
	}
	if _, err := e.b.JoinRoom("ABCD-EF23-" + strings.Repeat("A", 26)); err == nil || !strings.Contains(err.Error(), "full invite") {
		t.Fatalf("join without relay: %v", err)
	}

	// Out-of-the-box Quick Tunnel mode works immediately without any Worker setup.
	fq := &fakeQuickRelay{url: startRelay(t)}
	e.a.StartQuick = func(context.Context, string, string) (QuickRelay, error) { return fq, nil }
	ra, err := e.a.CreateRoom(e.proxy.Addr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ra.Invite, "@") {
		t.Fatalf("quick tunnel invite missing @relay: %+v", ra)
	}
	runSharingFrom(t, e, ra)
	if !fq.closed {
		t.Fatal("quick tunnel was not torn down when the session ended")
	}
}

// An http:// default relay (allowed for the built-in server) must still pass
// its own health check; only user-entered Worker URLs require https.
func TestPublicRelayHealth(t *testing.T) {
	e := setup(t)
	if h := e.a.CheckRelay(""); !h.OK {
		t.Fatalf("public relay health: %+v", h)
	}
	if h := e.a.CheckRelay(e.a.PublicURL); !h.OK {
		t.Fatalf("public relay health by url: %+v", h)
	}
}

func TestLiveQuickTunnelEndToEnd(t *testing.T) {
	if os.Getenv("TEMPROOM_TEST_QUICK") == "" {
		t.Skip("set TEMPROOM_TEST_QUICK=1 to test against live trycloudflare.com")
	}
	e := setupRelays(t, "", "")
	ra, err := e.a.CreateRoom(e.proxy.Addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live quick tunnel invite: %s", ra.Invite)
	if !strings.HasSuffix(ra.Invite, ".trycloudflare.com") {
		t.Fatalf("expected trycloudflare.com invite, got %s", ra.Invite)
	}
	runSharingFrom(t, e, ra)
}

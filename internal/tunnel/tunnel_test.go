package tunnel

import (
	"bufio"
	"io"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"temproom/internal/server"
)

func TestInviteRoundTrip(t *testing.T) {
	s, _ := NewSecret()
	inv := FormatInvite("ABCD-EF23", s, "")
	for _, in := range []string{inv, "  " + inv + "\n", "abcdef23" + s.String()} {
		key, got, relay, err := ParseInvite(in)
		if err != nil || key != "ABCD-EF23" || got != s || relay != "" {
			t.Fatalf("parse %q: %v %q %v", in, key, relay, err)
		}
	}
	if _, _, _, err := ParseInvite("ABCD-EF23"); err == nil {
		t.Fatal("room key alone must not be accepted as an invite")
	}
}

func TestInviteCarriesPrivateRelay(t *testing.T) {
	s, _ := NewSecret()
	for relay, want := range map[string]string{
		"https://my-relay.alice.workers.dev": "ABCD-EF23-" + s.String() + "@my-relay.alice.workers.dev",
		"http://127.0.0.1:8787":              "ABCD-EF23-" + s.String() + "@http://127.0.0.1:8787",
	} {
		inv := FormatInvite("ABCD-EF23", s, relay)
		if inv != want {
			t.Fatalf("format = %q, want %q", inv, want)
		}
		key, got, gotRelay, err := ParseInvite(" " + inv + " ")
		if err != nil || key != "ABCD-EF23" || got != s || gotRelay != relay {
			t.Fatalf("parse %q: %v %q %v", inv, key, gotRelay, err)
		}
	}
	if _, _, _, err := ParseInvite("ABCD-EF23-" + s.String() + "@"); err == nil {
		t.Fatal("empty relay accepted")
	}
}

// echoServer stands in for A's proxy: whatever arrives through the tunnel is echoed.
func echoServer(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String()
}

type room struct{ url, key, reqTok, opTok string }

func activeRoom(t *testing.T) room {
	srv := server.NewServer()
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.Close() })
	hub := server.NewHubForTest(srv)
	r, _ := hub.Create("", "")
	j, err := hub.Join(r.RoomKey, "join-id-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Approve(r.RoomKey, r.Token); err != nil {
		t.Fatal(err)
	}
	return room{ts.URL, r.RoomKey, r.Token, j.Token}
}

func waitOnline(t *testing.T, p *Provider) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if on, _, _ := p.Status(); on {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("provider never came online")
}

func roundTrip(addr string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("ping through tunnel\n")); err != nil {
		return "", err
	}
	return bufio.NewReader(c).ReadString('\n')
}

func TestTunnelCarriesBytesAndRevokes(t *testing.T) {
	rm := activeRoom(t)
	secret, _ := NewSecret()
	p, err := StartProvider(rm.url, rm.key, rm.reqTok, secret, echoServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	waitOnline(t, p)
	c, err := StartConsumer(rm.url, rm.key, rm.opTok, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := roundTrip(c.Addr())
	if err != nil || got != "ping through tunnel\n" {
		t.Fatalf("tunnel round trip: %q %v", got, err)
	}
	p.Close() // A revokes
	if got, err := roundTrip(c.Addr()); err == nil && got != "" {
		t.Fatalf("traffic still flowed after the host revoked: %q", got)
	}
}

func TestWrongSecretIsRejected(t *testing.T) {
	rm := activeRoom(t)
	good, _ := NewSecret()
	bad, _ := NewSecret()
	p, _ := StartProvider(rm.url, rm.key, rm.reqTok, good, echoServer(t))
	defer p.Close()
	waitOnline(t, p)
	c, _ := StartConsumer(rm.url, rm.key, rm.opTok, bad)
	defer c.Close()
	if got, err := roundTrip(c.Addr()); err == nil && got != "" {
		t.Fatalf("a consumer without the invite secret reached the proxy: %q", got)
	}
}

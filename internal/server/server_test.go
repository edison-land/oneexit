package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func call(t *testing.T, h http.Handler, method, path, token string, body any) (*httptest.ResponseRecorder, Room) {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var r Room
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	return rec, r
}

func TestRoleTokensGuardEveryMutation(t *testing.T) {
	srv := NewServer()
	defer srv.Close()
	h := srv.Routes()
	rec, a := call(t, h, "POST", "/api/v1/rooms/create", "", map[string]string{"exitIp": "203.0.113.7", "requestId": "req-1"})
	if rec.Code != 200 || a.Token == "" || a.State != StateWaiting {
		t.Fatalf("create: %d %+v", rec.Code, a)
	}
	// Retry with the same request ID returns the same room (lost-response safety).
	if _, again := call(t, h, "POST", "/api/v1/rooms/create", "", map[string]string{"requestId": "req-1"}); again.RoomKey != a.RoomKey {
		t.Fatal("create retry produced a second room")
	}
	key := map[string]string{"roomKey": a.RoomKey}
	if rec, _ := call(t, h, "GET", "/api/v1/rooms/status?key="+a.RoomKey, "", nil); rec.Code != 403 {
		t.Fatalf("status without token: %d", rec.Code)
	}
	rec, b := call(t, h, "POST", "/api/v1/rooms/join", "", map[string]string{"roomKey": a.RoomKey, "joinId": "b-join-0123456789"})
	if rec.Code != 200 || b.Token == "" || b.State != StatePendingApproval {
		t.Fatalf("join: %d %+v", rec.Code, b)
	}
	if rec, _ := call(t, h, "POST", "/api/v1/rooms/join", "", map[string]string{"roomKey": a.RoomKey, "joinId": "intruder-0123456789"}); rec.Code != 409 {
		t.Fatalf("third party join: %d", rec.Code)
	}
	for _, tok := range []string{"", b.Token, "bogus"} {
		if rec, _ := call(t, h, "POST", "/api/v1/rooms/approve", tok, key); rec.Code != 403 {
			t.Fatalf("approve with %q: %d", tok, rec.Code)
		}
	}
	if rec, r := call(t, h, "POST", "/api/v1/rooms/approve", a.Token, key); rec.Code != 200 || r.State != StateActive {
		t.Fatalf("approve: %d %+v", rec.Code, r)
	}
	// B retrying its join after approval gets the same token, not a 409.
	if rec, again := call(t, h, "POST", "/api/v1/rooms/join", "", map[string]string{"roomKey": a.RoomKey, "joinId": "b-join-0123456789"}); rec.Code != 200 || again.Token != b.Token {
		t.Fatalf("join retry after approval: %d", rec.Code)
	}
	if rec, _ := call(t, h, "POST", "/api/v1/rooms/leave", "bogus", key); rec.Code != 403 {
		t.Fatalf("leave with bad token: %d", rec.Code)
	}
	if rec, _ := call(t, h, "POST", "/api/v1/rooms/leave", b.Token, key); rec.Code != 200 {
		t.Fatalf("leave: %d", rec.Code)
	}
	if rec, _ := call(t, h, "GET", "/api/v1/rooms/status?key="+a.RoomKey, a.Token, nil); rec.Code != 404 {
		t.Fatalf("closed room still visible: %d", rec.Code)
	}
}

func TestLeaseExpiresSilentPeer(t *testing.T) {
	old := LeaseTTL
	LeaseTTL = 50 * time.Millisecond
	defer func() { LeaseTTL = old }()
	h := NewHub()
	defer close(h.stop)
	r, _ := h.Create("", "")
	if _, err := h.Join(r.RoomKey, "b-join-0123456789"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := h.Status(r.RoomKey, r.Token); err != nil { // A keeps polling; B went silent
		t.Fatal(err)
	}
	h.sweep()
	if _, err := h.Status(r.RoomKey, r.Token); err == nil {
		t.Fatal("room survived a silent operator")
	}
}

func TestRelayRequiresApproval(t *testing.T) {
	srv := NewServer()
	defer srv.Close()
	h := srv.Routes()
	r, _ := srv.hub.Create("", "")
	j, _ := srv.hub.Join(r.RoomKey, "b-join-0123456789")
	req := httptest.NewRequest("GET", "/ws/relay/connect?key="+r.RoomKey, nil)
	req.Header.Set("Authorization", "Bearer "+j.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("relay before approval: %d", rec.Code)
	}
}

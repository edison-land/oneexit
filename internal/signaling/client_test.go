package signaling

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestMutationHTTPFailures(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "missing", 404) }))
	defer remote.Close()
	c := NewClient(remote.URL, nil)
	for _, err := range []error{c.ApproveRoom(context.Background(), "ABCD-2345", "t"), c.LeaveRoom(context.Background(), "ABCD-2345", "t")} {
		if !IsGone(err) {
			t.Fatalf("ignored HTTP failure: %v", err)
		}
	}
}

func TestWebSocketReconnectAndCancel(t *testing.T) {
	registered := make(chan string, 8)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		registered <- auth
		time.Sleep(50 * time.Millisecond)
		c.CloseNow()
	}))
	defer remote.Close()
	client := NewClient(remote.URL, nil)
	client.Subscribe("ABCD-2345", "tok")
	defer client.DisconnectWS()
	for i := 0; i < 2; i++ {
		select {
		case a := <-registered:
			if a != "Bearer tok" {
				t.Fatalf("hint channel sent auth %q", a)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no reconnect")
		}
	}
	client.DisconnectWS()
	time.Sleep(100 * time.Millisecond)
	for len(registered) > 0 {
		<-registered
	}
	select {
	case <-registered:
		t.Fatal("reconnected after cancel")
	case <-time.After(1200 * time.Millisecond):
	}
}

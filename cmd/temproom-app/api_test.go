package main

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"temproom/internal/app"
	"temproom/internal/server"
)

func TestAPIEndpoints(t *testing.T) {
	srv := server.NewServer()
	remote := httptest.NewServer(srv.Routes())
	defer func() { remote.Close(); srv.Close() }()
	t.Setenv("TEMPROOM_SERVER_URL", remote.URL)
	t.Setenv("TEMPROOM_CONFIG_DIR", t.TempDir())
	svc := app.NewService()
	defer svc.Close()
	h := NewAPIHandler(svc)
	do := func(method, path, body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
		return rec.Code
	}
	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/api/session", "", 200},
		{"POST", "/api/room/approve", "", 400},
		{"POST", "/api/room/join", `{"invite":"ABCD-EF23"}`, 400},
		{"POST", "/api/chrome/launch", `{}`, 409},
		{"POST", "/api/room/create", `{"proxy":"127.0.0.1:1"}`, 400},
		{"POST", "/api/leave", "", 200},
		{"POST", "/api/tunnel/connect", "", 404},
	} {
		if got := do(c.method, c.path, c.body); got != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, got, c.want)
		}
	}
}

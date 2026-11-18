//go:build uidev

// UI dev harness: serves two app instances (host on :18081, partner on :18082)
// against an in-process signaling server, so the real frontend can be driven
// from a normal browser.  go test -tags uidev -run UIDev -timeout 0 ./cmd/temproom-app
package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"temproom/internal/app"
	"temproom/internal/server"
)

func TestUIDev(t *testing.T) {
	if os.Getenv("UIDEV_QUICK") != "" {
		t.Setenv("TEMPROOM_SERVER_URL", "")
	} else {
		srv := server.NewServer()
		remote := httptest.NewServer(srv.Routes())
		defer remote.Close()
		t.Setenv("TEMPROOM_SERVER_URL", remote.URL)
	}
	sub, _ := fs.Sub(distFS, "dist")
	files := http.FileServer(http.FS(sub))
	for i, addr := range []string{"127.0.0.1:18081", "127.0.0.1:18082"} {
		// UIDEV_HOST_CONFIG lets the host start with saved settings (e.g. a private relay).
		dir := t.TempDir()
		if v := os.Getenv("UIDEV_HOST_CONFIG"); v != "" && i == 0 {
			dir = v
		}
		t.Setenv("TEMPROOM_CONFIG_DIR", dir)
		api := NewAPIHandler(app.NewService())
		go http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				api.ServeHTTP(w, r)
				return
			}
			files.ServeHTTP(w, r)
		}))
	}
	d := 2 * time.Minute
	if v, err := time.ParseDuration(os.Getenv("UIDEV_FOR")); err == nil {
		d = v
	}
	time.Sleep(d)
}

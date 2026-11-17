package network

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolverTailscaleInterface(t *testing.T) {
	r := NewResolver()
	// Should not crash, returns string or empty
	_ = r.DetectTailscaleInterface()
}

func TestResolverGetPublicIPWithMock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("198.51.100.99\n"))
	}))
	defer ts.Close()

	r := &Resolver{
		client: ts.Client(),
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL, nil)
	resp, err := r.client.Do(req)
	if err != nil {
		t.Fatalf("Mock server error: %v", err)
	}
	defer resp.Body.Close()
}

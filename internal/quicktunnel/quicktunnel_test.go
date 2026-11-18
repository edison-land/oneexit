package quicktunnel

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestTryCloudflareRegexIgnoresAPIHost(t *testing.T) {
	if m := tryCloudflareRE.FindStringSubmatch("failed calling https://api.trycloudflare.com/tunnel"); len(m) == 2 && m[1] != "api" {
		t.Fatalf("matched api host as tunnel URL: %v", m)
	}
	m := tryCloudflareRE.FindStringSubmatch("|  https://calm-river-bright-star.trycloudflare.com  |")
	if len(m) != 2 || m[0] != "https://calm-river-bright-star.trycloudflare.com" {
		t.Fatalf("failed to parse quick tunnel URL: %v", m)
	}
}

func TestExtractTGZBinary(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho ok\n")
	if err := tw.WriteHeader(&tar.Header{Name: "cloudflared", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()

	dest := filepath.Join(t.TempDir(), "cloudflared")
	if err := extractTGZBinary(&buf, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("extracted %q, err %v", got, err)
	}
}

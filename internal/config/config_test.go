package config

import "testing"

func TestNormalizeRelayURL(t *testing.T) {
	ok := map[string]string{
		"my-relay.alice.workers.dev":            "https://my-relay.alice.workers.dev",
		" https://My-Relay.alice.workers.dev/ ": "https://my-relay.alice.workers.dev",
		"https://relay.example.com:8443":        "https://relay.example.com:8443",
		"http://127.0.0.1:8787":                 "http://127.0.0.1:8787",
		"http://localhost:8787/":                "http://localhost:8787",
	}
	for in, want := range ok {
		got, err := NormalizeRelayURL(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "http://relay.example.com", "ftp://x", "https://x.dev/path", "https://u:p@x.dev", "https://x.dev?a=1", "https://"} {
		if got, err := NormalizeRelayURL(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if c := Load(dir); c.RelayMode != RelayPublic {
		t.Fatalf("default = %+v", c)
	}
	if err := Save(dir, Config{RelayMode: RelayCustom, CustomWorkerURL: "https://r.example.workers.dev"}); err != nil {
		t.Fatal(err)
	}
	if c := Load(dir); c.RelayMode != RelayCustom || c.CustomWorkerURL != "https://r.example.workers.dev" {
		t.Fatalf("loaded %+v", c)
	}
	// A custom mode without a usable URL falls back to the public relay.
	if err := Save(dir, Config{RelayMode: RelayCustom, CustomWorkerURL: "http://evil.example"}); err != nil {
		t.Fatal(err)
	}
	if c := Load(dir); c.RelayMode != RelayPublic || c.CustomWorkerURL != "" {
		t.Fatalf("bad url kept: %+v", c)
	}
}

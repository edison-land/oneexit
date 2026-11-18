// Package config holds the few local settings TempRoom keeps on disk:
// which relay new rooms use. Nothing here is ever sent to a server.
package config

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	RelayPublic = "public"
	RelayQuick  = "quick"
	RelayCustom = "custom"
)

type Config struct {
	RelayMode       string `json:"relay_mode"`
	CustomWorkerURL string `json:"custom_worker_url"`
}

// Dir is ~/Library/Application Support/TempRoom on macOS (os.UserConfigDir
// elsewhere). TEMPROOM_CONFIG_DIR overrides it (tests, portable setups).
func Dir() (string, error) {
	if d := os.Getenv("TEMPROOM_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "TempRoom"), nil
}

// Load returns the config saved in dir; a missing or unreadable file means defaults.
func Load(dir string) Config {
	c := Config{RelayMode: RelayPublic}
	if dir == "" {
		return c
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || json.Unmarshal(data, &c) != nil {
		return Config{RelayMode: RelayPublic}
	}
	if c.RelayMode != RelayCustom && c.RelayMode != RelayQuick {
		c.RelayMode = RelayPublic
	}
	if u, err := NormalizeRelayURL(c.CustomWorkerURL); err == nil {
		c.CustomWorkerURL = u
	} else {
		c.CustomWorkerURL = ""
	}
	if c.RelayMode == RelayCustom && c.CustomWorkerURL == "" {
		c.RelayMode = RelayPublic
	}
	return c
}

// Save writes the config into dir atomically, readable only by this user.
func Save(dir string, c Config) error {
	if dir == "" {
		return errors.New("no settings directory")
	}
	p := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// NormalizeRelayURL turns "my-relay.workers.dev" or "https://my-relay.workers.dev/"
// into "https://my-relay.workers.dev". Plain http is allowed only for loopback
// (local `wrangler dev`). Paths, queries and credentials are rejected.
func NormalizeRelayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("enter your Worker URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return "", errors.New("not a valid URL")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("use only the Worker address, e.g. https://your-relay.workers.dev")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return "", errors.New("the Worker URL must start with https://")
		}
	default:
		return "", errors.New("the Worker URL must start with https://")
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

package network

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type NetworkInfo struct {
	PublicIP    string   `json:"publicIp"`
	DNSServers  []string `json:"dnsServers"`
	TailscaleIP string   `json:"tailscaleIp,omitempty"`
	IsConnected bool     `json:"isConnected"`
}

type Resolver struct {
	client *http.Client
}

func NewResolver() *Resolver {
	return &Resolver{
		client: &http.Client{
			Timeout: 4 * time.Second,
		},
	}
}

// GetPublicIP queries public IP services
func (r *Resolver) GetPublicIP(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		// Fallback
		req2, err2 := http.NewRequestWithContext(ctx, http.MethodGet, "https://ifconfig.me/ip", nil)
		if err2 != nil {
			return "", err
		}
		resp2, err2 := r.client.Do(req2)
		if err2 != nil {
			return "", err
		}
		defer resp2.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp2.Body, 128))
		return validIP(string(body))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return "", err
	}
	return validIP(string(body))
}

func validIP(s string) (string, error) {
	s = strings.TrimSpace(s)
	if net.ParseIP(s) == nil {
		return "", errors.New("invalid public IP response")
	}
	return s, nil
}

// DetectTailscaleInterface looks for 100.64.0.0/10 CGNAT Tailscale IP
func (r *Resolver) DetectTailscaleInterface() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil && cgnat.Contains(ip) {
				return ip.String()
			}
		}
	}
	return ""
}

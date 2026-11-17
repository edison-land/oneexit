// Package testutil holds a tiny SOCKS5 server used by tests as a stand-in for
// the host's Clash/mihomo mixed port.
package testutil

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// SOCKS5 is a no-auth CONNECT-only proxy. Close stops it and cuts live connections.
type SOCKS5 struct {
	Addr  string
	ln    net.Listener
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func StartSOCKS5(t testing.TB) *SOCKS5 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &SOCKS5{Addr: ln.Addr().String(), ln: ln, conns: map[net.Conn]struct{}{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	t.Cleanup(s.Close)
	return s
}

func (s *SOCKS5) Close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
}

func (s *SOCKS5) track(c net.Conn) {
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
}

func (s *SOCKS5) handle(c net.Conn) {
	s.track(c)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	h := make([]byte, 2)
	if _, err := io.ReadFull(c, h); err != nil || h[0] != 5 {
		return
	}
	if _, err := io.ReadFull(c, make([]byte, h[1])); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(c, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		_, _ = io.ReadFull(c, b)
		host = net.IP(b).String()
	case 3:
		l := make([]byte, 1)
		_, _ = io.ReadFull(c, l)
		b := make([]byte, l[0])
		_, _ = io.ReadFull(c, b)
		host = string(b)
	case 4:
		b := make([]byte, 16)
		_, _ = io.ReadFull(c, b)
		host = net.IP(b).String()
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(c, pb); err != nil {
		return
	}
	if req[1] != 1 { // only CONNECT
		_, _ = c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	dst, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb)))), 3*time.Second)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	s.track(dst)
	defer dst.Close()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(dst, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, dst); done <- struct{}{} }()
	<-done
}

package egress

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func startTestSOCKS5Server(t *testing.T, supportUDP bool) (string, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}

	addr := l.Addr().String()

	stopCh := make(chan struct{})

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case <-stopCh:
					return
				default:
					return
				}
			}

			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))

				// Handshake
				header := make([]byte, 2)
				if _, err := io.ReadFull(c, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				// 0x05 0x00
				_, _ = c.Write([]byte{0x05, 0x00})

				// Request
				reqHeader := make([]byte, 4)
				if _, err := io.ReadFull(c, reqHeader); err != nil {
					return
				}
				cmd := reqHeader[1]
				atyp := reqHeader[3]

				switch atyp {
				case 0x01: // IPv4
					ip := make([]byte, 4)
					_, _ = io.ReadFull(c, ip)
				case 0x03: // Domain
					dlen := make([]byte, 1)
					_, _ = io.ReadFull(c, dlen)
					dom := make([]byte, int(dlen[0]))
					_, _ = io.ReadFull(c, dom)
				}
				port := make([]byte, 2)
				_, _ = io.ReadFull(c, port)

				if cmd == 0x03 { // UDP ASSOCIATE
					if !supportUDP {
						_, _ = c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
						return
					}
					_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x1F, 0x90})
					// Wait
					b := make([]byte, 1)
					_, _ = c.Read(b)
					return
				}

				if cmd == 0x01 { // CONNECT
					_, _ = c.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
					_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 13\r\n\r\n198.51.100.42\n"))
					return
				}
			}(conn)
		}
	}()

	var once sync.Once
	return addr, func() {
		once.Do(func() {
			close(stopCh)
			_ = l.Close()
		})
	}
}

func TestSOCKS5ProbeSuccess(t *testing.T) {
	addr, cleanup := startTestSOCKS5Server(t, true)
	defer cleanup()

	probe := NewSOCKS5Probe(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res := probe.Probe(ctx, addr)
	if !res.Available {
		t.Fatalf("Expected probe to succeed, got error: %s", res.Error)
	}
	if !res.UDPSupport {
		t.Errorf("Expected UDP associate support to be true")
	}
	if res.LatencyMs < 0 {
		t.Errorf("Expected non-negative latency, got %d", res.LatencyMs)
	}
}

func TestSOCKS5ProbeWithoutUDP(t *testing.T) {
	addr, cleanup := startTestSOCKS5Server(t, false)
	defer cleanup()

	probe := NewSOCKS5Probe(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res := probe.Probe(ctx, addr)
	if !res.Available {
		t.Fatalf("Expected probe to be available")
	}
	if res.UDPSupport {
		t.Errorf("Expected UDPSupport to be false")
	}
}

func TestSOCKS5ProbeUnreachable(t *testing.T) {
	probe := NewSOCKS5Probe(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	res := probe.Probe(ctx, "127.0.0.1:49999")
	if res.Available {
		t.Errorf("Expected unreachable port to be unavailable")
	}
}

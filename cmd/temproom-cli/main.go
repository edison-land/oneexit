package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"temproom/internal/device"
	"temproom/internal/egress"
	"temproom/internal/network"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "probe-socks5":
		runProbeSOCKS5(os.Args[2:])
	case "probe-egress":
		runProbeEgress(os.Args[2:])
	case "mock-socks5":
		runMockSOCKS5(os.Args[2:])
	case "sys-info":
		runSysInfo()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("TempRoom CLI - Diagnostics and M0 Egress Compatibility Probing")
	fmt.Println("")
	fmt.Println("Usage:")
	fmt.Println("  temproom-cli probe-socks5 [-addr 127.0.0.1:7890] [-timeout 3s]")
	fmt.Println("  temproom-cli probe-egress")
	fmt.Println("  temproom-cli mock-socks5 [-port 17890] [-simulate-udp true]")
	fmt.Println("  temproom-cli sys-info")
}

func runProbeSOCKS5(args []string) {
	fs := flag.NewFlagSet("probe-socks5", flag.ExitOnError)
	addr := fs.String("addr", "", "Target SOCKS5 address (leave empty for auto-discovery)")
	timeout := fs.Duration("timeout", 3*time.Second, "Timeout per probe")
	_ = fs.Parse(args)

	p := egress.NewSOCKS5Probe(*timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if *addr != "" {
		fmt.Printf("Probing SOCKS5 target: %s ...\n", *addr)
		res := p.Probe(ctx, *addr)
		printProbeResult(res)
		if !res.Available {
			os.Exit(2)
		}
		return
	}

	fmt.Println("Auto-discovering common local SOCKS5 endpoints (127.0.0.1: 7890, 7891, 2080, 1080...)...")
	results := p.DiscoverLocal(ctx)
	if len(results) == 0 {
		fmt.Println("No active SOCKS5 proxy endpoints detected on localhost.")
		os.Exit(2)
	}

	fmt.Printf("Found %d active SOCKS5 endpoint(s):\n", len(results))
	for _, res := range results {
		printProbeResult(res)
	}
}

func printProbeResult(res egress.ProbeResult) {
	fmt.Printf("\n--- Endpoint: %s ---\n", res.Address)
	fmt.Printf("  TCP Handshake : %v\n", res.Available)
	if res.Available {
		fmt.Printf("  Latency       : %d ms\n", res.LatencyMs)
		fmt.Printf("  UDP Associate : %v\n", res.UDPSupport)
		if res.ExitIP != "" {
			fmt.Printf("  Public Exit IP: %s\n", res.ExitIP)
		} else {
			fmt.Println("  Public Exit IP: (Failed to query via proxy)")
		}
	} else {
		fmt.Printf("  Error         : %s\n", res.Error)
	}
}

func runProbeEgress(args []string) {
	resolver := network.NewResolver()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fmt.Println("Checking current machine egress status...")
	pubIP, err := resolver.GetPublicIP(ctx)
	if err != nil {
		fmt.Printf("Failed to get public IP: %v\n", err)
	} else {
		fmt.Printf("Direct Public IP: %s\n", pubIP)
	}

	tsIP := resolver.DetectTailscaleInterface()
	if tsIP != "" {
		fmt.Printf("Tailscale CGNAT IP: %s\n", tsIP)
	} else {
		fmt.Println("Tailscale interface: Not detected (or no 100.64.0.0/10 address)")
	}
}

func runSysInfo() {
	cam := device.DetectCamera()
	fmt.Println("TempRoom System Diagnostic:")
	fmt.Printf("  Camera Available: %v\n", cam.Available)
	if cam.Name != "" {
		fmt.Printf("  Camera Name     : %s\n", cam.Name)
	}
}

// runMockSOCKS5 runs a standalone, pure Go test SOCKS5 server for M0 test automation
// without needing any third-party VPN/proxy client running.
func runMockSOCKS5(args []string) {
	fs := flag.NewFlagSet("mock-socks5", flag.ExitOnError)
	port := fs.Int("port", 17890, "Port to listen on 127.0.0.1")
	simUDP := fs.Bool("simulate-udp", true, "Support UDP ASSOCIATE command in mock")
	_ = fs.Parse(args)

	listenAddr := fmt.Sprintf("127.0.0.1:%d", *port)
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start mock SOCKS5 server: %v\n", err)
		os.Exit(1)
	}
	defer l.Close()

	fmt.Printf("Mock SOCKS5 server listening on %s (UDP Associate supported: %v)\n", listenAddr, *simUDP)
	fmt.Println("Press Ctrl+C to terminate.")

	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go handleMockClient(conn, *simUDP)
	}
}

func handleMockClient(conn net.Conn, supportUDP bool) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// 1. Handshake: [0x05, NMETHODS, METHODS...]
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	if header[0] != 0x05 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}

	// Respond: 0x05 0x00 (NO AUTH)
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// 2. Request: [VER, CMD, RSV, ATYP, DST.ADDR, DST.PORT]
	reqHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, reqHeader); err != nil {
		return
	}

	cmd := reqHeader[1]
	atyp := reqHeader[3]

	// Read destination address
	var dstAddr string
	switch atyp {
	case 0x01: // IPv4
		ip := make([]byte, 4)
		_, _ = io.ReadFull(conn, ip)
		dstAddr = net.IP(ip).String()
	case 0x03: // Domain
		dlen := make([]byte, 1)
		_, _ = io.ReadFull(conn, dlen)
		dom := make([]byte, int(dlen[0]))
		_, _ = io.ReadFull(conn, dom)
		dstAddr = string(dom)
	case 0x04: // IPv6
		ip := make([]byte, 16)
		_, _ = io.ReadFull(conn, ip)
		dstAddr = net.IP(ip).String()
	}
	portBytes := make([]byte, 2)
	_, _ = io.ReadFull(conn, portBytes)
	dstPort := int(portBytes[0])<<8 | int(portBytes[1])
	target := net.JoinHostPort(dstAddr, strconv.Itoa(dstPort))

	// Command 0x03: UDP ASSOCIATE
	if cmd == 0x03 {
		if !supportUDP {
			// Reply: Command not supported (0x07)
			_, _ = conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		// Return dummy relay address: 127.0.0.1:17891
		reply := []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x45, 0xe3}
		_, _ = conn.Write(reply)
		// Keep TCP connection open to hold UDP association alive
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
		return
	}

	// Command 0x01: CONNECT
	if cmd == 0x01 {
		// Connect to destination
		targetConn, err := net.DialTimeout("tcp", target, 5*time.Second)
		if err != nil {
			_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
			return
		}
		defer targetConn.Close()

		// Success response
		_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x00, 0x00})
		_ = conn.SetDeadline(time.Time{})

		// Proxy data bi-directionally
		done := make(chan struct{}, 2)
		go func() {
			_, _ = io.Copy(targetConn, conn)
			done <- struct{}{}
		}()
		go func() {
			_, _ = io.Copy(conn, targetConn)
			done <- struct{}{}
		}()
		<-done
	}
}

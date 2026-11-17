package app

import (
	"net"
	"time"
)

type netDialer struct{}

func (netDialer) dial(addr string) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, time.Second)
}

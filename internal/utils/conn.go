package utils

import (
	"net"
	"time"
)

// WriteTimeoutConn fails a write that stalls for longer than timeout, so a
// peer that stops reading cannot block its writer for good.
func WriteTimeoutConn(c net.Conn, timeout time.Duration) net.Conn {
	if timeout <= 0 {
		return c
	}
	return &writeTimeoutConn{Conn: c, timeout: timeout}
}

type writeTimeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (c *writeTimeoutConn) Write(b []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}
	return c.Conn.Write(b)
}

// WriteTimeoutListener wraps every accepted connection with WriteTimeoutConn.
func WriteTimeoutListener(l net.Listener, timeout time.Duration) net.Listener {
	if timeout <= 0 {
		return l
	}
	return &writeTimeoutListener{Listener: l, timeout: timeout}
}

type writeTimeoutListener struct {
	net.Listener
	timeout time.Duration
}

func (l *writeTimeoutListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return WriteTimeoutConn(c, l.timeout), nil
}

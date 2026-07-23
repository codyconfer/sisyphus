//go:build !windows

package daemon

import (
	"context"
	"net"
	"os"
)

func Listen(name string) (net.Listener, error) {
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", name)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func dialConn(_ context.Context, name string) (net.Conn, error) {
	return net.Dial("unix", name)
}

func IsListening(name string) bool {
	conn, err := net.Dial("unix", name)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

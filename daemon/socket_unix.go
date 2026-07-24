//go:build !windows

package daemon

import (
	"context"
	"net"
	"os"
)

// DefaultPipePrefix is unused on Unix (name is the socket path) but kept so
// callers can share one Listen/Dial signature across platforms.
const DefaultPipePrefix = "sisyphus"

// Listen opens a Unix domain socket at name. prefix is ignored on Unix.
func Listen(prefix, name string) (net.Listener, error) {
	_ = prefix
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

func dialConn(ctx context.Context, prefix, name string) (net.Conn, error) {
	_ = prefix
	var d net.Dialer
	return d.DialContext(ctx, "unix", name)
}

func IsListening(prefix, name string) bool {
	_ = prefix
	conn, err := net.Dial("unix", name)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

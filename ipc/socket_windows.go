//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/user"

	"github.com/Microsoft/go-winio"
)

func pipeName(prefix, name string) string {
	if prefix == "" {
		// No library-supplied product name: an anonymous but stable fallback.
		prefix = "pipe"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf(`\\.\pipe\%s-%08x`, prefix, h.Sum32())
}

func ownerOnlySDDL() string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)(A;;GA;;;BA)", u.Uid)
	}
	return "D:P(A;;GA;;;OW)(A;;GA;;;SY)(A;;GA;;;BA)"
}

func Listen(prefix, name string) (net.Listener, error) {
	pipe := pipeName(prefix, name)
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: ownerOnlySDDL()})
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s: %w", ErrInUse, pipe, err)
		}
		return nil, err
	}
	return ln, nil
}

func dialConn(ctx context.Context, prefix, name string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(prefix, name))
}

func IsListening(prefix, name string) bool {
	timeout := dialProbeTimeout
	conn, err := winio.DialPipe(pipeName(prefix, name), &timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

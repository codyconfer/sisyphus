//go:build windows

package daemon

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"os/user"
	"time"

	"github.com/Microsoft/go-winio"
)

const dialProbeTimeout = 200 * time.Millisecond

func pipeName(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf(`\\.\pipe\munin-%08x`, h.Sum32())
}

func ownerOnlySDDL() string {
	if u, err := user.Current(); err == nil && u.Uid != "" {
		return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)(A;;GA;;;BA)", u.Uid)
	}
	return "D:P(A;;GA;;;OW)(A;;GA;;;SY)(A;;GA;;;BA)"
}

func Listen(name string) (net.Listener, error) {
	return winio.ListenPipe(pipeName(name), &winio.PipeConfig{SecurityDescriptor: ownerOnlySDDL()})
}

func dialConn(ctx context.Context, name string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName(name))
}

func IsListening(name string) bool {
	timeout := dialProbeTimeout
	conn, err := winio.DialPipe(pipeName(name), &timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

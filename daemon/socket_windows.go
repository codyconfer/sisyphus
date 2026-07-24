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

// DefaultPipePrefix is used when Listen/Dial/IsListening are called with an
// empty prefix. Apps should pass their own prefix (e.g. "munin") so pipe names
// stay stable and this library carries no app literals.
const DefaultPipePrefix = "sisyphus"

func pipeName(prefix, name string) string {
	if prefix == "" {
		prefix = DefaultPipePrefix
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

// Listen opens a Windows named pipe derived from prefix+name.
func Listen(prefix, name string) (net.Listener, error) {
	return winio.ListenPipe(pipeName(prefix, name), &winio.PipeConfig{SecurityDescriptor: ownerOnlySDDL()})
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

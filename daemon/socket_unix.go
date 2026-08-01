//go:build !windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const dialProbeTimeout = 200 * time.Millisecond

const (
	lockAcquireTimeout = 2 * time.Second
	lockRetryInterval  = 50 * time.Millisecond
)

// Listen binds the service endpoint. On Unix systems name is a filesystem
// path for a Unix socket and prefix is ignored; on Windows the pair names a
// named pipe. The socket is owner-only (0600). A stale socket file left by a
// dead process is detected by probing and replaced, while a live listener —
// or a non-socket file at name — makes Listen fail, the former wrapped in
// ErrInUse. Closing the listener removes the socket file only if it is still
// the one this listener bound.
func Listen(prefix, name string) (net.Listener, error) {
	_ = prefix
	lock, err := lockSocketPath(name)
	if err != nil {
		return nil, err
	}
	defer unlockSocketPath(lock)
	info, err := os.Lstat(name)
	switch {
	case err != nil && !os.IsNotExist(err):
		return nil, err
	case err == nil && info.Mode()&os.ModeSocket == 0:
		return nil, fmt.Errorf("refusing to replace non-socket path %s", name)
	case err == nil:
		if err := clearDeadSocket(name); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", name)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("%w: %s: %w", ErrInUse, name, err)
		}
		return nil, err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	ul, ok := ln.(*net.UnixListener)
	if !ok {
		return ln, nil
	}
	guard, err := guardUnlink(ul, name)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	return guard, nil
}

func lockSocketPath(name string) (*os.File, error) {
	f, err := os.OpenFile(name+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot lock socket path %s: %w", name, err)
	}
	deadline := time.Now().Add(lockAcquireTimeout)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EWOULDBLOCK) {
			if time.Now().Before(deadline) {
				time.Sleep(lockRetryInterval)
				continue
			}
			_ = f.Close()
			return nil, fmt.Errorf("%w: %s: socket path lock still held after %s", ErrInUse, name, lockAcquireTimeout)
		}
		_ = f.Close()
		return nil, fmt.Errorf("cannot lock socket path %s: %w", name, err)
	}
}

func unlockSocketPath(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	_ = f.Close()
}

func clearDeadSocket(name string) error {
	live, err := probeSocket(name)
	switch {
	case err != nil:
		return fmt.Errorf("cannot tell whether %s is in use: %w", name, err)
	case live:
		return fmt.Errorf("%w: %s", ErrInUse, name)
	}
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func probeSocket(name string) (listening bool, undecided error) {
	conn, err := net.DialTimeout("unix", name, dialProbeTimeout)
	if err == nil {
		_ = conn.Close()
		return true, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
		return false, nil
	}
	if os.IsTimeout(err) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	return false, err
}

type unlinkGuard struct {
	*net.UnixListener
	path  string
	bound os.FileInfo
	once  sync.Once
}

func guardUnlink(ul *net.UnixListener, name string) (net.Listener, error) {
	bound, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	ul.SetUnlinkOnClose(false)
	return &unlinkGuard{UnixListener: ul, path: name, bound: bound}, nil
}

func (g *unlinkGuard) Close() error {
	err := g.UnixListener.Close()
	g.once.Do(g.unlinkIfStillOwned)
	return err
}

func (g *unlinkGuard) unlinkIfStillOwned() {
	at, err := os.Lstat(g.path)
	if err != nil || !os.SameFile(at, g.bound) {
		return
	}
	_ = os.Remove(g.path)
}

func dialConn(ctx context.Context, prefix, name string) (net.Conn, error) {
	_ = prefix
	var d net.Dialer
	return d.DialContext(ctx, "unix", name)
}

// IsListening reports whether something accepts connections at the service
// endpoint, by dialing it. It is a raw probe that ignores build capability:
// prefer Attached, which returns false outright in nodaemon builds and only
// then delegates here, and reach for IsListening directly only when you want
// the probe regardless of build.
func IsListening(prefix, name string) bool {
	_ = prefix
	conn, err := net.Dial("unix", name)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

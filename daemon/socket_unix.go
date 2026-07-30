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
)

const DefaultPipePrefix = "sisyphus"

const dialProbeTimeout = 200 * time.Millisecond

var ErrInUse = errors.New("address already in use")

func Listen(prefix, name string) (net.Listener, error) {
	_ = prefix
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

func IsListening(prefix, name string) bool {
	_ = prefix
	conn, err := net.Dial("unix", name)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

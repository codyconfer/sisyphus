//go:build !windows

package ipc

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestListenRefusesToReplaceRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if ln, err := Listen("test", path); err == nil {
		_ = ln.Close()
		t.Fatal("Listen should reject a regular file")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("regular file changed: %q", got)
	}
}

func TestListenRefusesLiveSocket(t *testing.T) {
	path := shortSocketPath(t)
	first, err := Listen("test", path)
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	defer first.Close()

	second, err := Listen("test", path)
	if err == nil {
		_ = second.Close()
		t.Fatal("Listen took over a socket a live listener is bound to")
	}
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("Listen error = %v, want one matching ErrInUse", err)
	}
	if !IsListening("test", path) {
		t.Fatal("the live listener's socket file is gone")
	}
}

func TestListenReclaimsStaleSocketFile(t *testing.T) {
	path := shortSocketPath(t)
	staleSocketFile(t, path)

	ln, err := Listen("test", path)
	if err != nil {
		t.Fatalf("Listen over a stale socket file: %v", err)
	}
	defer ln.Close()
	if !IsListening("test", path) {
		t.Fatal("reclaimed socket is not accepting connections")
	}
}

func TestCloseKeepsSocketOwnedByAnotherListener(t *testing.T) {
	path := shortSocketPath(t)
	displaced, err := Listen("test", path)
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove socket behind the first listener: %v", err)
	}
	winner, err := Listen("test", path)
	if err != nil {
		t.Fatalf("second Listen after the path was freed: %v", err)
	}
	defer winner.Close()

	if err := displaced.Close(); err != nil {
		t.Fatalf("close displaced listener: %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("displaced listener deleted the live socket file: %v", err)
	}
	if !IsListening("test", path) {
		t.Fatal("displaced listener's Close broke the live listener's socket")
	}
}

func TestListenRefusesSocketHeldByAnotherProcess(t *testing.T) {
	if path := os.Getenv("SISYPHUS_TEST_HOLD_SOCKET"); path != "" {
		holdSocket(t, path)
		return
	}
	path := shortSocketPath(t)

	self, err := os.Executable()
	if err != nil {
		t.Skipf("cannot locate test binary: %v", err)
	}
	child := exec.Command(self, "-test.run", "TestListenRefusesSocketHeldByAnotherProcess")
	child.Env = append(os.Environ(), "SISYPHUS_TEST_HOLD_SOCKET="+path)
	child.Stdout, child.Stderr = os.Stderr, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	killed := false
	t.Cleanup(func() {
		if !killed {
			_ = child.Process.Kill()
			_, _ = child.Process.Wait()
		}
	})
	waitForListener(t, path)

	if ln, err := Listen("test", path); err == nil {
		_ = ln.Close()
		t.Fatal("Listen stole a socket another process is serving")
	} else if !errors.Is(err, ErrInUse) {
		t.Fatalf("Listen error = %v, want one matching ErrInUse", err)
	}
	if !IsListening("test", path) {
		t.Fatal("the other process's socket file is gone")
	}

	if err := child.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	_, _ = child.Process.Wait()
	killed = true

	if _, err := os.Lstat(path); err != nil {
		t.Skipf("killed child left no socket file to reclaim: %v", err)
	}
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatalf("Listen over the dead process's socket: %v", err)
	}
	defer ln.Close()
}

func holdSocket(t *testing.T, path string) {
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatalf("child Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	time.Sleep(30 * time.Second)
}

func waitForListener(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if IsListening("test", path) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a listener at %s", path)
}

func TestListenFailsWithinBoundWhenLockHeld(t *testing.T) {
	path := shortSocketPath(t)
	holder, err := lockSocketPath(path)
	if err != nil {
		t.Fatalf("holding the socket path lock: %v", err)
	}
	defer unlockSocketPath(holder)

	start := time.Now()
	ln, err := Listen("test", path)
	elapsed := time.Since(start)
	if err == nil {
		_ = ln.Close()
		t.Fatal("Listen succeeded while another holder had the socket path lock")
	}
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("Listen error = %v, want one matching ErrInUse", err)
	}
	if elapsed > lockAcquireTimeout+5*time.Second {
		t.Fatalf("Listen took %s to fail; a held lock must fail within the acquisition bound", elapsed)
	}
}

func TestListenSerializesConcurrentStaleReclaim(t *testing.T) {
	path := shortSocketPath(t)
	staleSocketFile(t, path)

	type outcome struct {
		ln  net.Listener
		err error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			ln, err := Listen("test", path)
			results <- outcome{ln: ln, err: err}
		}()
	}
	close(start)

	var winners []net.Listener
	var losers []error
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			losers = append(losers, r.err)
			continue
		}
		winners = append(winners, r.ln)
	}
	if len(winners) != 1 {
		for _, ln := range winners {
			_ = ln.Close()
		}
		t.Fatalf("want exactly one winner, got %d (loser errors: %v)", len(winners), losers)
	}
	defer winners[0].Close()
	if !errors.Is(losers[0], ErrInUse) {
		t.Fatalf("loser error = %v, want one matching ErrInUse", losers[0])
	}
	if !IsListening("test", path) {
		t.Fatal("winner's socket is not connectable")
	}
}

func staleSocketFile(t *testing.T, path string) {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		_ = syscall.Close(fd)
		t.Fatalf("bind %s: %v", path, err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatalf("close: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("stale path %s is not a socket", path)
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = conn.Close()
		t.Fatalf("stale socket %s still answers", path)
	}
}

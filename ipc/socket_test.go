package ipc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/codyconfer/sisyphus/stream"
)

func encodeInt(v int) ([]byte, error) { return []byte(strconv.Itoa(v)), nil }
func decodeInt(b []byte) (int, error) { return strconv.Atoi(string(b)) }

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sisyphus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove socket temp directory: %v", err)
		}
	})
	return filepath.Join(dir, "s.sock")
}

func TestSocketBroadcastToMultipleClients(t *testing.T) {
	path := shortSocketPath(t)
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	subj := stream.NewSubject[int]()
	defer subj.Close()
	go Broadcast(t.Context(), ln, subj, 8, encodeInt)

	a, err := Dial(t.Context(), "test", path, decodeInt)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Dial(t.Context(), "test", path, decodeInt)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	subj.Next(1)
	subj.Next(2)

	for _, ch := range []<-chan int{a, b} {
		for want := 1; want <= 2; want++ {
			select {
			case got := <-ch:
				if got != want {
					t.Fatalf("want %d, got %d", want, got)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("client did not receive %d", want)
			}
		}
	}
}

func TestListenClearsStaleSocket(t *testing.T) {
	path := shortSocketPath(t)
	ln1, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	ln1.Close()

	ln2, err := Listen("test", path)
	if err != nil {
		t.Fatalf("re-listen over stale socket failed: %v", err)
	}
	ln2.Close()
}

func TestIsListening(t *testing.T) {
	path := shortSocketPath(t)
	if IsListening("test", path) {
		t.Fatal("nothing should be listening yet")
	}
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if !IsListening("test", path) {
		t.Fatal("should detect the live listener")
	}
}

func TestDialSurfacesOversizedFrame(t *testing.T) {
	path := shortSocketPath(t)
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, _ = conn.Write(bytes.Repeat([]byte("x"), maxDialFrame+1024))
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reason := make(chan error, 1)
	out, err := Dial(ctx, "test", path, decodeInt, WithDialClose(func(err error) { reason <- err }))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case v, ok := <-out:
		if ok {
			t.Fatalf("decoded %d from an oversized frame", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close on an oversized frame")
	}
	select {
	case got := <-reason:
		if !errors.Is(got, bufio.ErrTooLong) {
			t.Fatalf("close reason = %v, want %v", got, bufio.ErrTooLong)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no close reason reported: reader errors are indistinguishable from a clean close")
	}
}

func TestDialReportsCleanClose(t *testing.T) {
	path := shortSocketPath(t)
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, _ = conn.Write([]byte("7\n"))
		conn.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reason := make(chan error, 1)
	out, err := Dial(ctx, "test", path, decodeInt, WithDialClose(func(err error) { reason <- err }))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case v := <-out:
		if v != 7 {
			t.Fatalf("got %d, want 7", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no value received")
	}
	select {
	case got := <-reason:
		if got != nil {
			t.Fatalf("close reason = %v, want nil for a clean end of stream", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no close reason reported")
	}
}

func TestDialReportsContextCancel(t *testing.T) {
	path := shortSocketPath(t)
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	held := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		held <- conn
	}()

	ctx, cancel := context.WithCancel(context.Background())
	reason := make(chan error, 1)
	out, err := Dial(ctx, "test", path, decodeInt, WithDialClose(func(err error) { reason <- err }))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	select {
	case conn := <-held:
		defer conn.Close()
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("server never accepted")
	}
	cancel()

	select {
	case got := <-reason:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("close reason = %v, want %v", got, context.Canceled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no close reason reported on cancel")
	}
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("stream stayed open after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close after cancel")
	}
}

func TestBroadcastFreesSlotAfterPeerCloses(t *testing.T) {
	path := shortSocketPath(t)
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	subj := stream.NewSubject[int]()
	defer subj.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Broadcast(ctx, ln, subj, 8, encodeInt)

	conns := make([]net.Conn, 0, maxBroadcastConns)
	for i := 0; i < maxBroadcastConns; i++ {
		c, err := dialConn(ctx, "test", path)
		if err != nil {
			t.Fatalf("occupying slot %d: %v", i, err)
		}
		defer c.Close()
		conns = append(conns, c)
	}

	stop := publishUntilStopped(subj, 1)
	buf := make([]byte, 8)
	for i, c := range conns {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, err := c.Read(buf); err != nil {
			stop()
			t.Fatalf("peer %d never received a publish, so the slots were never taken: %v", i, err)
		}
	}
	stop()

	for _, c := range conns {
		c.Close()
	}
	time.Sleep(300 * time.Millisecond)

	events, err := Dial(ctx, "test", path, decodeInt)
	if err != nil {
		t.Fatal(err)
	}
	stop = publishUntilStopped(subj, 2)
	defer stop()
	select {
	case v, ok := <-events:
		if !ok {
			t.Fatal("new peer was rejected: departed peers still hold their broadcast slots")
		}
		if v != 2 {
			t.Fatalf("got %d, want 2", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new peer received nothing: departed peers still hold their broadcast slots")
	}
}

func publishUntilStopped(subj *stream.Subject[int], v int) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		for {
			subj.Next(v)
			select {
			case <-stop:
				return
			case <-tk.C:
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

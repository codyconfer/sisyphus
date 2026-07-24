package daemon

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func encodeInt(v int) ([]byte, error) { return []byte(strconv.Itoa(v)), nil }
func decodeInt(b []byte) (int, error) { return strconv.Atoi(string(b)) }

func TestSocketBroadcastToMultipleClients(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	subj := NewSubject[int]()
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
	path := filepath.Join(t.TempDir(), "s.sock")
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
	path := filepath.Join(t.TempDir(), "s.sock")
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

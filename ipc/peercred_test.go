package ipc

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/codyconfer/sisyphus/stream"
)

func TestAllowPeerAcceptsSameUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	subj := stream.NewSubject[int]()
	defer subj.Close()
	go Broadcast(t.Context(), ln, subj, 8, encodeInt)

	ch, err := Dial(t.Context(), "test", path, decodeInt)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	subj.Next(42)

	select {
	case got := <-ch:
		if got != 42 {
			t.Fatalf("want 42, got %d", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("same-UID client was not accepted (no event received)")
	}
}

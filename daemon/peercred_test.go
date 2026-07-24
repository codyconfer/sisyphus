package daemon

import (
	"path/filepath"
	"testing"
	"time"
)

// A same-UID client must be accepted by allowPeer on every platform that has a
// peer-credential check (linux via SO_PEERCRED, darwin/freebsd via LOCAL_PEERCRED).
// Cross-UID rejection cannot be exercised in a unit test without a second user, so
// this guards only that a legitimate same-UID dial is not wrongly refused.
func TestAllowPeerAcceptsSameUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	ln, err := Listen("test", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	subj := NewSubject[int]()
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

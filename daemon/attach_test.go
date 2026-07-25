package daemon

import (
	"testing"

	"github.com/codyconfer/sisyphus/mode"
)

func TestAttachedRespectsDaemonSupported(t *testing.T) {
	path := shortSocketPath(t)
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
		t.Fatal("the raw probe should see the listener")
	}
	got := Attached("test", path)
	if mode.DaemonSupported {
		if !got {
			t.Fatal("default build should detect the live listener")
		}
		return
	}
	if got {
		t.Fatal("a nodaemon build must report detached even with a live socket")
	}
}

func TestAttachedFalseWhenNothingListening(t *testing.T) {
	if !mode.DaemonSupported {
		t.Skip("Attached is always false under nodaemon")
	}
	path := shortSocketPath(t)
	if Attached("test", path) {
		t.Fatal("nothing should be attached yet")
	}
}

package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunToolNotInstalled(t *testing.T) {
	_, err := RunTool(context.Background(), []string{"sisyphus-auth-tool-missing-xyz"}, "demo")
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

func TestRunToolHappy(t *testing.T) {
	dir := t.TempDir()
	name := "oktool"
	if runtime.GOOS == "windows" {
		name += ".bat"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("@echo hello\r\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := RunTool(context.Background(), []string{name}, "ok")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello\n" && string(out) != "hello\r\n" {
		t.Fatalf("out = %q", out)
	}
}

package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrNotInstalled is returned when none of the candidate binaries are on PATH.
var ErrNotInstalled = errors.New("tool not installed")

// RunTool looks up the first available binary in bins and runs it with args.
// On missing binary it returns ErrNotInstalled wrapped with name.
func RunTool(ctx context.Context, bins []string, name string, args ...string) ([]byte, error) {
	bin := ""
	for _, b := range bins {
		if _, err := exec.LookPath(b); err == nil {
			bin = b
			break
		}
	}
	if bin == "" {
		return nil, fmt.Errorf("%s: %w", name, ErrNotInstalled)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), msg, err)
	}
	return stdout.Bytes(), nil
}

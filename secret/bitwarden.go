package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type bwStore struct{}

func (bwStore) Name() string { return "bitwarden" }

func bwConfigured(ctx context.Context) bool {
	if _, err := exec.LookPath("bw"); err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, "bw", "status")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var st struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(out, &st) != nil {
		return false
	}
	return st.Status == "unlocked"
}

func (bwStore) Get(ctx context.Context, key string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "bw", "get", "notes", key)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(errb.String()), "not found") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("bw get %q: %v: %s", key, err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), true, nil
}

func (bwStore) Set(ctx context.Context, key, value string) error {
	item := map[string]any{
		"type":  2,
		"name":  key,
		"notes": value,
		"secureNote": map[string]any{
			"type": 0,
		},
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	encoded, err := pipe(ctx, "bw", []string{"encode"}, raw)
	if err != nil {
		return err
	}
	_, err = pipe(ctx, "bw", []string{"create", "item"}, encoded)
	return err
}

func pipe(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

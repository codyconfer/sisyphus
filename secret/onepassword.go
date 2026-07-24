package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type opStore struct{}

func (opStore) Name() string { return "1password" }

func opConfigured(ctx context.Context) bool {
	if _, err := exec.LookPath("op"); err != nil {
		return false
	}
	if os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "" {
		return true
	}
	return exec.CommandContext(ctx, "op", "whoami").Run() == nil
}

func (opStore) Get(ctx context.Context, key string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "op", "item", "get", key, "--fields", "password", "--reveal")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if opNotFound(errb.String()) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("op item get %q: %v: %s", key, err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), true, nil
}

func opNotFound(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "isn't an item") ||
		strings.Contains(s, "not found") ||
		strings.Contains(s, "no item") ||
		strings.Contains(s, "doesn't exist")
}

func (opStore) Set(ctx context.Context, key, value string) error {
	tmpl, err := opTemplate(key, value)
	if err != nil {
		return err
	}
	_, err = pipe(ctx, "op", opCreateArgs(), tmpl)
	return err
}

func opCreateArgs() []string {
	return []string{"item", "create", "--template", "-"}
}

func opTemplate(key, value string) ([]byte, error) {
	item := map[string]any{
		"title":    key,
		"category": "PASSWORD",
		"fields": []map[string]any{
			{
				"id":    "password",
				"type":  "CONCEALED",
				"value": value,
			},
		},
	}
	return json.Marshal(item)
}

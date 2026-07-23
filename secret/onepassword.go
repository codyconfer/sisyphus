package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type opStore struct{}

func (opStore) Name() string { return "1password" }

func opConfigured() bool {
	if _, err := exec.LookPath("op"); err != nil {
		return false
	}
	if os.Getenv("OP_SERVICE_ACCOUNT_TOKEN") != "" {
		return true
	}
	return exec.Command("op", "whoami").Run() == nil
}

func (opStore) Get(key string) (string, bool, error) {
	cmd := exec.Command("op", "item", "get", key, "--fields", "password", "--reveal")
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

func (opStore) Set(key, value string) error {
	tmpl, err := opTemplate(key, value)
	if err != nil {
		return err
	}
	_, err = pipe("op", opCreateArgs(), tmpl)
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

package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type bwStore struct{}

func (bwStore) Name() string { return "bitwarden" }

func bwConfigured() bool {
	if _, err := exec.LookPath("bw"); err != nil {
		return false
	}
	out, err := exec.Command("bw", "status").Output()
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

func (bwStore) Get(key string) (string, bool, error) {
	cmd := exec.Command("bw", "get", "notes", key)
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

func (bwStore) Set(key, value string) error {
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
	encoded, err := pipe("bw", []string{"encode"}, raw)
	if err != nil {
		return err
	}
	_, err = pipe("bw", []string{"create", "item"}, encoded)
	return err
}

func pipe(name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

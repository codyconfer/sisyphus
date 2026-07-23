package redact

import (
	"strings"
	"testing"
)

func TestConfigYAMLNested(t *testing.T) {
	in := []byte(strings.Join([]string{
		"google:",
		"  oauth_client_id: Iv1.public",
		"  oauth_client_secret: super-secret-value",
		"output: terminal",
	}, "\n"))
	got := Config(in, "yaml")

	if strings.Contains(got, "super-secret-value") {
		t.Error("nested oauth_client_secret was not masked")
	}
	if !strings.Contains(got, Mask) {
		t.Errorf("expected mask marker in output:\n%s", got)
	}
	if !strings.Contains(got, "Iv1.public") {
		t.Error("non-secret oauth_client_id should survive")
	}
	if !strings.Contains(got, "terminal") {
		t.Error("non-secret output value should survive")
	}
}

func TestConfigJSON(t *testing.T) {
	in := []byte(`{"github":{"api_key":"ghp_leak","api_url":"https://api.github.com"}}`)
	got := Config(in, "json")

	if strings.Contains(got, "ghp_leak") {
		t.Error("api_key was not masked")
	}
	if !strings.Contains(got, "https://api.github.com") {
		t.Error("non-secret api_url should survive")
	}
	if !strings.Contains(got, Mask) {
		t.Errorf("expected mask marker in output:\n%s", got)
	}
}

func TestConfigMultiLineValue(t *testing.T) {
	in := []byte("private_key: |\n  line-one\n  line-two\nname: bob\n")
	got := Config(in, "yaml")

	if strings.Contains(got, "line-one") || strings.Contains(got, "line-two") {
		t.Error("multi-line private_key was not masked")
	}
	if !strings.Contains(got, "bob") {
		t.Error("non-secret name should survive")
	}
}

func TestConfigNonSecretUnchanged(t *testing.T) {
	in := []byte("output: terminal\ntoken_env: SLACK_TOKEN\n")
	got := Config(in, "yaml")

	if strings.Contains(got, Mask) {
		t.Errorf("no secret keys present; nothing should be masked:\n%s", got)
	}
	if !strings.Contains(got, "SLACK_TOKEN") {
		t.Error("token_env value should survive")
	}
}

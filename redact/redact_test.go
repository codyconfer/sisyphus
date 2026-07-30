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

func TestKeySelectorsAreNotSecrets(t *testing.T) {
	notSecret := []string{
		"secret_backend", "secret_name", "SECRET_BACKEND", "Secret_Name",
		"  secret_name  ", "backup.secret_backend", "token_env", "oauth_client_id",
		"id", "name", "credential_name", "session_name", "output",
	}
	for _, k := range notSecret {
		if Key(k) {
			t.Errorf("Key(%q) = true, want false: selector keys name a backend or a key, they are not secrets", k)
		}
	}

	stillSecret := []string{
		"oauth_client_secret", "client_secret", "secret", "password", "passwd",
		"passphrase", "pwd", "token", "access_token", "refresh_token", "apikey",
		"api_key", "accesskey", "access_key", "private_key", "privatekey",
		"credential", "bearer", "cookie", "session", "signature", "salt", "otp",
		"passcode", "secret_value", "SECRET",
	}
	for _, k := range stillSecret {
		if !Key(k) {
			t.Errorf("Key(%q) = false, want true: this must stay masked", k)
		}
	}
}

func TestConfigKeepsBackupSelectorsUsable(t *testing.T) {
	in := []byte(strings.Join([]string{
		"backup:",
		"  secret_backend: keyring",
		"  secret_name: munin-backup-key",
		"google:",
		"  oauth_client_secret: super-secret-value",
		"",
	}, "\n"))
	got := Config(in, "yaml")

	if !strings.Contains(got, "keyring") {
		t.Errorf("secret_backend value was masked; a redacted config would break backups:\n%s", got)
	}
	if !strings.Contains(got, "munin-backup-key") {
		t.Errorf("secret_name value was masked; a redacted config would rotate the backup key:\n%s", got)
	}
	if strings.Contains(got, "super-secret-value") {
		t.Errorf("oauth_client_secret must still be masked:\n%s", got)
	}
	if !strings.Contains(got, Mask) {
		t.Errorf("expected mask marker in output:\n%s", got)
	}
}

func TestLineKeepsBackupSelectorsUsable(t *testing.T) {
	in := strings.Join([]string{
		"secret_backend: keyring",
		"secret_name: munin-backup-key",
		"oauth_client_secret: super-secret-value",
	}, "\n")
	got := Line(in)

	if !strings.Contains(got, "keyring") || !strings.Contains(got, "munin-backup-key") {
		t.Errorf("selector values must survive line redaction:\n%s", got)
	}
	if strings.Contains(got, "super-secret-value") {
		t.Errorf("oauth_client_secret must still be masked:\n%s", got)
	}
}

func TestKeyHyphenatedAndWebhookSpellings(t *testing.T) {
	secretKeys := []string{
		"api-key", "API-KEY", "x-api-key", "access-key", "private-key",
		"client-secret", "auth-token", "access-token", "pass-phrase",
		"webhook", "webhook_url", "webhook-url", "slack_webhook",
		"incoming-webhook-url", "authorization", "Authorization",
		"github.webhook_url", "  api-key  ",
	}
	for _, k := range secretKeys {
		if !Key(k) {
			t.Errorf("Key(%q) = false, want true: hyphenated and webhook keys carry secrets", k)
		}
	}

	notSecret := []string{
		"secret_backend", "secret-backend", "secret_name", "secret-name",
		"token_env", "token-env", "webhook_name", "webhook_id",
		"oauth_client_id", "oauth-client-id", "id", "name", "output",
		"api-url", "keybinds", "keyring", "theme",
	}
	for _, k := range notSecret {
		if Key(k) {
			t.Errorf("Key(%q) = true, want false: selector and plain-config keys must stay readable", k)
		}
	}

	if !Key("oauth_client_secret") || !Key("oauth-client-secret") {
		t.Error("oauth_client_secret must stay masked in both spellings")
	}
}

func TestConfigMasksHyphenatedAndWebhookKeys(t *testing.T) {
	in := []byte(strings.Join([]string{
		"github:",
		"  api-key: DASHKEY",
		"  api_url: https://api.github.com",
		"slack:",
		"  webhook_url: https://hooks.slack.com/services/T/B/XYZ",
		"  webhook-url: https://hooks.slack.com/services/T/B/ABC",
		"backup:",
		"  secret_backend: keyring",
		"  secret_name: munin-backup-key",
		"google:",
		"  oauth_client_secret: super-secret-value",
		"",
	}, "\n"))
	got := Config(in, "yaml")

	for _, leak := range []string{"DASHKEY", "T/B/XYZ", "T/B/ABC", "super-secret-value"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked through redaction:\n%s", leak, got)
		}
	}
	for _, keep := range []string{"https://api.github.com", "keyring", "munin-backup-key"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q must survive redaction:\n%s", keep, got)
		}
	}
}

func TestLineMasksHyphenatedAndWebhookKeys(t *testing.T) {
	in := strings.Join([]string{
		"api-key: DASHKEY",
		"webhook_url: https://hooks.slack.com/services/T/B/XYZ",
		"secret_backend: keyring",
		"secret_name: munin-backup-key",
		"oauth_client_secret: super-secret-value",
	}, "\n")
	got := Line(in)

	for _, leak := range []string{"DASHKEY", "T/B/XYZ", "super-secret-value"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked through line redaction:\n%s", leak, got)
		}
	}
	if !strings.Contains(got, "keyring") || !strings.Contains(got, "munin-backup-key") {
		t.Errorf("selector values must survive line redaction:\n%s", got)
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

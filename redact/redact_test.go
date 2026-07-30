package redact

import (
	"strings"
	"testing"
)

var mustMask = []string{
	"secret", "SECRET", "Secret", "  secret  ",
	"secret_value", "client_secret", "client-secret", "clientSecret",
	"oauth_client_secret", "oauth-client-secret", "oauthClientSecret",
	"password", "PASSWORD", "passwords", "passwd", "pwd", "pwds",
	"passphrase", "pass-phrase", "pass_phrase", "passcode",
	"token", "access_token", "refresh_token", "auth-token", "accessToken",
	"api_key", "api-key", "apikey", "API-KEY", "x-api-key", "apiKey",
	"access_key", "access-key", "accesskey", "accessKey",
	"private_key", "private-key", "privatekey", "privateKey",
	"key", "keys",
	"credential", "credentials", "bearer", "cookie", "session",
	"signature", "salt", "salts", "otp", "otps",
	"webhook", "webhook_url", "webhook-url", "webhookUrl",
	"slack_webhook", "slack-webhook", "incoming-webhook-url",
	"github.webhook_url", "web_hook_url",
	"authorization", "Authorization", "authorisation",

	"sessionid", "sessionId", "session_id", "session-id",
	"secretid", "secretId",
	"tokenid", "tokenId",
	"cookieid", "cookieId", "cookie_id", "cookie-id",
	"credentialid", "credentialId", "credential_id", "credential-id",
	"secretenv", "secretEnv",
	"passwordenv", "passwordEnv", "password_env", "password-env",
	"secretname", "secretName",
	"tokenname", "tokenName",
	"passwordname", "passwordName", "password_name", "password-name",
	"secret_key_name", "secret-key-name", "secretKeyName",
	"credential_name", "session_name", "webhook_name", "webhook_id",
	"secretbackend", "secretBackend",
}

var mustNotMask = []string{
	"secret_backend", "secret-backend", "SECRET_BACKEND", "  secret_name  ",
	"secret_name", "Secret_Name", "secret-name", "secret_service", "secret-service",
	"backup.secret_backend", "backup.secret_name", "backup.secret_service",
	"secrets_backend", "secrets_name",
	"token_env", "token-env", "app_token_env", "bot_token_env", "tokens_env",
	"secret_env", "secret_id", "token_id", "token_name",
	"oauth_client_id", "oauth-client-id", "oauthClientId", "id", "name",
	"keybinds", "keyring", "keyboard", "low_keyed", "mon_keyboard",
	"oauth_scopes", "user_scopes", "auth_url", "api_url", "api-url",
	"output", "theme", "query", "queries", "signals", "timeout", "desktop",

	"root_path", "snapshot_path", "logs_alt", "bot_ping", "hot_patch",
	"pilot_period", "robot_pool", "tools_altitude", "details_alternate",
	"top_wd", "dump_wd", "bot_pid", "hot_pink",
}

func TestKeyMasksEverySecretSpelling(t *testing.T) {
	for _, k := range mustMask {
		if !Key(k) {
			t.Errorf("Key(%q) = false, want true: this key carries a credential and must be masked", k)
		}
	}
}

func TestKeyLeavesEveryNonSecretReadable(t *testing.T) {
	for _, k := range mustNotMask {
		if Key(k) {
			t.Errorf("Key(%q) = true, want false: masking a non-secret corrupts an exported config", k)
		}
	}
}

func TestKeyTablesAreDisjoint(t *testing.T) {
	seen := make(map[string]string, len(mustMask)+len(mustNotMask))
	for _, k := range mustMask {
		seen[strings.ToLower(k)] = "mask"
	}
	for _, k := range mustNotMask {
		if side, dup := seen[strings.ToLower(k)]; dup && side == "mask" {
			t.Errorf("%q appears on both sides of the table; the expectation is ambiguous", k)
		}
	}
}

var muninKoanfTags = map[string]bool{
	"output": false, "timeout": false, "role": false, "keybinds": false,
	"audit": false, "backup": false, "github": false, "google": false,
	"calendar": false, "gmail": false, "docs": false, "drive": false,
	"tasks": false, "slack": false, "daemon": false, "cache": false,
	"ttl": false, "detail_ttl": false, "signals": false,
	"interval": false, "bell": false, "desktop": false, "tray": false,
	"theme": false, "dir": false, "folders": false, "recent": false,
	"list": false, "lists": false, "show_completed": false, "max": false,
	"oauth_client_id": false, "oauth_client_secret": true,
	"enabled": false, "path": false,
	"secret_backend": false, "secret_name": false,
	"destination": false, "keep": false,
	"queries": false, "oauth_scopes": false, "api_url": false,
	"calendar_id": false, "window": false, "query": false,
	"token_env": false, "app_token_env": false, "bot_token_env": false,
	"user_scopes": false, "limit": false,
}

var muninDirectiveTags = []string{
	"name", "type", "home", "flights", "queries", "formatters", "contexts",
	"hooks", "status", "enter", "exit", "bash", "powershell", "glyph",
	"title", "template", "formatter",
}

func TestKeyAgainstMuninConfigTags(t *testing.T) {
	for tag, want := range muninKoanfTags {
		if got := Key(tag); got != want {
			t.Errorf("Key(%q) = %v, want %v: munin/internal/config/types.go koanf tag landed on the wrong side", tag, got, want)
		}
	}
	for _, tag := range muninDirectiveTags {
		if Key(tag) {
			t.Errorf("Key(%q) = true, want false: directive field names are not secrets", tag)
		}
	}
}

func TestKeySelectorExemptionRequiresWholeTrailingSegment(t *testing.T) {
	pairs := []struct {
		selector string
		fused    string
	}{
		{"secret_backend", "secretbackend"},
		{"secret_name", "secretname"},
		{"secret_service", "secretservice"},
		{"secret_id", "secretid"},
		{"secret_env", "secretenv"},
		{"token_env", "tokenenv"},
		{"token_name", "tokenname"},
		{"token_id", "tokenid"},
	}
	for _, p := range pairs {
		if Key(p.selector) {
			t.Errorf("Key(%q) = true, want false: the trailing segment names where a secret lives", p.selector)
		}
		if !Key(p.fused) {
			t.Errorf("Key(%q) = false, want true: a fused word is not a segmented selector", p.fused)
		}
	}
}

func TestKeyExemptionIsVoidedByAHardSecretSegment(t *testing.T) {
	for _, k := range []string{
		"secret_key_name", "password_name", "password_env", "session_id",
		"cookie_id", "credential_id", "apikey_name", "salt_name", "otp_id",
	} {
		if !Key(k) {
			t.Errorf("Key(%q) = false, want true: an exempt tail must not shelter a hard secret term", k)
		}
	}
}

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

func TestConfigKeepsBackupSelectorsUsable(t *testing.T) {
	in := []byte(strings.Join([]string{
		"backup:",
		"  secret_backend: keyring",
		"  secret_name: munin-backup-key",
		"  secret_service: munin",
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
	if !strings.Contains(got, "secret_service: munin") {
		t.Errorf("secret_service value was masked; a redacted config would resolve the wrong store:\n%s", got)
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
		"secret_service: munin",
		"oauth_client_secret: super-secret-value",
	}, "\n")
	got := Line(in)

	for _, keep := range []string{"keyring", "munin-backup-key", "secret_service: munin"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q must survive line redaction:\n%s", keep, got)
		}
	}
	if strings.Contains(got, "super-secret-value") {
		t.Errorf("oauth_client_secret must still be masked:\n%s", got)
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

func TestConfigMasksSessionAndNameSelectorLookalikes(t *testing.T) {
	in := []byte(strings.Join([]string{
		"sessionid: SESSIONBEARER",
		"secretname: FUSEDNAME",
		"password_name: PWNAME",
		"secret_key_name: KEYNAME",
		"tokenid: TOKID",
		"root_path: /var/lib/munin",
		"snapshot_path: /var/snap",
		"logs_alt: /var/log/alt",
		"bot_ping: 30s",
		"hot_patch: true",
		"",
	}, "\n"))
	got := Config(in, "yaml")

	for _, leak := range []string{"SESSIONBEARER", "FUSEDNAME", "PWNAME", "KEYNAME", "TOKID"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked through redaction:\n%s", leak, got)
		}
	}
	for _, keep := range []string{"/var/lib/munin", "/var/snap", "/var/log/alt", "30s", "true"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q must survive redaction; separators must not fuse into secret words:\n%s", keep, got)
		}
	}
}

func TestLineMasksHyphenatedAndWebhookKeys(t *testing.T) {
	in := strings.Join([]string{
		"api-key: DASHKEY",
		"webhook_url: https://hooks.slack.com/services/T/B/XYZ",
		"sessionid: SESSIONBEARER",
		"secret_backend: keyring",
		"secret_name: munin-backup-key",
		"root_path: /var/lib/munin",
		"oauth_client_secret: super-secret-value",
	}, "\n")
	got := Line(in)

	for _, leak := range []string{"DASHKEY", "T/B/XYZ", "SESSIONBEARER", "super-secret-value"} {
		if strings.Contains(got, leak) {
			t.Errorf("%q leaked through line redaction:\n%s", leak, got)
		}
	}
	for _, keep := range []string{"keyring", "munin-backup-key", "/var/lib/munin"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q must survive line redaction:\n%s", keep, got)
		}
	}
}

func TestConfigAndLineAgreeWithKey(t *testing.T) {
	for _, k := range append(append([]string{}, mustMask...), mustNotMask...) {
		if strings.ContainsAny(k, ":\n") || strings.TrimSpace(k) != k {
			continue
		}
		doc := k + ": PROBEVALUE"
		wantMasked := Key(k)

		if gotMasked := !strings.Contains(Line(doc), "PROBEVALUE"); gotMasked != wantMasked {
			t.Errorf("Line(%q) masked = %v, but Key(%q) = %v: Line must agree with Key", doc, gotMasked, k, wantMasked)
		}
		if gotMasked := !strings.Contains(Config([]byte(doc+"\n"), "yaml"), "PROBEVALUE"); gotMasked != wantMasked {
			t.Errorf("Config(%q) masked = %v, but Key(%q) = %v: Config must agree with Key", doc, gotMasked, k, wantMasked)
		}
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

func TestKeyEmptyAndPunctuationOnly(t *testing.T) {
	for _, k := range []string{"", "   ", "_", "-", "...", "__--__"} {
		if Key(k) {
			t.Errorf("Key(%q) = true, want false: no segments means no secret", k)
		}
	}
}

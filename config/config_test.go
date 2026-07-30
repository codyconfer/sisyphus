package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeOverrideWins(t *testing.T) {
	got, err := Home("/explicit", "SOME_ENV", ".app")
	if err != nil || got != "/explicit" {
		t.Fatalf("Home override = %q, %v", got, err)
	}
}

func TestHomeEnv(t *testing.T) {
	t.Setenv("APP_HOME", "/from/env")
	got, err := Home("", "APP_HOME", ".app")
	if err != nil || got != "/from/env" {
		t.Fatalf("Home env = %q, %v", got, err)
	}
}

func TestHomeDefault(t *testing.T) {
	t.Setenv("APP_HOME", "")
	got, err := Home("", "APP_HOME", ".app")
	if err != nil {
		t.Fatalf("Home default: %v", err)
	}
	hd, _ := os.UserHomeDir()
	if got != filepath.Join(hd, ".app") {
		t.Errorf("Home default = %q", got)
	}
}

func TestReadFileCandidateOrder(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.yml"), []byte("a: 1\n"), 0o600)
	os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"a":2}`), 0o600)

	raw, format, err := ReadFile(home)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if format != "yaml" || string(raw) != "a: 1\n" {
		t.Errorf("expected yml first: format=%q raw=%q", format, raw)
	}
}

func TestReadFileJSON(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"a":2}`), 0o600)
	raw, format, err := ReadFile(home)
	if err != nil || format != "json" || string(raw) != `{"a":2}` {
		t.Fatalf("ReadFile json = %q/%q/%v", raw, format, err)
	}
}

func TestReadFileCustomBasenames(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "settings.yaml"), []byte("b: 2\n"), 0o600)
	raw, format, err := ReadFile(home, "settings.yaml")
	if err != nil || format != "yaml" || string(raw) != "b: 2\n" {
		t.Fatalf("ReadFile custom = %q/%q/%v", raw, format, err)
	}
}

func TestReadFileNone(t *testing.T) {
	raw, format, err := ReadFile(t.TempDir())
	if err != nil || raw != nil || format != "" {
		t.Fatalf("ReadFile none = %q/%q/%v", raw, format, err)
	}
}

func TestParseIntoYAMLAndEnv(t *testing.T) {
	type cfg struct {
		Output string `koanf:"output"`
		Max    int    `koanf:"max"`
	}
	var c cfg
	if err := ParseInto(&c, []byte("output: terminal\nmax: 5\n"), "yaml", ""); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if c.Output != "terminal" || c.Max != 5 {
		t.Fatalf("parsed = %+v", c)
	}

	t.Setenv("APP_OUTPUT", "json")
	var c2 cfg
	if err := ParseInto(&c2, []byte("output: terminal\n"), "yaml", "APP_"); err != nil {
		t.Fatalf("ParseInto env: %v", err)
	}
	if c2.Output != "json" {
		t.Errorf("env override failed: %+v", c2)
	}
}

func TestParseIntoJSON(t *testing.T) {
	type cfg struct {
		A int `koanf:"a"`
	}
	var c cfg
	if err := ParseInto(&c, []byte(`{"a":7}`), "json", ""); err != nil {
		t.Fatalf("ParseInto json: %v", err)
	}
	if c.A != 7 {
		t.Errorf("parsed = %+v", c)
	}
}

type ghSection struct {
	APIURL string `koanf:"api_url"`
	Max    int    `koanf:"max"`
}

type cacheSection struct {
	TTL       string            `koanf:"ttl"`
	DetailTTL string            `koanf:"detail_ttl"`
	Signals   map[string]string `koanf:"signals"`
}

type backupSection struct {
	SecretBackend string `koanf:"secret_backend"`
	SecretName    string `koanf:"secret_name"`
}

type slackSection struct {
	TokenEnv string `koanf:"token_env"`
}

type tasksSection struct {
	ShowCompleted bool `koanf:"show_completed"`
}

type googleSection struct {
	OAuthClientSecret string `koanf:"oauth_client_secret"`
}

type calSection struct {
	CalendarID string `koanf:"calendar_id"`
}

type appConfig struct {
	Home   string        `koanf:"-"`
	Output string        `koanf:"output"`
	Role   string        `koanf:"role"`
	GitHub ghSection     `koanf:"github"`
	Cache  cacheSection  `koanf:"cache"`
	Backup backupSection `koanf:"backup"`
	Slack  slackSection  `koanf:"slack"`
	Tasks  tasksSection  `koanf:"tasks"`
	Google googleSection `koanf:"google"`
	Cal    calSection    `koanf:"calendar"`
}

func TestParseIntoEnvSnakeCaseKeys(t *testing.T) {
	t.Setenv("MUNIN_OUTPUT", "terminal")
	t.Setenv("MUNIN_GITHUB_API_URL", "https://ghe.example.com/api/v3")
	t.Setenv("MUNIN_SLACK_TOKEN_ENV", "CI_SLACK_TOKEN")
	t.Setenv("MUNIN_BACKUP_SECRET_BACKEND", "keyring")
	t.Setenv("MUNIN_BACKUP_SECRET_NAME", "ci-backup-key")
	t.Setenv("MUNIN_GOOGLE_OAUTH_CLIENT_SECRET", "gsecret")
	t.Setenv("MUNIN_TASKS_SHOW_COMPLETED", "true")
	t.Setenv("MUNIN_CALENDAR_CALENDAR_ID", "team@example.com")
	t.Setenv("MUNIN_CACHE_DETAIL_TTL", "90s")
	t.Setenv("MUNIN_GITHUB_MAX", "7")

	var c appConfig
	if err := ParseInto(&c, []byte("output: json\ngithub:\n  api_url: https://api.github.com\n"), "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}

	want := map[string]string{
		"output":                     c.Output,
		"github.api_url":             c.GitHub.APIURL,
		"slack.token_env":            c.Slack.TokenEnv,
		"backup.secret_backend":      c.Backup.SecretBackend,
		"backup.secret_name":         c.Backup.SecretName,
		"google.oauth_client_secret": c.Google.OAuthClientSecret,
		"calendar.calendar_id":       c.Cal.CalendarID,
		"cache.detail_ttl":           c.Cache.DetailTTL,
	}
	expect := map[string]string{
		"output":                     "terminal",
		"github.api_url":             "https://ghe.example.com/api/v3",
		"slack.token_env":            "CI_SLACK_TOKEN",
		"backup.secret_backend":      "keyring",
		"backup.secret_name":         "ci-backup-key",
		"google.oauth_client_secret": "gsecret",
		"calendar.calendar_id":       "team@example.com",
		"cache.detail_ttl":           "90s",
	}
	for key, got := range want {
		if got != expect[key] {
			t.Errorf("%s = %q, want %q", key, got, expect[key])
		}
	}
	if !c.Tasks.ShowCompleted {
		t.Error("tasks.show_completed = false, want true from MUNIN_TASKS_SHOW_COMPLETED")
	}
	if c.GitHub.Max != 7 {
		t.Errorf("github.max = %d, want 7", c.GitHub.Max)
	}
}

func TestParseIntoEnvUnknownKeysIgnored(t *testing.T) {
	t.Setenv("MUNIN_OUTPUT_DIR", "/tmp/x")
	t.Setenv("MUNIN_NOT_A_REAL_KEY", "whatever")
	t.Setenv("MUNIN_GITHUB_API_URL_EXTRA", "nope")

	var c appConfig
	if err := ParseInto(&c, []byte("output: terminal\n"), "yaml", "MUNIN_"); err != nil {
		t.Fatalf("MUNIN_OUTPUT_DIR must not break the parse: %v", err)
	}
	if c.Output != "terminal" {
		t.Errorf("output = %q, want terminal (scalar key must not be turned into a map)", c.Output)
	}
}

func TestParseIntoEnvEmptyUnknownKeyIgnored(t *testing.T) {
	t.Setenv("MUNIN_OUTPUT_DIR", "")

	var c appConfig
	if err := ParseInto(&c, []byte("output: terminal\n"), "yaml", "MUNIN_"); err != nil {
		t.Fatalf("empty MUNIN_OUTPUT_DIR must not break the parse: %v", err)
	}
	if c.Output != "terminal" {
		t.Errorf("output = %q, want terminal", c.Output)
	}
}

func TestParseIntoEnvDottedMapKeys(t *testing.T) {
	t.Setenv("MUNIN_CACHE_SIGNALS_GITHUB_PRS", "5m")
	t.Setenv("MUNIN_CACHE_SIGNALS_SLACK", "30s")

	var c appConfig
	if err := ParseInto(&c, []byte("cache:\n  signals:\n    github.prs: 60s\n    gmail: 60s\n"), "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if got := c.Cache.Signals["github.prs"]; got != "5m" {
		t.Errorf("cache.signals[github.prs] = %q, want 5m (signals=%v)", got, c.Cache.Signals)
	}
	if got := c.Cache.Signals["slack"]; got != "30s" {
		t.Errorf("cache.signals[slack] = %q, want 30s (signals=%v)", got, c.Cache.Signals)
	}
	if got := c.Cache.Signals["gmail"]; got != "60s" {
		t.Errorf("cache.signals[gmail] = %q, want the file value 60s", got)
	}
}

func TestParseIntoEnvDottedMapKeyWithoutFileEntry(t *testing.T) {
	t.Setenv("MUNIN_CACHE_SIGNALS_GITHUB_PRS", "5m")

	var c appConfig
	if err := ParseInto(&c, nil, "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if got := c.Cache.Signals["github.prs"]; got != "5m" {
		t.Errorf("cache.signals[github.prs] = %q, want 5m (signals=%v)", got, c.Cache.Signals)
	}
}

func TestParseIntoEnvIgnoredKeyDoesNotClobberTaggedSkip(t *testing.T) {
	t.Setenv("MUNIN_HOME", "/from/env")

	c := appConfig{Home: "/set/by/caller"}
	if err := ParseInto(&c, []byte("output: terminal\n"), "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if c.Home != "/set/by/caller" {
		t.Errorf("home = %q, want the caller value (koanf:\"-\" fields are not env-settable)", c.Home)
	}
}

func TestParseIntoEnvKeyMapper(t *testing.T) {
	t.Setenv("MUNIN_GH_URL", "https://ghe.example.com/api/v3")

	var c appConfig
	err := ParseInto(&c, nil, "yaml", "MUNIN_", WithEnvKeyMapper(func(name string) []string {
		if name == "MUNIN_GH_URL" {
			return []string{"github", "api_url"}
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if c.GitHub.APIURL != "https://ghe.example.com/api/v3" {
		t.Errorf("github.api_url = %q, want the mapped value", c.GitHub.APIURL)
	}
}

func TestParseIntoEnvFreeFormMapTarget(t *testing.T) {
	t.Setenv("MUNIN_A_B", "1")

	c := map[string]string{}
	if err := ParseInto(&c, nil, "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if c["a.b"] != "1" {
		t.Errorf("map target = %#v, want key %q = 1", c, "a.b")
	}
}

func TestParseIntoInvalid(t *testing.T) {
	var c struct{}
	if err := ParseInto(&c, []byte("::: not yaml :::\n"), "yaml", ""); err == nil {
		t.Fatal("expected parse error")
	}
}

package config

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

type mAudit struct {
	Enabled bool   `koanf:"enabled"`
	Path    string `koanf:"path"`
}

type mBackup struct {
	SecretBackend string `koanf:"secret_backend"`
	SecretName    string `koanf:"secret_name"`
	Destination   string `koanf:"destination"`
	Keep          int    `koanf:"keep"`
}

type mGitHub struct {
	Queries       []string `koanf:"queries"`
	OAuthClientID string   `koanf:"oauth_client_id"`
	OAuthScopes   string   `koanf:"oauth_scopes"`
	APIURL        string   `koanf:"api_url"`
	Max           int      `koanf:"max"`
}

type mGoogle struct {
	OAuthClientID     string `koanf:"oauth_client_id"`
	OAuthClientSecret string `koanf:"oauth_client_secret"`
}

type mCalendar struct {
	CalendarID string `koanf:"calendar_id"`
	Window     string `koanf:"window"`
	Max        int    `koanf:"max"`
}

type mGmail struct {
	Query string `koanf:"query"`
	Max   int    `koanf:"max"`
}

type mDocs struct {
	Dir     string   `koanf:"dir"`
	Folders []string `koanf:"folders"`
	Recent  int      `koanf:"recent"`
}

type mDrive struct {
	Recent int `koanf:"recent"`
}

type mTasks struct {
	List          string   `koanf:"list"`
	Lists         []string `koanf:"lists"`
	ShowCompleted bool     `koanf:"show_completed"`
	Max           int      `koanf:"max"`
}

type mSlack struct {
	TokenEnv          string `koanf:"token_env"`
	AppTokenEnv       string `koanf:"app_token_env"`
	BotTokenEnv       string `koanf:"bot_token_env"`
	OAuthClientID     string `koanf:"oauth_client_id"`
	OAuthClientSecret string `koanf:"oauth_client_secret"`
	UserScopes        string `koanf:"user_scopes"`
	Limit             int    `koanf:"limit"`
}

type mDaemon struct {
	Interval string `koanf:"interval"`
	Bell     bool   `koanf:"bell"`
	Desktop  bool   `koanf:"desktop"`
	Tray     bool   `koanf:"tray"`
	Theme    string `koanf:"theme"`
}

type mCache struct {
	TTL       string            `koanf:"ttl"`
	DetailTTL string            `koanf:"detail_ttl"`
	Signals   map[string]string `koanf:"signals"`
}

type muninConfig struct {
	Home     string            `koanf:"-"`
	Output   string            `koanf:"output"`
	Timeout  string            `koanf:"timeout"`
	Role     string            `koanf:"role"`
	Keybinds map[string]string `koanf:"keybinds"`
	Audit    mAudit            `koanf:"audit"`
	Backup   mBackup           `koanf:"backup"`
	GitHub   mGitHub           `koanf:"github"`
	Google   mGoogle           `koanf:"google"`
	Cal      mCalendar         `koanf:"calendar"`
	Gmail    mGmail            `koanf:"gmail"`
	Docs     mDocs             `koanf:"docs"`
	Drive    mDrive            `koanf:"drive"`
	Tasks    mTasks            `koanf:"tasks"`
	Slack    mSlack            `koanf:"slack"`
	Daemon   mDaemon           `koanf:"daemon"`
	Cache    mCache            `koanf:"cache"`
}

const muninYAML = `output: terminal
timeout: 30s
github:
  api_url: https://api.github.com
cache:
  ttl: 60s
  signals:
    github.prs: 60s
    gmail: 60s
keybinds:
  "alt+n": ntr.note.new
  "alt+]": role.next
`

var muninSectionEnv = []string{
	"MUNIN_GITHUB",
	"MUNIN_CACHE",
	"MUNIN_AUDIT",
	"MUNIN_SLACK",
	"MUNIN_BACKUP",
	"MUNIN_GOOGLE",
	"MUNIN_CALENDAR",
	"MUNIN_GMAIL",
	"MUNIN_DOCS",
	"MUNIN_DRIVE",
	"MUNIN_TASKS",
	"MUNIN_DAEMON",
	"MUNIN_KEYBINDS",
	"MUNIN_CACHE_SIGNALS",
}

func TestParseIntoEnvSectionNameIsIgnoredNotFatal(t *testing.T) {
	for _, name := range muninSectionEnv {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "boom")
			var c muninConfig
			if err := ParseInto(&c, []byte(muninYAML), "yaml", "MUNIN_"); err != nil {
				t.Fatalf("%s=boom must be ignored like an unknown key, got fatal error: %v", name, err)
			}
			if c.Output != "terminal" || c.Timeout != "30s" {
				t.Errorf("%s clobbered top-level file values: output=%q timeout=%q", name, c.Output, c.Timeout)
			}
			if c.GitHub.APIURL != "https://api.github.com" {
				t.Errorf("%s clobbered github.api_url: %q", name, c.GitHub.APIURL)
			}
			if c.Cache.TTL != "60s" || c.Cache.Signals["github.prs"] != "60s" {
				t.Errorf("%s clobbered cache: ttl=%q signals=%v", name, c.Cache.TTL, c.Cache.Signals)
			}
			if c.Keybinds["alt+n"] != "ntr.note.new" {
				t.Errorf("%s clobbered keybinds: %v", name, c.Keybinds)
			}
		})
	}
}

func TestParseIntoEnvEveryLeafResolves(t *testing.T) {
	leaves := []struct {
		env  string
		set  string
		want string
		got  func(*muninConfig) string
	}{
		{"MUNIN_OUTPUT", "json", "json", func(c *muninConfig) string { return c.Output }},
		{"MUNIN_TIMEOUT", "45s", "45s", func(c *muninConfig) string { return c.Timeout }},
		{"MUNIN_ROLE", "oncall", "oncall", func(c *muninConfig) string { return c.Role }},
		{"MUNIN_AUDIT_ENABLED", "true", "true", func(c *muninConfig) string { return fmt.Sprint(c.Audit.Enabled) }},
		{"MUNIN_AUDIT_PATH", "/tmp/audit.db", "/tmp/audit.db", func(c *muninConfig) string { return c.Audit.Path }},
		{"MUNIN_BACKUP_SECRET_BACKEND", "keyring", "keyring", func(c *muninConfig) string { return c.Backup.SecretBackend }},
		{"MUNIN_BACKUP_SECRET_NAME", "ci-key", "ci-key", func(c *muninConfig) string { return c.Backup.SecretName }},
		{"MUNIN_BACKUP_DESTINATION", "s3", "s3", func(c *muninConfig) string { return c.Backup.Destination }},
		{"MUNIN_BACKUP_KEEP", "9", "9", func(c *muninConfig) string { return fmt.Sprint(c.Backup.Keep) }},
		{"MUNIN_GITHUB_QUERIES", "is:open", "[is:open]", func(c *muninConfig) string { return fmt.Sprint(c.GitHub.Queries) }},
		{"MUNIN_GITHUB_OAUTH_CLIENT_ID", "gh-id", "gh-id", func(c *muninConfig) string { return c.GitHub.OAuthClientID }},
		{"MUNIN_GITHUB_OAUTH_SCOPES", "repo", "repo", func(c *muninConfig) string { return c.GitHub.OAuthScopes }},
		{"MUNIN_GITHUB_API_URL", "https://ghe/api/v3", "https://ghe/api/v3", func(c *muninConfig) string { return c.GitHub.APIURL }},
		{"MUNIN_GITHUB_MAX", "7", "7", func(c *muninConfig) string { return fmt.Sprint(c.GitHub.Max) }},
		{"MUNIN_GOOGLE_OAUTH_CLIENT_ID", "g-id", "g-id", func(c *muninConfig) string { return c.Google.OAuthClientID }},
		{"MUNIN_GOOGLE_OAUTH_CLIENT_SECRET", "g-secret", "g-secret", func(c *muninConfig) string { return c.Google.OAuthClientSecret }},
		{"MUNIN_CALENDAR_CALENDAR_ID", "team@example.com", "team@example.com", func(c *muninConfig) string { return c.Cal.CalendarID }},
		{"MUNIN_CALENDAR_WINDOW", "12h", "12h", func(c *muninConfig) string { return c.Cal.Window }},
		{"MUNIN_CALENDAR_MAX", "11", "11", func(c *muninConfig) string { return fmt.Sprint(c.Cal.Max) }},
		{"MUNIN_GMAIL_QUERY", "is:unread", "is:unread", func(c *muninConfig) string { return c.Gmail.Query }},
		{"MUNIN_GMAIL_MAX", "12", "12", func(c *muninConfig) string { return fmt.Sprint(c.Gmail.Max) }},
		{"MUNIN_DOCS_DIR", "/docs", "/docs", func(c *muninConfig) string { return c.Docs.Dir }},
		{"MUNIN_DOCS_FOLDERS", "notes", "[notes]", func(c *muninConfig) string { return fmt.Sprint(c.Docs.Folders) }},
		{"MUNIN_DOCS_RECENT", "13", "13", func(c *muninConfig) string { return fmt.Sprint(c.Docs.Recent) }},
		{"MUNIN_DRIVE_RECENT", "14", "14", func(c *muninConfig) string { return fmt.Sprint(c.Drive.Recent) }},
		{"MUNIN_TASKS_LIST", "work", "work", func(c *muninConfig) string { return c.Tasks.List }},
		{"MUNIN_TASKS_LISTS", "work", "[work]", func(c *muninConfig) string { return fmt.Sprint(c.Tasks.Lists) }},
		{"MUNIN_TASKS_SHOW_COMPLETED", "true", "true", func(c *muninConfig) string { return fmt.Sprint(c.Tasks.ShowCompleted) }},
		{"MUNIN_TASKS_MAX", "15", "15", func(c *muninConfig) string { return fmt.Sprint(c.Tasks.Max) }},
		{"MUNIN_SLACK_TOKEN_ENV", "CI_SLACK_TOKEN", "CI_SLACK_TOKEN", func(c *muninConfig) string { return c.Slack.TokenEnv }},
		{"MUNIN_SLACK_APP_TOKEN_ENV", "CI_APP", "CI_APP", func(c *muninConfig) string { return c.Slack.AppTokenEnv }},
		{"MUNIN_SLACK_BOT_TOKEN_ENV", "CI_BOT", "CI_BOT", func(c *muninConfig) string { return c.Slack.BotTokenEnv }},
		{"MUNIN_SLACK_OAUTH_CLIENT_ID", "s-id", "s-id", func(c *muninConfig) string { return c.Slack.OAuthClientID }},
		{"MUNIN_SLACK_OAUTH_CLIENT_SECRET", "s-secret", "s-secret", func(c *muninConfig) string { return c.Slack.OAuthClientSecret }},
		{"MUNIN_SLACK_USER_SCOPES", "search:read", "search:read", func(c *muninConfig) string { return c.Slack.UserScopes }},
		{"MUNIN_SLACK_LIMIT", "16", "16", func(c *muninConfig) string { return fmt.Sprint(c.Slack.Limit) }},
		{"MUNIN_DAEMON_INTERVAL", "90s", "90s", func(c *muninConfig) string { return c.Daemon.Interval }},
		{"MUNIN_DAEMON_BELL", "false", "false", func(c *muninConfig) string { return fmt.Sprint(c.Daemon.Bell) }},
		{"MUNIN_DAEMON_DESKTOP", "true", "true", func(c *muninConfig) string { return fmt.Sprint(c.Daemon.Desktop) }},
		{"MUNIN_DAEMON_TRAY", "true", "true", func(c *muninConfig) string { return fmt.Sprint(c.Daemon.Tray) }},
		{"MUNIN_DAEMON_THEME", "light", "light", func(c *muninConfig) string { return c.Daemon.Theme }},
		{"MUNIN_CACHE_TTL", "5m", "5m", func(c *muninConfig) string { return c.Cache.TTL }},
		{"MUNIN_CACHE_DETAIL_TTL", "90s", "90s", func(c *muninConfig) string { return c.Cache.DetailTTL }},
		{"MUNIN_CACHE_SIGNALS_GITHUB_PRS", "5m", "5m", func(c *muninConfig) string { return c.Cache.Signals["github.prs"] }},
		{"MUNIN_CACHE_SIGNALS_SLACK", "30s", "30s", func(c *muninConfig) string { return c.Cache.Signals["slack"] }},
		{"MUNIN_KEYBINDS_ALT_N", "role.next", "role.next", func(c *muninConfig) string { return c.Keybinds["alt+n"] }},
	}
	for _, leaf := range leaves {
		t.Setenv(leaf.env, leaf.set)
	}
	var c muninConfig
	if err := ParseInto(&c, []byte(muninYAML), "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto with every leaf overridden: %v", err)
	}
	for _, leaf := range leaves {
		if got := leaf.got(&c); got != leaf.want {
			t.Errorf("%s: got %q, want %q", leaf.env, got, leaf.want)
		}
	}
	if got := c.Cache.Signals["gmail"]; got != "60s" {
		t.Errorf("cache.signals[gmail] = %q, want the untouched file value 60s", got)
	}
	if got := c.Keybinds["alt+]"]; got != "role.next" {
		t.Errorf("keybinds[alt+]] = %q, want the untouched file value", got)
	}
}

func TestParseIntoEnvDynamicKeyWithPlus(t *testing.T) {
	t.Setenv("MUNIN_KEYBINDS_ALT_N", "role.next")

	var c muninConfig
	if err := ParseInto(&c, []byte(muninYAML), "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if got := c.Keybinds["alt+n"]; got != "role.next" {
		t.Errorf("keybinds[alt+n] = %q, want role.next (keys containing + must be reachable)", got)
	}
	if _, junk := c.Keybinds["alt.n"]; junk {
		t.Errorf("env created a junk sibling key: %v", c.Keybinds)
	}
	if len(c.Keybinds) != 2 {
		t.Errorf("keybinds = %v, want the 2 file keys and no new ones", c.Keybinds)
	}
}

func TestParseIntoEnvNamedSquashField(t *testing.T) {
	type inner struct {
		Alpha string `koanf:"alpha"`
	}
	type outer struct {
		Inner inner `koanf:"inner,squash"`
	}

	t.Run("squashed spelling wins", func(t *testing.T) {
		t.Setenv("T_ALPHA", "from-env")
		var c outer
		if err := ParseInto(&c, []byte("alpha: from-file\n"), "yaml", "T_"); err != nil {
			t.Fatalf("ParseInto: %v", err)
		}
		if c.Inner.Alpha != "from-env" {
			t.Errorf("inner.alpha = %q, want from-env: a named ,squash field is squashed by mapstructure, so T_ALPHA is its env name", c.Inner.Alpha)
		}
	})

	t.Run("nested spelling is ignored", func(t *testing.T) {
		t.Setenv("T_INNER_ALPHA", "from-env")
		var c outer
		if err := ParseInto(&c, []byte("alpha: from-file\n"), "yaml", "T_"); err != nil {
			t.Fatalf("ParseInto: %v", err)
		}
		if c.Inner.Alpha != "from-file" {
			t.Errorf("inner.alpha = %q, want from-file", c.Inner.Alpha)
		}
	})
}

func TestParseIntoEnvSectionWarning(t *testing.T) {
	t.Setenv("MUNIN_GITHUB", "boom")
	t.Setenv("MUNIN_CACHE_SIGNALS", "boom")
	t.Setenv("MUNIN_NOT_A_REAL_KEY", "boom")
	t.Setenv("MUNIN_OUTPUT", "json")

	got := map[string]string{}
	var c muninConfig
	err := ParseInto(&c, []byte(muninYAML), "yaml", "MUNIN_", WithEnvSectionWarning(func(name string, section []string) {
		got[name] = strings.Join(section, ".")
	}))
	if err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	want := map[string]string{
		"MUNIN_GITHUB":        "github",
		"MUNIN_CACHE_SIGNALS": "cache.signals",
	}
	if len(got) != len(want) {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("warned for %v, want exactly %v", keys, want)
	}
	for name, section := range want {
		if got[name] != section {
			t.Errorf("warning for %s = %q, want %q", name, got[name], section)
		}
	}
	if c.Output != "json" {
		t.Errorf("output = %q, want json: warnings must not stop real overrides", c.Output)
	}
}

func TestParseIntoEnvSeparatorAliasPrecedence(t *testing.T) {
	t.Setenv("MUNIN_GOOGLE_OAUTHCLIENTID", "alias")
	t.Setenv("MUNIN_GOOGLE_OAUTH_CLIENT_ID", "canonical")

	var c muninConfig
	if err := ParseInto(&c, nil, "yaml", "MUNIN_"); err != nil {
		t.Fatalf("ParseInto: %v", err)
	}
	if c.Google.OAuthClientID != "canonical" {
		t.Errorf("google.oauth_client_id = %q, want canonical: with both spellings set the highest-sorting env name wins", c.Google.OAuthClientID)
	}
}

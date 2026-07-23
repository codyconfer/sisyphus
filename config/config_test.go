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

func TestParseIntoInvalid(t *testing.T) {
	var c struct{}
	if err := ParseInto(&c, []byte("::: not yaml :::\n"), "yaml", ""); err == nil {
		t.Fatal("expected parse error")
	}
}

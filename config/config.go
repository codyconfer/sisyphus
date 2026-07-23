package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	kjson "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
)

var defaultBasenames = []string{"config.yaml", "config.yml", "config.json"}

func Home(override, envVar, dirName string) (string, error) {
	if override != "" {
		return override, nil
	}
	if envVar != "" {
		if h := os.Getenv(envVar); h != "" {
			return h, nil
		}
	}
	hd, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	return filepath.Join(hd, dirName), nil
}

func ReadFile(home string, basenames ...string) (raw []byte, format string, err error) {
	if len(basenames) == 0 {
		basenames = defaultBasenames
	}
	for _, name := range basenames {
		data, statErr := os.ReadFile(filepath.Join(home, name))
		if statErr == nil {
			return data, formatOf(name), nil
		}
		if !os.IsNotExist(statErr) {
			return nil, "", fmt.Errorf("reading %s: %w", name, statErr)
		}
	}
	return nil, "", nil
}

func formatOf(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".json") {
		return "json"
	}
	return "yaml"
}

func ParseInto(target any, raw []byte, format, envPrefix string) error {
	k := koanf.New(".")
	if len(raw) > 0 {
		parser := koanf.Parser(yaml.Parser())
		if format == "json" {
			parser = kjson.Parser()
		}
		if err := k.Load(rawbytes.Provider(raw), parser); err != nil {
			return fmt.Errorf("parsing %s config: %w", format, err)
		}
	}
	if envPrefix != "" {
		_ = k.Load(env.Provider(envPrefix, ".", func(s string) string {
			return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s, envPrefix)), "_", ".")
		}), nil)
	}
	if err := k.Unmarshal("", target); err != nil {
		return fmt.Errorf("decoding config: %w", err)
	}
	return nil
}

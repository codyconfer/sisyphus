package redact

import (
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

const Mask = "••••••"

func Config(content []byte, format string) string {
	switch strings.ToLower(format) {
	case "json":
		var v any
		if err := json.Unmarshal(content, &v); err != nil {
			return Line(string(content))
		}
		out, err := json.MarshalIndent(walk(v), "", "  ")
		if err != nil {
			return Line(string(content))
		}
		return string(out)
	default:
		var v any
		if err := yaml.Unmarshal(content, &v); err != nil {
			return Line(string(content))
		}
		out, err := yaml.Marshal(walk(v))
		if err != nil {
			return Line(string(content))
		}
		return string(out)
	}
}

func walk(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if Key(k) {
				t[k] = Mask
			} else {
				t[k] = walk(val)
			}
		}
		return t
	case map[any]any:
		for k, val := range t {
			if ks, ok := k.(string); ok && Key(ks) {
				t[k] = Mask
			} else {
				t[k] = walk(val)
			}
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = walk(val)
		}
		return t
	}
	return v
}

func Line(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		idx := strings.Index(ln, ":")
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(ln[:idx]))
		val := strings.TrimSpace(ln[idx+1:])
		if val == "" || !Key(key) {
			continue
		}
		lines[i] = ln[:idx] + `: "` + Mask + `"`
	}
	return strings.Join(lines, "\n")
}

var secretTerms = []string{
	"secret", "password", "passwd", "passphrase", "pwd",
	"token", "apikey", "api_key", "accesskey", "access_key",
	"private_key", "privatekey", "credential", "bearer",
	"cookie", "session", "signature", "salt", "otp", "passcode",
}

var selectorSuffixes = []string{"_env", "_id", "_backend", "_name"}

func Key(k string) bool {
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "id" {
		return false
	}
	for _, suffix := range selectorSuffixes {
		if strings.HasSuffix(k, suffix) {
			return false
		}
	}
	for _, term := range secretTerms {
		if strings.Contains(k, term) {
			return true
		}
	}
	return false
}

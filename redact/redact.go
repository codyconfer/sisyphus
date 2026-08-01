// Package redact masks secret-looking values in config content before it is
// displayed or logged. Keys are judged by name (IsSecretKey), values are
// replaced with Mask, and the original input is never modified — everything
// returns new strings.
package redact

import (
	"encoding/json"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Mask is the string that replaces every redacted value.
const Mask = "••••••"

// Document redacts a whole config document. It parses content as JSON when
// format is "json" (any case) and as YAML otherwise, masks every value whose
// key IsSecretKey reports secret (recursively, arrays included), and
// re-renders the document. Content that does not parse, or fails to
// re-render, falls back to the more conservative line-based Line.
func Document(content []byte, format string) string {
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
			if IsSecretKey(k) {
				t[k] = Mask
			} else {
				t[k] = walk(val)
			}
		}
		return t
	case map[any]any:
		for k, val := range t {
			if ks, ok := k.(string); ok && IsSecretKey(ks) {
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

// Line redacts s line by line without parsing it: any line shaped like
// "key: value" whose key IsSecretKey reports secret has its value replaced
// with a quoted Mask. Lines without a colon, or with an empty value, pass
// through unchanged.
func Line(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		idx := strings.Index(ln, ":")
		if idx < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(ln[:idx]))
		val := strings.TrimSpace(ln[idx+1:])
		if val == "" || !IsSecretKey(key) {
			continue
		}
		lines[i] = ln[:idx] + `: "` + Mask + `"`
	}
	return strings.Join(lines, "\n")
}

var wordTerms = []string{
	"secret", "password", "passwd", "passphrase", "passcode",
	"token", "apikey", "accesskey", "privatekey", "credential",
	"bearer", "cookie", "session", "signature", "webhook",
	"authorization", "authorisation",
}

var exactTerms = map[string]struct{}{
	"key": {}, "keys": {},
	"pwd": {}, "pwds": {},
	"otp": {}, "otps": {},
	"salt": {}, "salts": {},
}

var fusedTerms = []string{"passphrase", "webhook"}

var selectorSegments = map[string]struct{}{
	"env": {}, "id": {}, "backend": {}, "service": {}, "name": {},
}

var selectorSubjects = map[string]struct{}{
	"secret": {}, "secrets": {},
	"token": {}, "tokens": {},
}

func segments(k string) []string {
	return strings.FieldsFunc(strings.ToLower(k), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func secretSegment(seg string) bool {
	if _, ok := exactTerms[seg]; ok {
		return true
	}
	for _, term := range wordTerms {
		if strings.Contains(seg, term) {
			return true
		}
	}
	return false
}

func fusedSecret(segs []string) bool {
	if len(segs) < 2 {
		return false
	}
	joined := strings.Join(segs, "")
	for _, term := range fusedTerms {
		if strings.Contains(joined, term) {
			return true
		}
	}
	return false
}

// IsSecretKey reports whether a config key names a secret. The key is split
// into letter/digit segments and matched against secret vocabulary (secret,
// password, token, apikey, ...). Selector-style keys are exempt: a soft
// subject like "token" or "secrets" followed by a selector segment such as
// env, id, backend, service or name (e.g. "token_env", "secrets.backend")
// names where a secret lives, not the secret itself, and is not redacted.
func IsSecretKey(k string) bool {
	segs := segments(k)
	if len(segs) == 0 {
		return false
	}

	matched, hard := false, false
	for _, seg := range segs {
		if !secretSegment(seg) {
			continue
		}
		matched = true
		if _, soft := selectorSubjects[seg]; !soft {
			hard = true
		}
	}
	if !matched {
		return fusedSecret(segs)
	}
	if hard || len(segs) < 2 {
		return true
	}
	_, exempt := selectorSegments[segs[len(segs)-1]]
	return !exempt
}

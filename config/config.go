// Package config resolves an application's home directory, reads its config
// file, and parses YAML or JSON into the caller's own struct with
// environment-variable overrides layered on top. Nothing app-specific is
// baked in: the env var, directory name, file basenames, and env prefix all
// come from the caller.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	kjson "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
)

var defaultBasenames = []string{"config.yaml", "config.yml", "config.json"}

// Format names a config serialization format. It is a string type, so a
// Format field marshals to plain JSON/YAML strings and existing on-disk data
// round-trips unchanged.
type Format string

// The two formats ReadFile detects and ParseInto parses.
const (
	FormatYAML Format = "yaml"
	FormatJSON Format = "json"
)

// Home resolves the application home directory: override when non-empty,
// else the value of envVar when set and non-empty, else dirName under the
// user's home directory. It only resolves the path; nothing is created.
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

// ReadFile reads the first of the given basenames that exists under home and
// reports its format from the extension (.json means JSON, anything else
// YAML). With no basenames it tries config.yaml, config.yml, config.json in
// that order. No file at all is not an error: it returns nil content, an
// empty format, and a nil error.
func ReadFile(home string, basenames ...string) (raw []byte, format Format, err error) {
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

func formatOf(name string) Format {
	if strings.EqualFold(filepath.Ext(name), ".json") {
		return FormatJSON
	}
	return FormatYAML
}

// Option customizes ParseInto. Nil options are ignored.
type Option func(*parseOptions)

type parseOptions struct {
	envKeyMapper   func(name string) []string
	envSectionWarn func(name string, section []string)
}

// WithEnvKeyMapper replaces the reflection-based mapping from env var name to
// config key path. fn receives the full variable name (prefix included) and
// returns the path segments to set, or an empty slice to ignore the variable.
func WithEnvKeyMapper(fn func(name string) []string) Option {
	return func(o *parseOptions) { o.envKeyMapper = fn }
}

// WithEnvSectionWarning installs a callback invoked when an env var resolves
// to a config section rather than a settable leaf value. Such variables are
// skipped; the callback is the caller's chance to warn about them.
func WithEnvSectionWarning(fn func(name string, section []string)) Option {
	return func(o *parseOptions) { o.envSectionWarn = fn }
}

// ParseInto parses raw (YAML unless format is FormatJSON) into target, then
// overlays values from environment variables starting with envPrefix (no env
// handling when the prefix is empty). Variable names are matched to target's
// fields by walking its koanf tags case-insensitively, so MYAPP_SERVER_PORT
// reaches server.port; WithEnvKeyMapper overrides that mapping. Empty raw is
// fine: env overrides alone can populate target.
func ParseInto(target any, raw []byte, format Format, envPrefix string, opts ...Option) error {
	var o parseOptions
	for _, apply := range opts {
		if apply != nil {
			apply(&o)
		}
	}
	k := koanf.New(".")
	if len(raw) > 0 {
		parser := koanf.Parser(yaml.Parser())
		if format == FormatJSON {
			parser = kjson.Parser()
		}
		if err := k.Load(rawbytes.Provider(raw), parser); err != nil {
			return fmt.Errorf("parsing %s config: %w", format, err)
		}
	}
	if envPrefix != "" {
		if err := loadEnvOverrides(k, target, envPrefix, &o); err != nil {
			return err
		}
	}
	if err := k.Unmarshal("", target); err != nil {
		return fmt.Errorf("decoding config: %w", err)
	}
	return nil
}

func loadEnvOverrides(k *koanf.Koanf, target any, prefix string, o *parseOptions) error {
	mapper := o.envKeyMapper
	values := map[string]string{}
	names := []string{}
	for _, entry := range os.Environ() {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			continue
		}
		name := entry[:eq]
		if !strings.HasPrefix(name, prefix) || name == prefix {
			continue
		}
		if _, dup := values[name]; !dup {
			names = append(names, name)
		}
		values[name] = entry[eq+1:]
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	var schema *envNode
	if mapper == nil {
		schema = newEnvSchema(target, k.Raw())
	}

	overrides := map[string]any{}
	for _, name := range names {
		var path []string
		settable := true
		if mapper != nil {
			path = mapper(name)
		} else {
			path, settable = schema.resolve(envTokens(strings.TrimPrefix(name, prefix)))
		}
		if len(path) == 0 {
			continue
		}
		if !settable {
			if o.envSectionWarn != nil {
				o.envSectionWarn(name, path)
			}
			continue
		}
		setEnvPath(overrides, path, values[name])
	}
	if len(overrides) == 0 {
		return nil
	}
	encoded, err := json.Marshal(overrides)
	if err != nil {
		return fmt.Errorf("encoding %s* env overrides: %w", prefix, err)
	}
	if err := k.Load(rawbytes.Provider(encoded), kjson.Parser()); err != nil {
		return fmt.Errorf("loading %s* env overrides: %w", prefix, err)
	}
	return nil
}

func envTokens(rest string) []string {
	out := []string{}
	for _, tok := range strings.Split(strings.ToLower(rest), "_") {
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

func setEnvPath(m map[string]any, path []string, value string) {
	for _, seg := range path[:len(path)-1] {
		next, ok := m[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[seg] = next
		}
		m = next
	}
	m[path[len(path)-1]] = value
}

type envNode struct {
	children   map[string]*envNode
	normalized map[string]string
	dynamic    bool
	dynamicKey []string
	dynamicVal *envNode
}

const envMaxDepth = 12

var envSeparators = strings.NewReplacer("_", "", "-", "", ".", "", "+", "", " ", "")

func normalizeEnvKey(s string) string {
	return envSeparators.Replace(strings.ToLower(s))
}

func newEnvSchema(target any, existing map[string]any) *envNode {
	v := reflect.ValueOf(target)
	if !v.IsValid() {
		return &envNode{}
	}
	return buildEnvNode(v.Type(), v, existing, 0)
}

func buildEnvNode(t reflect.Type, v reflect.Value, existing map[string]any, depth int) *envNode {
	n := &envNode{}
	if t == nil || depth > envMaxDepth {
		return n
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
		if v.IsValid() {
			if v.IsNil() {
				v = reflect.Value{}
			} else {
				v = v.Elem()
			}
		}
	}
	if v.IsValid() && v.Kind() == reflect.Interface {
		if v.IsNil() {
			v = reflect.Value{}
		} else {
			v = v.Elem()
		}
	}
	switch t.Kind() {
	case reflect.Struct:
		n.children = map[string]*envNode{}
		n.normalized = map[string]string{}
		addStructFields(n, t, v, existing, depth)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return n
		}
		n.dynamic = true
		n.dynamicVal = buildEnvNode(t.Elem(), reflect.Value{}, nil, depth+1)
		seen := map[string]struct{}{}
		if v.IsValid() && v.Kind() == reflect.Map && !v.IsNil() {
			for _, mk := range v.MapKeys() {
				key := mk.String()
				if key == "" {
					continue
				}
				if _, dup := seen[key]; !dup {
					seen[key] = struct{}{}
					n.dynamicKey = append(n.dynamicKey, key)
				}
			}
		}
		for key := range existing {
			if key == "" {
				continue
			}
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				n.dynamicKey = append(n.dynamicKey, key)
			}
		}
		sort.Strings(n.dynamicKey)
	}
	return n
}

func addStructFields(n *envNode, t reflect.Type, v reflect.Value, existing map[string]any, depth int) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, squash := envFieldName(f)
		if name == "-" {
			continue
		}
		var fv reflect.Value
		if v.IsValid() && v.Kind() == reflect.Struct {
			fv = v.Field(i)
		}
		if squash {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && depth <= envMaxDepth {
				addStructFields(n, ft, derefValue(fv), existing, depth)
			}
			continue
		}
		if name == "" {
			continue
		}
		child := buildEnvNode(f.Type, fv, childMap(existing, name), depth+1)
		if _, dup := n.children[name]; dup {
			continue
		}
		n.children[name] = child
		if norm := normalizeEnvKey(name); norm != "" {
			if _, dup := n.normalized[norm]; !dup {
				n.normalized[norm] = name
			}
		}
	}
}

func derefValue(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func envFieldName(f reflect.StructField) (name string, squash bool) {
	tag, ok := f.Tag.Lookup("koanf")
	if !ok {
		return strings.ToLower(f.Name), false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "squash" {
			squash = true
		}
	}
	if name == "" && !squash {
		return strings.ToLower(f.Name), false
	}
	return name, squash
}

func childMap(existing map[string]any, name string) map[string]any {
	if existing == nil {
		return nil
	}
	sub, ok := existing[name].(map[string]any)
	if !ok {
		return nil
	}
	return sub
}

func (n *envNode) leaf() bool {
	return n == nil || (len(n.children) == 0 && !n.dynamic)
}

func (n *envNode) resolve(tokens []string) (path []string, settable bool) {
	if n == nil {
		return nil, false
	}
	if len(tokens) == 0 {
		return []string{}, n.leaf()
	}
	var section []string
	for i := len(tokens); i >= 1; i-- {
		for _, name := range n.match(tokens[:i]) {
			rest, ok := n.children[name].resolve(tokens[i:])
			if rest == nil {
				continue
			}
			if ok {
				return append([]string{name}, rest...), true
			}
			if section == nil {
				section = append([]string{name}, rest...)
			}
		}
	}
	if !n.dynamic {
		return section, false
	}
	for i := len(tokens); i >= 1; i-- {
		want := normalizeEnvKey(strings.Join(tokens[:i], ""))
		for _, key := range n.dynamicKey {
			if normalizeEnvKey(key) != want {
				continue
			}
			rest, ok := n.dynamicVal.resolve(tokens[i:])
			if rest == nil {
				continue
			}
			if ok {
				return append([]string{key}, rest...), true
			}
			if section == nil {
				section = append([]string{key}, rest...)
			}
		}
	}
	if section != nil {
		return section, false
	}
	if n.dynamicVal.leaf() {
		return []string{strings.Join(tokens, ".")}, true
	}
	return nil, false
}

func (n *envNode) match(tokens []string) []string {
	if len(n.children) == 0 {
		return nil
	}
	var out []string
	joined := strings.Join(tokens, "_")
	if _, ok := n.children[joined]; ok {
		out = append(out, joined)
	}
	if name, ok := n.normalized[normalizeEnvKey(strings.Join(tokens, ""))]; ok && (len(out) == 0 || out[0] != name) {
		out = append(out, name)
	}
	return out
}

package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
)

// envRefPattern matches an embedded "${env:NAME}" reference so designated
// fields support both exact references ("${env:TOKEN}") and embedded ones
// ("Bearer ${env:TOKEN}", "https://host/path/${env:TOKEN}").
var envRefPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnvRefString replaces every "${env:NAME}" reference in s with
// os.Getenv(NAME). Missing env vars expand to "" (references degrade safely
// when a feature is optional). Strings without references pass through.
func expandEnvRefString(s string) string {
	if !strings.Contains(s, "${env:") {
		return s
	}
	return envRefPattern.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[len("${env:") : len(m)-1])
	})
}

// ExpandEnv resolves "${env:NAME}" references in DESIGNATED fields only -
// the enumerated credential-bearing fields tagged `expand:"env"` in
// internal/config/schemas (see docs/configuration.md "Token expansion" for
// the exact list). Every other field keeps `${env:...}` as literal text.
//
// This is a security boundary, not a convenience default: .reeve/*.yaml is
// loaded from the PR HEAD, so config content is attacker-controlled before
// approval. An unrestricted walk-everything expansion would let any new
// config field become an env-var oracle. A new field therefore gets NO
// expansion unless it is deliberately opted in with the struct tag.
//
// The returned warnings name non-designated fields that contain a
// "${env:" reference (left literal), so typos and unsupported placements
// surface at load/lint time instead of failing silently.
func (c *Config) ExpandEnv() []string {
	if c == nil {
		return nil
	}
	w := &envExpandWalker{}
	w.walk(reflect.ValueOf(c), "", false)
	return w.warnings
}

type envExpandWalker struct {
	warnings []string
}

func (w *envExpandWalker) warn(path string) {
	w.warnings = append(w.warnings,
		fmt.Sprintf("env expansion is not supported for field %s - the ${env:...} reference is kept as literal text (designated fields are listed in docs/configuration.md#token-expansion)", path))
}

// walk descends the config. designated is inherited downward: tagging a
// map/slice field designates its values, tagging a struct field designates
// every string beneath it.
func (w *envExpandWalker) walk(v reflect.Value, path string, designated bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			w.walk(v.Elem(), path, designated)
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		// A designated `any` field holding a string (e.g. auth app_id) is
		// replaced wholesale; other dynamic types descend read-only.
		if s, ok := v.Interface().(string); ok {
			if designated {
				if v.CanSet() {
					v.Set(reflect.ValueOf(expandEnvRefString(s)))
				}
			} else if strings.Contains(s, "${env:") {
				w.warn(path)
			}
			return
		}
		w.walk(v.Elem(), path, designated)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue // unexported: not settable, skip
			}
			w.walk(v.Field(i), joinFieldPath(path, f), designated || f.Tag.Get("expand") == "env")
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			w.walk(v.Index(i), fmt.Sprintf("%s[%d]", path, i), designated)
		}
	case reflect.Map:
		// Map values aren't addressable; copy, expand, reinsert.
		for _, k := range v.MapKeys() {
			mv := v.MapIndex(k)
			cp := reflect.New(mv.Type()).Elem()
			cp.Set(mv)
			w.walk(cp, fmt.Sprintf("%s[%v]", path, k.Interface()), designated)
			v.SetMapIndex(k, cp)
		}
	case reflect.String:
		if designated {
			if v.CanSet() {
				v.SetString(expandEnvRefString(v.String()))
			}
			return
		}
		if strings.Contains(v.String(), "${env:") {
			w.warn(path)
		}
	}
}

// joinFieldPath builds a human-readable dotted path using yaml tag names so
// warnings match what users wrote in the file. Inline/embedded fields keep
// the parent path.
func joinFieldPath(parent string, f reflect.StructField) string {
	name := strings.Split(f.Tag.Get("yaml"), ",")[0]
	if name == "" || name == "-" {
		if f.Anonymous {
			return parent // embedded (e.g. Header `yaml:",inline"`)
		}
		name = strings.ToLower(f.Name)
	}
	if parent == "" {
		return name
	}
	return parent + "." + name
}

// Package config provides AgentMesh's shared configuration-loading policy.
//
// Architecture.md §12 requires: "Each service reads configuration from
// environment variables first, with a config.yaml override for local
// development (12-factor style). No configuration is baked into container
// images."
//
// This package implements that precedence generically so every service
// (Collector, Query API, MCP Gateway, ...) loads configuration the same way
// instead of each hand-rolling its own env-var parsing — a direct instance
// of the "avoid duplicated logic" project standard.
//
// Precedence (highest wins): explicit environment variable > value from an
// optional YAML file > the field's declared default. A service defines its
// own typed config struct with `env` and `yaml` struct tags; Load populates
// it.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader loads configuration into a caller-supplied struct pointer following
// the env-over-YAML-over-default precedence described in the package doc.
type Loader struct {
	// Getenv is the environment lookup function; overridable in tests so
	// config-loading logic never depends on process-global environment state
	// during unit tests (Phase 3's "independently testable" standard).
	Getenv func(key string) (string, bool)
}

// NewLoader returns a Loader backed by the real process environment.
func NewLoader() *Loader {
	return &Loader{Getenv: os.LookupEnv}
}

// Load populates dst (a pointer to a struct) from, in precedence order:
// the process environment (via each field's `env` tag), then yamlPath if
// non-empty and the file exists, then the zero-value default already present
// on dst before Load is called (callers should pre-populate defaults on the
// struct literal before calling Load).
//
// Load returns an error if dst is not a non-nil pointer to a struct, if the
// YAML file exists but fails to parse, or if an environment variable's value
// cannot be converted to the destination field's type.
func (l *Loader) Load(dst any, yamlPath string) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Ptr || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: Load requires a non-nil pointer to a struct, got %T", dst)
	}

	if yamlPath != "" {
		if data, err := os.ReadFile(yamlPath); err == nil {
			if err := yaml.Unmarshal(data, dst); err != nil {
				return fmt.Errorf("config: parsing %s: %w", yamlPath, err)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("config: reading %s: %w", yamlPath, err)
		}
		// A missing YAML file is not an error: local dev overrides are optional.
	}

	return l.applyEnv(v.Elem())
}

func (l *Loader) applyEnv(structVal reflect.Value) error {
	t := structVal.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		envKey := field.Tag.Get("env")
		if envKey == "" {
			continue
		}
		raw, ok := l.Getenv(envKey)
		if !ok || raw == "" {
			continue // leave YAML value or zero-value default in place
		}
		fieldVal := structVal.Field(i)
		if err := setFromString(fieldVal, raw); err != nil {
			return fmt.Errorf("config: env %s=%q: %w", envKey, raw, err)
		}
	}
	return nil
}

func setFromString(field reflect.Value, raw string) error {
	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("expected integer: %w", err)
		}
		field.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("expected boolean: %w", err)
		}
		field.SetBool(b)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("expected float: %w", err)
		}
		field.SetFloat(f)
	case reflect.Slice:
		if field.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice element type %s", field.Type().Elem())
		}
		parts := strings.Split(raw, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		field.Set(reflect.ValueOf(parts))
	default:
		return fmt.Errorf("unsupported config field type %s", field.Kind())
	}
	return nil
}

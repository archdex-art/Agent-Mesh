package config

import (
	"os"
	"path/filepath"
	"testing"
)

type testConfig struct {
	Host        string   `env:"AM_HOST" yaml:"host"`
	Port        int      `env:"AM_PORT" yaml:"port"`
	Debug       bool     `env:"AM_DEBUG" yaml:"debug"`
	SampleRate  float64  `env:"AM_SAMPLE_RATE" yaml:"sample_rate"`
	AllowedTags []string `env:"AM_ALLOWED_TAGS" yaml:"allowed_tags"`
}

func fakeEnv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestLoadAppliesStructDefaultsWhenNothingElseSet(t *testing.T) {
	cfg := testConfig{Host: "localhost", Port: 8080}
	loader := &Loader{Getenv: fakeEnv(nil)}

	if err := loader.Load(&cfg, ""); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "localhost" || cfg.Port != 8080 {
		t.Fatalf("defaults were not preserved: %+v", cfg)
	}
}

func TestLoadYAMLOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yamlContent := "host: yaml-host\nport: 9090\n"
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("writing temp yaml: %v", err)
	}

	cfg := testConfig{Host: "default-host", Port: 8080}
	loader := &Loader{Getenv: fakeEnv(nil)}
	if err := loader.Load(&cfg, path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "yaml-host" {
		t.Errorf("Host = %q, want %q", cfg.Host, "yaml-host")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
}

func TestLoadEnvOverridesYAMLAndDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("host: yaml-host\nport: 9090\n"), 0o644); err != nil {
		t.Fatalf("writing temp yaml: %v", err)
	}

	cfg := testConfig{Host: "default-host", Port: 8080}
	loader := &Loader{Getenv: fakeEnv(map[string]string{
		"AM_HOST": "env-host",
	})}
	if err := loader.Load(&cfg, path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// AM_HOST env var must win over the YAML value.
	if cfg.Host != "env-host" {
		t.Errorf("Host = %q, want %q (env should win)", cfg.Host, "env-host")
	}
	// Port has no env override, so the YAML value should still apply.
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090 (yaml should apply where env is absent)", cfg.Port)
	}
}

func TestLoadMissingYAMLFileIsNotAnError(t *testing.T) {
	cfg := testConfig{Host: "default-host"}
	loader := &Loader{Getenv: fakeEnv(nil)}
	if err := loader.Load(&cfg, "/nonexistent/path/config.yaml"); err != nil {
		t.Fatalf("Load with missing yaml file should not error, got: %v", err)
	}
	if cfg.Host != "default-host" {
		t.Fatalf("Host = %q, want default preserved", cfg.Host)
	}
}

func TestLoadMalformedYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("host: [unterminated"), 0o644); err != nil {
		t.Fatalf("writing temp yaml: %v", err)
	}
	cfg := testConfig{}
	loader := &Loader{Getenv: fakeEnv(nil)}
	if err := loader.Load(&cfg, path); err == nil {
		t.Fatal("Load with malformed yaml succeeded, want error")
	}
}

func TestLoadAllScalarTypes(t *testing.T) {
	cfg := testConfig{}
	loader := &Loader{Getenv: fakeEnv(map[string]string{
		"AM_HOST":         "env-host",
		"AM_PORT":         "1234",
		"AM_DEBUG":        "true",
		"AM_SAMPLE_RATE":  "0.25",
		"AM_ALLOWED_TAGS": "a, b ,c",
	})}
	if err := loader.Load(&cfg, ""); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "env-host" {
		t.Errorf("Host = %q", cfg.Host)
	}
	if cfg.Port != 1234 {
		t.Errorf("Port = %d", cfg.Port)
	}
	if !cfg.Debug {
		t.Errorf("Debug = %v, want true", cfg.Debug)
	}
	if cfg.SampleRate != 0.25 {
		t.Errorf("SampleRate = %v, want 0.25", cfg.SampleRate)
	}
	want := []string{"a", "b", "c"}
	if len(cfg.AllowedTags) != len(want) {
		t.Fatalf("AllowedTags = %v, want %v", cfg.AllowedTags, want)
	}
	for i := range want {
		if cfg.AllowedTags[i] != want[i] {
			t.Errorf("AllowedTags[%d] = %q, want %q", i, cfg.AllowedTags[i], want[i])
		}
	}
}

func TestLoadInvalidIntEnvReturnsError(t *testing.T) {
	cfg := testConfig{}
	loader := &Loader{Getenv: fakeEnv(map[string]string{"AM_PORT": "not-a-number"})}
	if err := loader.Load(&cfg, ""); err == nil {
		t.Fatal("Load with invalid int env succeeded, want error")
	}
}

func TestLoadRejectsNonPointer(t *testing.T) {
	loader := &Loader{Getenv: fakeEnv(nil)}
	if err := loader.Load(testConfig{}, ""); err == nil {
		t.Fatal("Load with non-pointer succeeded, want error")
	}
}

func TestLoadRejectsNilPointer(t *testing.T) {
	loader := &Loader{Getenv: fakeEnv(nil)}
	var cfg *testConfig
	if err := loader.Load(cfg, ""); err == nil {
		t.Fatal("Load with nil pointer succeeded, want error")
	}
}

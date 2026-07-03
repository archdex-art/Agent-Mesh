package localconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// withHome points $HOME (and, on Windows, USERPROFILE — os.UserHomeDir
// consults that instead) at a fresh t.TempDir() for the duration of one
// test, so Load/Save never touch the real developer's ~/.agentmesh.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()

	envVar := "HOME"
	if runtime.GOOS == "windows" {
		envVar = "USERPROFILE"
	}
	t.Setenv(envVar, home)
	return home
}

func TestLoadReturnsZeroConfigWhenFileMissing(t *testing.T) {
	withHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: unexpected error for a missing file: %v", err)
	}
	if cfg != (Config{}) {
		t.Errorf("cfg = %+v, want a zero Config", cfg)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	withHome(t)

	want := Config{
		SessionToken: "ams_abc123",
		APIKey:       "am_live_def456",
		QueryAPIURL:  "http://localhost:8080",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestSaveWritesFileWithMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode bits don't apply on windows")
	}
	home := withHome(t)

	if err := Save(Config{SessionToken: "ams_abc123"}); err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}

	p := filepath.Join(home, ".agentmesh", "config.json")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600", perm)
	}

	dirInfo, err := os.Stat(filepath.Join(home, ".agentmesh"))
	if err != nil {
		t.Fatalf("stat %s/.agentmesh: %v", home, err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %o, want 0700", perm)
	}
}

func TestSaveCreatesParentDirectoryWhenMissing(t *testing.T) {
	home := withHome(t)

	if _, err := os.Stat(filepath.Join(home, ".agentmesh")); !os.IsNotExist(err) {
		t.Fatalf("precondition failed: ~/.agentmesh already exists in fresh tempdir")
	}

	if err := Save(Config{SessionToken: "ams_abc123"}); err != nil {
		t.Fatalf("Save: unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agentmesh")); err != nil {
		t.Errorf("Save should have created ~/.agentmesh: %v", err)
	}
}

func TestSaveOverwritesPreviousContent(t *testing.T) {
	withHome(t)

	if err := Save(Config{SessionToken: "old-token", APIKey: "old-key"}); err != nil {
		t.Fatalf("Save (first): unexpected error: %v", err)
	}
	if err := Save(Config{SessionToken: "new-token"}); err != nil {
		t.Fatalf("Save (second): unexpected error: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if got.SessionToken != "new-token" {
		t.Errorf("SessionToken = %q, want new-token", got.SessionToken)
	}
	if got.APIKey != "" {
		t.Errorf("APIKey = %q, want empty (fully overwritten, not merged) after re-Save with no APIKey field", got.APIKey)
	}
}

func TestLoadSurfacesErrorOnMalformedJSON(t *testing.T) {
	home := withHome(t)

	dir := filepath.Join(home, ".agentmesh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load: expected an error for malformed JSON, got nil")
	}
}

func TestPathReturnsFileUnderHomeAgentmeshDir(t *testing.T) {
	home := withHome(t)

	p, err := Path()
	if err != nil {
		t.Fatalf("Path: unexpected error: %v", err)
	}
	want := filepath.Join(home, ".agentmesh", "config.json")
	if p != want {
		t.Errorf("Path() = %q, want %q", p, want)
	}
}

func TestConfigJSONFieldsOmitEmptyValues(t *testing.T) {
	raw, err := json.Marshal(Config{SessionToken: "only-this-set"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, present := asMap["api_key"]; present {
		t.Errorf("encoded JSON %s should omit api_key when empty", raw)
	}
	if _, present := asMap["query_api_url"]; present {
		t.Errorf("encoded JSON %s should omit query_api_url when empty", raw)
	}
	if asMap["session_token"] != "only-this-set" {
		t.Errorf("encoded JSON %s should include the set session_token field", raw)
	}
}

// Package localconfig persists the AgentMesh CLI's per-user login state
// — the session token and resolved API key `agentmesh login` obtains —
// to a small JSON file under the user's home directory, so subsequent
// commands (`agentmesh tail`, `agentmesh mcp register`) can pick up a
// stored API key without the caller re-passing --api-key or
// re-exporting $AGENTMESH_API_KEY every time (cmd/apikey.go's
// resolveAPIKey fallback chain).
//
// This file is a convenience cache, not a source of truth: it is never
// required to exist (Load returns a zero Config, no error, when it's
// absent — "not logged in yet" is a normal state, not a failure) and it
// is never the whole story either — a project created before the file
// existed, or on another machine, only ever has its key recoverable via
// this file or the environment, because the Query API never re-exposes
// a raw API key after creation.
package localconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the on-disk shape of ~/.agentmesh/config.json.
type Config struct {
	SessionToken string `json:"session_token,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	QueryAPIURL  string `json:"query_api_url,omitempty"`
}

// dir returns ~/.agentmesh, the directory config.json lives in.
func dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".agentmesh"), nil
}

// path returns ~/.agentmesh/config.json.
func path() (string, error) {
	d, err := dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// Path returns the absolute path to ~/.agentmesh/config.json — exported
// so callers (cmd/login.go's post-save summary) can tell the user
// exactly where their credentials landed without reimplementing the
// home-directory join.
func Path() (string, error) {
	return path()
}

// Load reads ~/.agentmesh/config.json. A missing file is not an error —
// it is the normal state for a caller who has never run `agentmesh
// login` and is relying on --api-key or $AGENTMESH_API_KEY instead — so
// Load returns a zero Config and a nil error in that case, only
// surfacing an error when the file exists but can't be read or parsed.
func Load() (Config, error) {
	p, err := path()
	if err != nil {
		return Config{}, err
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("reading %s: %w", p, err)
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", p, err)
	}
	return cfg, nil
}

// Save writes cfg to ~/.agentmesh/config.json with 0600 permissions —
// it holds a live session token and API key, both bearer credentials
// good until explicit revocation — creating ~/.agentmesh with 0700
// first if it doesn't exist yet. The permissions are enforced with an
// explicit Chmod (rather than relying on WriteFile's create-only mode
// bit) so re-running `agentmesh login` tightens permissions on an
// existing file too, not just a freshly created one.
func Save(cfg Config) error {
	d, err := dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", d, err)
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	p := filepath.Join(d, "config.json")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", p, err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", p, err)
	}
	return nil
}

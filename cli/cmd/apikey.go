package main

import (
	"os"

	"github.com/agentmesh/agentmesh/cli/internal/localconfig"
)

// resolveAPIKey implements the CLI's single API-key fallback chain,
// shared by every command that needs one (tail.go, mcp.go's register
// subcommand): an explicit --api-key flag wins, then $AGENTMESH_API_KEY,
// then whatever `agentmesh login` last stored in
// ~/.agentmesh/config.json. Centralizing the chain here means the two
// call sites can never silently diverge on fallback order — pass the
// flag's current value in and use the (possibly still empty) result the
// same way the old os.Getenv-only default was used.
func resolveAPIKey(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envValue := os.Getenv("AGENTMESH_API_KEY"); envValue != "" {
		return envValue
	}
	cfg, err := localconfig.Load()
	if err != nil {
		// A corrupt or unreadable config file should not block a
		// command that has another way to get an API key (or will
		// fail its own clear "no API key" check below) — treat it the
		// same as "no stored key."
		return ""
	}
	return cfg.APIKey
}

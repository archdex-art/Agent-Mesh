// Command agentmesh is the AgentMesh CLI (Architecture.md §10, Milestone
// 5): a single static binary with no runtime dependency, distributed via
// GitHub Releases + Homebrew (Technical Roadmap.md §10).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "agentmesh",
	Short: "AgentMesh CLI — local developer workflows for a self-hosted AgentMesh install",
	Long: `agentmesh is the command-line client for AgentMesh, a framework-agnostic
control plane for AI agents. See https://github.com/agentmesh/agentmesh for
the full project.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Version:       fmt.Sprintf("%s (%s)", version, commit),
}

// Execute runs the CLI, exiting non-zero on error — the single entry
// point cmd/main.go calls.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(tailCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(loginCmd)
}

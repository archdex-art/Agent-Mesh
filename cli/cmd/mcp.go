package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/agentmesh/agentmesh/cli/internal/manifest"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "MCP server registration workflows",
}

var mcpValidateCmd = &cobra.Command{
	Use:   "validate <manifest.yaml>",
	Short: "Lint an MCP server manifest before registering it",
	Long: `validate checks required fields (name, upstream_url, transport, version,
owner) and warns on missing auth configuration, per Milestones.md's
Milestone 5 scope. It never contacts the network — 'agentmesh mcp
register' (Milestone 6) reuses this same validation before it does.`,
	Args: cobra.ExactArgs(1),
	RunE: runMCPValidate,
}

func init() {
	mcpCmd.AddCommand(mcpValidateCmd)
}

func runMCPValidate(cmd *cobra.Command, args []string) error {
	path := args[0]
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	result, err := manifest.Validate(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	if len(result.Findings) == 0 {
		fmt.Printf("%s: OK — no issues found\n", path)
		return nil
	}

	for _, f := range result.Findings {
		fmt.Println(f.String())
	}

	if result.HasErrors() {
		return fmt.Errorf("%s: failed validation", path)
	}
	fmt.Printf("\n%s: OK, with warnings\n", path)
	return nil
}

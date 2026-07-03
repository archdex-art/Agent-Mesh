package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/agentmesh/agentmesh/cli/internal/manifest"
	"github.com/agentmesh/agentmesh/cli/internal/registryclient"
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

var (
	mcpRegisterAPIKey     string
	mcpRegisterQueryAPI   string
	mcpRegisterGatewayURL string
)

var mcpRegisterCmd = &cobra.Command{
	Use:   "register <manifest.yaml>",
	Short: "Validate and register an MCP server with the Registry",
	Long: `register runs the same checks as 'agentmesh mcp validate' (refusing to
proceed if any Finding is an error), then POSTs the manifest to the Query
API's Registry (docs/plan/Architecture.md §10: "agentmesh mcp register
<manifest> --gateway-url <url> — registers a server and prints the
Gateway URL to swap into the agent's config").

Two things this command deliberately does NOT do (Milestone 6 scope
boundary, not oversights):
  - It does not upload a guardrail policy (rule_dsl) alongside the
    manifest. Policy-document registration via the CLI is a documented
    future enhancement; the Registry accepts an optional rule_dsl field,
    this command just never populates it.
  - It does not issue a per-caller OAuth bearer token (POST
    /v1/mcp/servers/{id}/tokens). That is a separate concern from
    manifest validation's own optional 'auth:' advisory block, and would
    be its own future affordance (e.g. 'agentmesh mcp token create').`,
	Args: cobra.ExactArgs(1),
	RunE: runMCPRegister,
}

func init() {
	mcpCmd.AddCommand(mcpValidateCmd)
	mcpCmd.AddCommand(mcpRegisterCmd)

	mcpRegisterCmd.Flags().StringVar(&mcpRegisterAPIKey, "api-key", "",
		"AgentMesh API key (defaults to $AGENTMESH_API_KEY, then the key stored by 'agentmesh login')")
	mcpRegisterCmd.Flags().StringVar(&mcpRegisterQueryAPI, "query-api-url", "http://localhost:8080",
		"Query API base URL, where the MCP Registry lives")
	mcpRegisterCmd.Flags().StringVar(&mcpRegisterGatewayURL, "gateway-url", "http://localhost:8090",
		"MCP Gateway base URL to print as the agent's new MCP endpoint")
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

func runMCPRegister(cmd *cobra.Command, args []string) error {
	path := args[0]
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	result, err := manifest.Validate(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}

	for _, f := range result.Findings {
		fmt.Println(f.String())
	}
	if result.HasErrors() {
		return fmt.Errorf("%s: failed validation, fix the errors above before registering", path)
	}

	mcpRegisterAPIKey = resolveAPIKey(mcpRegisterAPIKey)
	if mcpRegisterAPIKey == "" {
		return fmt.Errorf("no API key: pass --api-key, set AGENTMESH_API_KEY, or run `agentmesh login`")
	}

	m := result.Manifest
	resp, err := registryclient.Register(mcpRegisterQueryAPI, mcpRegisterAPIKey, registryclient.RegisterRequest{
		Name:         m.Name,
		UpstreamURL:  m.UpstreamURL,
		Transport:    string(m.Transport),
		Version:      m.Version,
		Owner:        m.Owner,
		ManifestYAML: string(raw),
		// RuleDSL is intentionally left empty — see the command's Long
		// description for why this is Milestone 6 scope, not an
		// oversight.
	})
	if err != nil {
		return fmt.Errorf("registering %s with the Registry at %s: %w", path, mcpRegisterQueryAPI, err)
	}

	fmt.Printf("Registered server %q (id=%s). Point your agent's MCP client at: %s/v1/mcp/%s\n",
		resp.Name, resp.ID, strings.TrimRight(mcpRegisterGatewayURL, "/"), resp.Name)
	return nil
}

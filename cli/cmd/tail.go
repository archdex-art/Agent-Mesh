package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/agentmesh/agentmesh/cli/internal/tailclient"
	"github.com/agentmesh/agentmesh/cli/internal/tui"
)

var (
	tailProject    string
	tailAPIKey     string
	tailGatewayURL string
)

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Live-stream spans for a project as they arrive",
	Long: `tail connects to a self-hosted AgentMesh Realtime Gateway and renders
spans in a scrolling terminal view as the instrumented agent produces
them — the "watch spans stream in within ~1 second of each tool call"
workflow from Milestones.md's Milestone 5 success criteria.`,
	RunE: runTail,
}

func init() {
	tailCmd.Flags().StringVar(&tailProject, "project", "", "Project ID to tail (required)")
	tailCmd.Flags().StringVar(&tailAPIKey, "api-key", os.Getenv("AGENTMESH_API_KEY"),
		"AgentMesh API key (defaults to $AGENTMESH_API_KEY)")
	tailCmd.Flags().StringVar(&tailGatewayURL, "gateway-url", "http://localhost:8081",
		"Realtime Gateway base URL")
	_ = tailCmd.MarkFlagRequired("project")
}

func runTail(cmd *cobra.Command, args []string) error {
	if tailAPIKey == "" {
		return fmt.Errorf("no API key: pass --api-key or set AGENTMESH_API_KEY")
	}

	client, err := tailclient.Dial(tailGatewayURL, tailAPIKey)
	if err != nil {
		return fmt.Errorf("connecting to realtime gateway at %s: %w", tailGatewayURL, err)
	}
	defer client.Close()

	model := tui.NewTailModel(client, tailProject)
	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("running tail view: %w", err)
	}
	return nil
}

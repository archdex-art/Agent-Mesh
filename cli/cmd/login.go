package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/agentmesh/agentmesh/cli/internal/authclient"
	"github.com/agentmesh/agentmesh/cli/internal/localconfig"
)

var loginQueryAPIURL string

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in with an AgentMesh account and store credentials locally",
	Long: `login authenticates against the Query API's account-management
surface (POST /v1/auth/login) and stores the resulting session token —
plus, where one can be obtained, a project API key — in
~/.agentmesh/config.json (mode 0600). Every other command that needs an
API key ('tail', 'mcp register') falls back to this file when neither
--api-key nor $AGENTMESH_API_KEY is set.

This is a new, additive way to authenticate. It does not replace or
require the existing anonymous 'POST /v1/setup' workflow that
self-hosted/CI/local-dev users can keep using with no account at all.

Because AgentMesh never re-exposes a raw API key once a project has been
created, logging into an account that already owns one or more projects
cannot recover their keys through this command — it can only mint a
fresh key by creating a new project. To use an existing project's key,
pass it directly via --api-key if you still have it saved somewhere.`,
	RunE: runLogin,
}

func init() {
	loginCmd.Flags().StringVar(&loginQueryAPIURL, "query-api-url", "http://localhost:8080",
		"Query API base URL, where account/session endpoints live")
}

func runLogin(cmd *cobra.Command, args []string) error {
	stdin := bufio.NewReader(os.Stdin)

	email, err := promptLine(stdin, "Email: ")
	if err != nil {
		return fmt.Errorf("reading email: %w", err)
	}
	if email == "" {
		return fmt.Errorf("email is required")
	}

	password, err := promptPassword("Password: ")
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	if password == "" {
		return fmt.Errorf("password is required")
	}

	sessionToken, userID, err := authclient.Login(loginQueryAPIURL, email, password)
	if err != nil {
		return fmt.Errorf("logging in at %s: %w", loginQueryAPIURL, err)
	}
	fmt.Printf("Logged in as %s (user %s).\n", email, userID)

	projects, err := authclient.ListProjects(loginQueryAPIURL, sessionToken)
	if err != nil {
		return fmt.Errorf("listing your projects: %w", err)
	}

	apiKey, err := resolveProjectAPIKey(stdin, loginQueryAPIURL, sessionToken, projects)
	if err != nil {
		return err
	}

	// Load-modify-save rather than overwrite: a caller who re-runs
	// `login` without creating a new project (e.g. they already had
	// projects and answered "no") should keep whatever API key a
	// previous login or manual edit stored, not lose it.
	cfg, err := localconfig.Load()
	if err != nil {
		return fmt.Errorf("reading existing local config: %w", err)
	}
	cfg.SessionToken = sessionToken
	cfg.QueryAPIURL = loginQueryAPIURL
	if apiKey != "" {
		cfg.APIKey = apiKey
	}
	if err := localconfig.Save(cfg); err != nil {
		return fmt.Errorf("saving local config: %w", err)
	}

	configPath, pathErr := localconfig.Path()
	if pathErr != nil {
		configPath = "~/.agentmesh/config.json"
	}
	fmt.Println()
	fmt.Printf("Stored to %s (mode 0600):\n", configPath)
	fmt.Println("  - session token: yes")
	switch {
	case apiKey != "":
		fmt.Println("  - api key:       yes (from the project just created)")
	case cfg.APIKey != "":
		fmt.Println("  - api key:       yes (kept from a previous `agentmesh login`)")
	default:
		fmt.Println("  - api key:       no (pass --api-key directly to `tail`/`mcp register`)")
	}
	fmt.Printf("  - query API url: %s\n", loginQueryAPIURL)
	return nil
}

// resolveProjectAPIKey figures out whether `agentmesh login` can obtain a
// usable API key for the logged-in user: if they own no projects yet, it
// creates one (the only way to get a raw key back from the Query API).
// Otherwise it lists what they do own, explains why an existing
// project's key can't be recovered here, and offers to create a new
// project instead. Returns an empty string (not an error) if the user
// declines — "logged in, no new key obtained" is a valid outcome.
func resolveProjectAPIKey(stdin *bufio.Reader, queryAPIURL, sessionToken string, projects []authclient.ProjectSummary) (string, error) {
	if len(projects) == 0 {
		fmt.Println("You don't own any projects yet.")
		return createProjectInteractive(stdin, queryAPIURL, sessionToken)
	}

	fmt.Println("Existing project(s) on this account:")
	for _, p := range projects {
		fmt.Printf("  - %s (id=%s, key prefix %s...)\n", p.Name, p.ID, p.APIKeyPrefix)
	}
	fmt.Println()
	fmt.Println("AgentMesh only ever shows a project's raw API key once, at creation")
	fmt.Println("time — it is not stored anywhere recoverable, so `agentmesh login`")
	fmt.Println("cannot fetch an existing project's key for you. If you still have")
	fmt.Println("one of the keys above saved somewhere, pass it directly:")
	fmt.Println("`agentmesh tail --api-key <key> ...` or")
	fmt.Println("`agentmesh mcp register ... --api-key <key>`.")

	create, err := promptYesNo(stdin, "Create a new project now to get a fresh key? [y/N]: ")
	if err != nil {
		return "", fmt.Errorf("reading answer: %w", err)
	}
	if !create {
		return "", nil
	}
	return createProjectInteractive(stdin, queryAPIURL, sessionToken)
}

// createProjectInteractive prompts for an optional project name and
// creates it, returning the fresh raw API key the Query API hands back
// exactly once.
func createProjectInteractive(stdin *bufio.Reader, queryAPIURL, sessionToken string) (string, error) {
	name, err := promptLine(stdin, "New project name (leave blank for a default name): ")
	if err != nil {
		return "", fmt.Errorf("reading project name: %w", err)
	}

	projectID, apiKey, err := authclient.CreateProject(queryAPIURL, sessionToken, name)
	if err != nil {
		return "", fmt.Errorf("creating project: %w", err)
	}
	if name == "" {
		fmt.Printf("Created a new project (id=%s) with the default name.\n", projectID)
	} else {
		fmt.Printf("Created project %q (id=%s).\n", name, projectID)
	}
	return apiKey, nil
}

// promptLine writes prompt to stdout with no trailing newline, then
// reads and trims one line from stdin. A final line with no trailing
// newline (EOF right after content) is still returned rather than
// treated as an error.
func promptLine(stdin *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptPassword writes prompt to stdout, then reads a line from the
// terminal with input echo disabled via golang.org/x/term so the
// password never appears on screen or in terminal scrollback.
func promptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// promptYesNo asks a yes/no question, defaulting to "no" on a blank
// answer — matching the "[y/N]" convention in every prompt string
// callers pass in.
func promptYesNo(stdin *bufio.Reader, prompt string) (bool, error) {
	answer, err := promptLine(stdin, prompt)
	if err != nil {
		return false, err
	}
	answer = strings.ToLower(answer)
	return answer == "y" || answer == "yes", nil
}

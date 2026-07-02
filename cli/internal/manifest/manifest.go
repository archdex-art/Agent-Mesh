// Package manifest defines the YAML shape a customer writes to describe
// an MCP server they intend to register with AgentMesh's Registry
// (System Design.md §2.2's `mcp_servers(id, project_id, name,
// upstream_url, transport, version, owner, manifest_yaml)` control-plane
// row), and validates it before registration.
//
// This package is intentionally built ahead of the MCP Gateway/Registry
// itself (Milestone 6) per Milestones.md's Milestone 5 sequencing note:
// "agentmesh mcp validate — manifest linter (ahead of the Gateway
// itself, so the validation logic can be reused by the Gateway's
// registration flow in Milestone 6)". `agentmesh mcp register` (which
// actually talks to the Registry over the network) is explicitly out of
// scope until that milestone; Validate here only ever reads a local
// file.
package manifest

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// Transport is how the Gateway reaches an MCP server, per Architecture.md
// §5: "stdio for local servers ..., and Streamable HTTP for remote
// servers."
type Transport string

const (
	TransportStdio          Transport = "stdio"
	TransportStreamableHTTP Transport = "streamable-http"
)

// Manifest is the parsed shape of a server's registration YAML.
type Manifest struct {
	Name        string    `yaml:"name"`
	UpstreamURL string    `yaml:"upstream_url"`
	Transport   Transport `yaml:"transport"`
	Version     string    `yaml:"version"`
	Owner       string    `yaml:"owner"`

	// Auth is optional at lint time (a missing block is a Warning, not
	// an Error — Architecture.md §5's MCP-governance value proposition
	// is precisely that AgentMesh adds auth in front of servers that
	// don't have their own, so "no auth configured" is a legitimate,
	// if risky, registration).
	Auth *AuthConfig `yaml:"auth,omitempty"`
}

// AuthConfig describes how the Gateway should authenticate callers for
// this server, per Architecture.md §13's "Gateway implements OAuth 2.1."
type AuthConfig struct {
	Type string `yaml:"type"` // e.g. "oauth2.1", "none" (explicit opt-out)
}

// Severity distinguishes a hard validation failure (the manifest cannot
// be registered as-is) from an advisory finding (it can, but a reviewer
// should know).
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one lint result.
type Finding struct {
	Severity Severity
	Field    string
	Message  string
}

func (f Finding) String() string {
	return fmt.Sprintf("[%s] %s: %s", strings.ToUpper(string(f.Severity)), f.Field, f.Message)
}

// Result is the outcome of Validate: the parsed Manifest (if parsing
// succeeded) plus every Finding. HasErrors reports whether any Finding
// is SeverityError — the CLI's exit-code decision point.
type Result struct {
	Manifest *Manifest
	Findings []Finding
}

func (r Result) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Parse decodes raw YAML bytes into a Manifest without validating it —
// exposed separately from Validate so callers (e.g. a future `mcp
// register` command) can parse once and both validate and use the
// result, matching the module docstring's "reused by the Gateway's
// registration flow" goal.
func Parse(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest YAML: %w", err)
	}
	return &m, nil
}

// Validate parses raw and lints it against the required-field and
// auth-config checks Milestone 5's plan calls for ("checks required
// fields, warns on missing auth config"). Validate never returns a Go
// error for a structurally-valid-but-semantically-wrong manifest (e.g. a
// missing name) — that is reported as a SeverityError Finding instead,
// so the CLI can print every problem in one pass rather than stopping at
// the first one. A Go error is reserved for raw being unparseable YAML
// at all.
func Validate(raw []byte) (Result, error) {
	m, err := Parse(raw)
	if err != nil {
		return Result{}, err
	}

	var findings []Finding

	if strings.TrimSpace(m.Name) == "" {
		findings = append(findings, Finding{SeverityError, "name", "required, but missing or empty"})
	}

	if strings.TrimSpace(m.UpstreamURL) == "" {
		findings = append(findings, Finding{SeverityError, "upstream_url", "required, but missing or empty"})
	} else if _, err := url.ParseRequestURI(m.UpstreamURL); err != nil {
		findings = append(findings, Finding{SeverityError, "upstream_url", fmt.Sprintf("not a valid URL: %v", err)})
	}

	switch m.Transport {
	case TransportStdio, TransportStreamableHTTP:
		// valid
	case "":
		findings = append(findings, Finding{SeverityError, "transport", "required: must be \"stdio\" or \"streamable-http\""})
	default:
		findings = append(findings, Finding{SeverityError, "transport",
			fmt.Sprintf("unrecognized value %q: must be \"stdio\" or \"streamable-http\"", m.Transport)})
	}

	if strings.TrimSpace(m.Version) == "" {
		findings = append(findings, Finding{SeverityError, "version", "required, but missing or empty"})
	}

	if strings.TrimSpace(m.Owner) == "" {
		findings = append(findings, Finding{SeverityError, "owner", "required, but missing or empty (who is accountable for this server?)"})
	}

	if m.Auth == nil {
		findings = append(findings, Finding{SeverityWarning, "auth",
			"no auth block configured — the Gateway will forward calls to this server unauthenticated; set auth.type explicitly (even \"none\") to acknowledge this"})
	} else if strings.TrimSpace(m.Auth.Type) == "" {
		findings = append(findings, Finding{SeverityError, "auth.type", "auth block present but type is empty"})
	}

	return Result{Manifest: m, Findings: findings}, nil
}

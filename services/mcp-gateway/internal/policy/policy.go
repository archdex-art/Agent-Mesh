// Package policy implements the MCP Gateway's declarative guardrail
// engine, per docs/plan/MCP_Gateway_Architecture.md §3.3.
//
// Policies are YAML documents evaluated against an incoming MCP JSON-RPC
// "tools/call" request. Each policy targets a set of tool names and
// applies regex rules against the tool's arguments; a match triggers the
// policy's configured action.
//
// v1 scope is deliberately narrow (tool-name targeting + regex parameter
// matching) — Milestones.md's Milestone 6 explicitly defers WASM-sandboxed
// custom policies to a later Innovative-tier feature; this package covers
// exactly the "regex-based rule evaluator" step named in the architecture
// doc's implementation steps.
package policy

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Action is what the engine does when a policy's rules match.
type Action string

const (
	// ActionDeny blocks the request; the proxy returns a JSON-RPC error
	// instead of forwarding to the upstream MCP server.
	ActionDeny Action = "deny"
	// ActionAllow is the implicit default for any request that matches no
	// policy — included as an explicit value so a policy author can also
	// write an "allow" override rule ahead of a broader "deny" rule.
	ActionAllow Action = "allow"
)

// ParamRule matches a single named parameter's string value against a
// regular expression.
type ParamRule struct {
	Param   string `yaml:"param"`
	Pattern string `yaml:"pattern"`

	compiled *regexp.Regexp
}

// Rule is one condition within a Policy. Exactly one matcher kind is set
// per rule (currently only ParamMatches exists; more matcher kinds are
// additive, not breaking, per the DSL's forward-compatible shape).
type Rule struct {
	ParamMatches *ParamRule `yaml:"param_matches,omitempty"`
}

// Policy is a single named guardrail: which tools it applies to, what
// happens on a match, and the rules that constitute a match.
type Policy struct {
	Name        string   `yaml:"name"`
	TargetTools []string `yaml:"target_tools"`
	Action      Action   `yaml:"action"`
	Rules       []Rule   `yaml:"rules"`
}

// RateLimit is an OPTIONAL, additive extension to Document (Milestone 6's
// per-server rate limiting requirement, docs/plan/Architecture.md §13):
// a Redis-backed fixed-window cap the Gateway's router applies per
// (mcp_server_id, caller) pair (services/mcp-gateway/internal/ratelimit).
// It is a sibling of Policies, never nested inside a Policy, so an
// existing "policies: only" document keeps parsing byte-for-byte
// identically — this field simply stays nil when the YAML omits it.
type RateLimit struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
}

// Document is the top-level YAML shape: a list of policies plus the
// optional rate_limit extension above.
type Document struct {
	Policies  []Policy   `yaml:"policies"`
	RateLimit *RateLimit `yaml:"rate_limit,omitempty"`
}

// Engine evaluates ToolCall requests against a compiled set of policies.
// It also retains the document's optional RateLimit so callers that only
// hold an *Engine (not the original bytes/Document) can still read it —
// see the RateLimit accessor method below.
type Engine struct {
	policies  []Policy
	rateLimit *RateLimit
}

// ToolCall is the subset of an MCP "tools/call" request the engine needs:
// the tool name and its arguments (already parsed from JSON into a
// generic map, since argument shapes are tool-defined and not known ahead
// of time).
type ToolCall struct {
	Name      string
	Arguments map[string]any
}

// Load parses a Document from YAML bytes and compiles every rule's regex
// up front, so a malformed pattern is a load-time error, not a
// discovered-at-first-match runtime panic.
func Load(data []byte) (*Engine, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("policy: parsing yaml: %w", err)
	}

	for i := range doc.Policies {
		p := &doc.Policies[i]
		if p.Action == "" {
			p.Action = ActionDeny // a policy with rules but no explicit action defaults to the safer choice
		}
		for j := range p.Rules {
			r := &p.Rules[j]
			if r.ParamMatches == nil {
				continue
			}
			compiled, err := regexp.Compile(r.ParamMatches.Pattern)
			if err != nil {
				return nil, fmt.Errorf("policy: compiling pattern %q in policy %q: %w", r.ParamMatches.Pattern, p.Name, err)
			}
			r.ParamMatches.compiled = compiled
		}
	}

	return &Engine{policies: doc.Policies, rateLimit: doc.RateLimit}, nil
}

// RateLimit returns the document's optional rate_limit configuration, or
// nil if the document never specified one — the router treats nil as
// "no rate limiting configured for this server," matching this
// package's existing fail-open default for an empty guardrail policy
// set (Load([]byte("policies: []"))).
func (e *Engine) RateLimit() *RateLimit {
	return e.rateLimit
}

// LoadFile reads and loads a policy document from disk.
func LoadFile(path string) (*Engine, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy: reading %s: %w", path, err)
	}
	return Load(data)
}

// Evaluate checks call against every loaded policy that targets its tool
// name. It returns a non-nil error (naming the violated policy) on the
// first ActionDeny match; a nil return means the call is allowed.
func (e *Engine) Evaluate(call ToolCall) error {
	for _, p := range e.policies {
		if !targetsTool(p.TargetTools, call.Name) {
			continue
		}
		if !anyRuleMatches(p.Rules, call) {
			continue
		}
		if p.Action == ActionDeny {
			return fmt.Errorf("policy %q denied tool %q", p.Name, call.Name)
		}
	}
	return nil
}

func targetsTool(targets []string, name string) bool {
	for _, t := range targets {
		if t == name {
			return true
		}
	}
	return false
}

func anyRuleMatches(rules []Rule, call ToolCall) bool {
	for _, r := range rules {
		if r.ParamMatches == nil {
			continue
		}
		val, ok := call.Arguments[r.ParamMatches.Param]
		if !ok {
			continue
		}
		strVal, ok := val.(string)
		if !ok {
			continue
		}
		if r.ParamMatches.compiled.MatchString(strVal) {
			return true
		}
	}
	return false
}

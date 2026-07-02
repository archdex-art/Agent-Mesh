package policy

import (
	"os"
	"strings"
	"testing"
)

// destructiveSQLPolicy mirrors the exact example from
// docs/plan/MCP_Gateway_Architecture.md §3.3.
const destructiveSQLPolicy = `
policies:
  - name: prevent_destructive_sql
    target_tools: ["execute_query", "run_sql"]
    action: deny
    rules:
      - param_matches:
          param: "sql"
          pattern: "(?i)(DROP|DELETE|TRUNCATE|ALTER)"
`

func TestEngineBlocksDestructiveSQL(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = engine.Evaluate(ToolCall{
		Name:      "execute_query",
		Arguments: map[string]any{"sql": "DROP TABLE users;"},
	})
	if err == nil {
		t.Fatal("Evaluate() succeeded for a DROP TABLE query, want denial")
	}
	if !strings.Contains(err.Error(), "prevent_destructive_sql") {
		t.Fatalf("error does not name the violated policy: %v", err)
	}
}

func TestEngineAllowsSafeSQL(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = engine.Evaluate(ToolCall{
		Name:      "execute_query",
		Arguments: map[string]any{"sql": "SELECT * FROM users;"},
	})
	if err != nil {
		t.Fatalf("Evaluate() denied a safe SELECT query: %v", err)
	}
}

func TestEngineCaseInsensitiveMatch(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = engine.Evaluate(ToolCall{
		Name:      "run_sql",
		Arguments: map[string]any{"sql": "drop table sessions"},
	})
	if err == nil {
		t.Fatal("Evaluate() succeeded for lowercase 'drop', want denial (pattern is case-insensitive)")
	}
}

func TestEngineIgnoresUntargetedTool(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// web_search is not in target_tools, so even a "DROP"-looking argument
	// must pass through untouched.
	err = engine.Evaluate(ToolCall{
		Name:      "web_search",
		Arguments: map[string]any{"sql": "DROP TABLE users;"},
	})
	if err != nil {
		t.Fatalf("Evaluate() denied a call to an untargeted tool: %v", err)
	}
}

func TestEngineIgnoresMissingParam(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	err = engine.Evaluate(ToolCall{
		Name:      "execute_query",
		Arguments: map[string]any{"other_param": "DROP TABLE users;"},
	})
	if err != nil {
		t.Fatalf("Evaluate() denied a call where the targeted param is absent: %v", err)
	}
}

func TestEngineIgnoresNonStringParam(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// A non-string value for the targeted param must not panic or match.
	err = engine.Evaluate(ToolCall{
		Name:      "execute_query",
		Arguments: map[string]any{"sql": 12345},
	})
	if err != nil {
		t.Fatalf("Evaluate() denied a call with a non-string param value: %v", err)
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	_, err := Load([]byte("not: valid: yaml: at: all: ["))
	if err == nil {
		t.Fatal("Load() succeeded on malformed YAML, want error")
	}
}

func TestLoadRejectsInvalidRegex(t *testing.T) {
	badPolicy := `
policies:
  - name: broken
    target_tools: ["x"]
    rules:
      - param_matches:
          param: "y"
          pattern: "(unterminated["
`
	_, err := Load([]byte(badPolicy))
	if err == nil {
		t.Fatal("Load() succeeded with an invalid regex pattern, want error")
	}
}

func TestPolicyDefaultsToActionDenyWhenUnspecified(t *testing.T) {
	noActionPolicy := `
policies:
  - name: implicit_deny
    target_tools: ["dangerous_tool"]
    rules:
      - param_matches:
          param: "arg"
          pattern: ".*"
`
	engine, err := Load([]byte(noActionPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = engine.Evaluate(ToolCall{
		Name:      "dangerous_tool",
		Arguments: map[string]any{"arg": "anything"},
	})
	if err == nil {
		t.Fatal("Evaluate() allowed a call under a policy with no explicit action, want the safer implicit-deny default")
	}
}

func TestEngineWithNoMatchingRulesAllows(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	err = engine.Evaluate(ToolCall{Name: "execute_query", Arguments: map[string]any{"sql": "UPDATE users SET name = 'x'"}})
	if err != nil {
		t.Fatalf("Evaluate() denied a non-matching (UPDATE) query: %v", err)
	}
}

func TestLoadFileReadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/policy.yaml"
	if err := os.WriteFile(path, []byte(destructiveSQLPolicy), 0o644); err != nil {
		t.Fatalf("writing temp policy file: %v", err)
	}

	engine, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	err = engine.Evaluate(ToolCall{Name: "execute_query", Arguments: map[string]any{"sql": "DROP TABLE x"}})
	if err == nil {
		t.Fatal("Evaluate() via LoadFile-loaded engine did not deny a destructive query")
	}
}

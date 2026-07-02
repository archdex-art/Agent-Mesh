package policy

import "testing"

// TestEngineRateLimitIsNilWhenDocumentOmitsIt pins the fail-open default:
// a "policies: only" document (every pre-M6 policy file on disk) must
// still compile, and Engine.RateLimit() must report "unconfigured" so the
// router applies no rate limiting rather than misreading a zero value as
// "0 requests per minute."
func TestEngineRateLimitIsNilWhenDocumentOmitsIt(t *testing.T) {
	engine, err := Load([]byte(destructiveSQLPolicy))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rl := engine.RateLimit(); rl != nil {
		t.Fatalf("RateLimit() = %+v, want nil for a document with no rate_limit section", rl)
	}
}

// TestEngineRateLimitParsesSiblingField confirms rate_limit is read as a
// document-level sibling of policies (not nested inside a Policy entry)
// and survives Load into the compiled Engine.
func TestEngineRateLimitParsesSiblingField(t *testing.T) {
	doc := `
policies:
  - name: prevent_destructive_sql
    target_tools: ["execute_query"]
    action: deny
    rules:
      - param_matches: {param: "sql", pattern: "(?i)(DROP|DELETE)"}
rate_limit:
  requests_per_minute: 30
`
	engine, err := Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rl := engine.RateLimit()
	if rl == nil {
		t.Fatal("RateLimit() = nil, want a populated *RateLimit")
	}
	if rl.RequestsPerMinute != 30 {
		t.Fatalf("RequestsPerMinute = %d, want 30", rl.RequestsPerMinute)
	}

	// The policies list must be entirely unaffected by the sibling
	// rate_limit field's presence — this is the additive-change
	// guarantee the whole exercise depends on.
	if err := engine.Evaluate(ToolCall{Name: "execute_query", Arguments: map[string]any{"sql": "DROP TABLE x"}}); err == nil {
		t.Fatal("Evaluate() allowed a DROP query even with rate_limit present, want denial")
	}
}

// TestEngineRateLimitAbsentWithoutAnyPolicies covers the Gateway's own
// empty-engine bootstrap document (policy.Load([]byte("policies: []"))
// in cmd/main.go) to guarantee that path still yields a nil RateLimit,
// not a panic or a zero-value *RateLimit.
func TestEngineRateLimitAbsentWithoutAnyPolicies(t *testing.T) {
	engine, err := Load([]byte("policies: []"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rl := engine.RateLimit(); rl != nil {
		t.Fatalf("RateLimit() = %+v, want nil", rl)
	}
}

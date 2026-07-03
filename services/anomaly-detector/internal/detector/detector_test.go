package detector

import (
	"context"
	"log/slog"
	"os"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

type mockDB struct {
	inserted bool
}

func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	m.inserted = true
	return pgconn.CommandTag{}, nil
}

func TestEvaluateRule_LoopDetected(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	db := &mockDB{}
	d := New(db, nil, logger)

	rule := AlertRule{
		ID:        "rule-1",
		ProjectID: "proj-1",
		Kind:      "loop_detected",
		Threshold: map[string]any{"max_repeats": float64(2)},
	}

	ctx := context.Background()
	projectID := "proj-1"

	ev1 := Event{TraceID: "trace-1", Kind: "tool.call", Name: "search"}
	
	d.evaluateRule(ctx, rule, projectID, ev1)
	
	key := "trace-1:rule-1"
	d.stateMu.Lock()
	tracker := d.loops[key]
	if tracker == nil || tracker.Count != 1 {
		t.Fatalf("expected count 1, got %v", tracker)
	}
	d.stateMu.Unlock()
	
	if db.inserted {
		t.Fatalf("should not have triggered alert yet")
	}

	d.evaluateRule(ctx, rule, projectID, ev1) // count 2
	d.evaluateRule(ctx, rule, projectID, ev1) // count 3 (triggers alert)
	
	d.stateMu.Lock()
	if d.loops[key].Count != 3 {
		t.Fatalf("expected count 3, got %d", d.loops[key].Count)
	}
	d.stateMu.Unlock()
	
	if !db.inserted {
		t.Fatalf("should have triggered alert")
	}
}

func TestEvaluateRule_GuardrailViolation(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	db := &mockDB{}
	d := New(db, nil, logger)

	rule := AlertRule{
		ID:        "rule-2",
		ProjectID: "proj-1",
		Kind:      "guardrail_violation",
	}

	ctx := context.Background()

	ev := Event{TraceID: "trace-1", Kind: "mcp.call", Status: "denied", Name: "fetch_data"}
	
	d.evaluateRule(ctx, rule, "proj-1", ev)

	if !db.inserted {
		t.Fatalf("expected guardrail violation to trigger alert")
	}
}

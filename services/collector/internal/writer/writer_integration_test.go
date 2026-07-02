//go:build integration

// Integration tests against a real ClickHouse instance. Run with:
//
//	go test -tags integration ./internal/writer/... -v
//
// Requires a ClickHouse instance reachable at AGENTMESH_TEST_CLICKHOUSE_ADDR
// (default localhost:9000) with schema/clickhouse/001_spans.sql already
// applied. This is deliberately a build-tagged, opt-in suite (not part of
// `go test ./...`) since it requires live infrastructure — Technical
// Roadmap.md §7 calls for testcontainers-driven integration tests for
// exactly this kind of database-touching logic; this file validates the
// writer against real ClickHouse semantics (column types, Nullable
// handling, FixedString sizing) that a pure unit test cannot catch.
package writer

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	"github.com/shopspring/decimal"
)

// testConn opens a connection to the ClickHouse instance under test and
// registers cleanup to close it, so every test in this file shares one
// connection-setup path instead of duplicating Open/Ping boilerplate.
func testConn(t *testing.T) driver.Conn {
	t.Helper()
	addr := os.Getenv("AGENTMESH_TEST_CLICKHOUSE_ADDR")
	if addr == "" {
		addr = "localhost:9000"
	}
	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{addr}})
	if err != nil {
		t.Fatalf("opening clickhouse connection: %v", err)
	}
	if err := conn.Ping(context.Background()); err != nil {
		t.Fatalf("pinging clickhouse: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func mustProjectID(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

func mustTraceID(t *testing.T) ids.TraceID {
	t.Helper()
	id, err := ids.NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID: %v", err)
	}
	return id
}

func mustSpanID(t *testing.T) ids.SpanID {
	t.Helper()
	id, err := ids.NewSpanID()
	if err != nil {
		t.Fatalf("NewSpanID: %v", err)
	}
	return id
}

func TestWriteBatchRoundTripsThroughRealClickHouse(t *testing.T) {
	conn := testConn(t)
	w := New(conn)
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	spanID := mustSpanID(t)
	tokenIn := uint32(10)
	tokenOut := uint32(5)
	cost := 0.0021
	start := time.Now().UTC().Truncate(time.Microsecond)
	end := start.Add(200 * time.Millisecond)

	s := span.Span{
		SchemaVersion: span.CurrentSchemaVersion,
		ProjectID:     projectID,
		TraceID:       traceID,
		SpanID:        spanID,
		Kind:          span.KindLLMCall,
		Name:          "gpt-4.1",
		StartTime:     start,
		EndTime:       end,
		Status:        span.StatusOK,
		Input:         span.Payload{Inline: `{"prompt":"hi"}`},
		Output:        span.Payload{Inline: `{"text":"hello"}`},
		TokenInput:    &tokenIn,
		TokenOutput:   &tokenOut,
		CostUSD:       &cost,
		Attributes:    map[string]string{"framework": "langgraph"},
	}

	if err := w.WriteBatch(context.Background(), []span.Span{s}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	row := conn.QueryRow(context.Background(),
		`SELECT project_id, trace_id, span_id, span_kind, name, status, token_input, token_output, cost_usd, attributes['framework']
		 FROM spans WHERE trace_id = ? AND span_id = ?`,
		traceID.String(), spanID.String())

	var gotProjectID, gotTraceID, gotSpanID, gotKind, gotName, gotStatus, gotFramework string
	var gotTokenIn, gotTokenOut uint32
	var gotCost decimal.Decimal
	if err := row.Scan(&gotProjectID, &gotTraceID, &gotSpanID, &gotKind, &gotName, &gotStatus, &gotTokenIn, &gotTokenOut, &gotCost, &gotFramework); err != nil {
		t.Fatalf("scanning row back from clickhouse: %v", err)
	}

	if gotProjectID != projectID.String() {
		t.Errorf("project_id = %q, want %q", gotProjectID, projectID.String())
	}
	if gotKind != string(span.KindLLMCall) {
		t.Errorf("span_kind = %q, want %q", gotKind, span.KindLLMCall)
	}
	if gotName != "gpt-4.1" {
		t.Errorf("name = %q, want %q", gotName, "gpt-4.1")
	}
	if gotStatus != string(span.StatusOK) {
		t.Errorf("status = %q, want %q", gotStatus, span.StatusOK)
	}
	if gotTokenIn != 10 || gotTokenOut != 5 {
		t.Errorf("tokens = (%d, %d), want (10, 5)", gotTokenIn, gotTokenOut)
	}
	if gotCostFloat, _ := gotCost.Float64(); gotCostFloat != 0.0021 {
		t.Errorf("cost_usd = %v, want 0.0021", gotCostFloat)
	}
	if gotFramework != "langgraph" {
		t.Errorf("attributes[framework] = %q, want %q", gotFramework, "langgraph")
	}
}

func TestWriteBatchEmptyIsNoOp(t *testing.T) {
	conn := testConn(t)
	w := New(conn)
	if err := w.WriteBatch(context.Background(), nil); err != nil {
		t.Fatalf("WriteBatch(nil) should be a no-op, got error: %v", err)
	}
}

func TestWriteBatchWithNullableFieldsOmitted(t *testing.T) {
	// A span with no end time, no status (in-flight), no tokens/cost must
	// round-trip with NULLs, not zero-values that would corrupt downstream
	// aggregation (System Design.md §7's "nil means unknown, not zero").
	conn := testConn(t)
	w := New(conn)
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	spanID := mustSpanID(t)

	s := span.Span{
		SchemaVersion: span.CurrentSchemaVersion,
		ProjectID:     projectID,
		TraceID:       traceID,
		SpanID:        spanID,
		Kind:          span.KindToolCall,
		Name:          "search",
		StartTime:     time.Now().UTC(),
	}

	if err := w.WriteBatch(context.Background(), []span.Span{s}); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	row := conn.QueryRow(context.Background(),
		`SELECT isNull(end_time), isNull(status), isNull(cost_usd) FROM spans WHERE trace_id = ? AND span_id = ?`,
		traceID.String(), spanID.String())

	var endIsNull, statusIsNull, costIsNull bool
	if err := row.Scan(&endIsNull, &statusIsNull, &costIsNull); err != nil {
		t.Fatalf("scanning row: %v", err)
	}
	if !endIsNull || !statusIsNull || !costIsNull {
		t.Fatalf("expected NULLs for unset fields, got end_time_null=%v status_null=%v cost_null=%v", endIsNull, statusIsNull, costIsNull)
	}
}

func TestWriteBatchMultipleSpansSameTrace(t *testing.T) {
	// Verifies the batching path itself (multiple Append calls before one
	// Send) rather than a single-row insert, per System Design.md §3's
	// non-negotiable batching requirement.
	conn := testConn(t)
	w := New(conn)
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)

	spans := make([]span.Span, 0, 5)
	for range 5 {
		spans = append(spans, span.Span{
			SchemaVersion: span.CurrentSchemaVersion,
			ProjectID:     projectID,
			TraceID:       traceID,
			SpanID:        mustSpanID(t),
			Kind:          span.KindToolCall,
			Name:          "step",
			StartTime:     time.Now().UTC(),
		})
	}

	if err := w.WriteBatch(context.Background(), spans); err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}

	row := conn.QueryRow(context.Background(), `SELECT count() FROM spans WHERE trace_id = ?`, traceID.String())
	var count uint64
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scanning count: %v", err)
	}
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}
}

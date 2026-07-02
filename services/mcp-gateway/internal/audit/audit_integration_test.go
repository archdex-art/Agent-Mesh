//go:build integration

// Run with: go test -tags integration ./internal/audit/... -v
// Requires a live Collector reachable at AGENTMESH_TEST_COLLECTOR_ADDR
// (default localhost:4317) and a ClickHouse instance at
// AGENTMESH_TEST_CLICKHOUSE_ADDR (default localhost:9000) with
// schema/clickhouse/001_spans.sql applied, matching the rigor established
// in Milestone 2's writer_integration_test.go — this proves the Gateway's
// hand-built OTLP spans are actually accepted and decoded correctly by the
// real Collector, not just well-formed according to our own assumptions.
package audit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

func TestEmitLandsRealSpanInClickHouseViaLiveCollector(t *testing.T) {
	collectorAddr := os.Getenv("AGENTMESH_TEST_COLLECTOR_ADDR")
	if collectorAddr == "" {
		collectorAddr = "localhost:4317"
	}
	clickhouseAddr := os.Getenv("AGENTMESH_TEST_CLICKHOUSE_ADDR")
	if clickhouseAddr == "" {
		clickhouseAddr = "localhost:9000"
	}
	apiKey := os.Getenv("AGENTMESH_TEST_API_KEY")
	if apiKey == "" {
		apiKey = "am_live_mcpgatewaytest1234567890"
	}
	projectIDStr := os.Getenv("AGENTMESH_TEST_PROJECT_ID")
	if projectIDStr == "" {
		projectIDStr = "11111111-1111-7111-8111-111111111111"
	}
	projectID, err := ids.ParseProjectID(projectIDStr)
	if err != nil {
		t.Fatalf("parsing test project id: %v", err)
	}

	emitter, err := NewEmitter(collectorAddr, apiKey)
	if err != nil {
		t.Fatalf("NewEmitter: %v", err)
	}
	defer emitter.Close()

	start := time.Now().UTC()
	end := start.Add(50 * time.Millisecond)

	call := Call{
		ProjectID:  projectID,
		ToolName:   "execute_query",
		Status:     span.StatusDenied,
		DenyReason: `policy "prevent_destructive_sql" denied tool "execute_query"`,
		StartTime:  start,
		EndTime:    end,
	}

	if err := emitter.Emit(context.Background(), call); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Give the Collector's batch writer a moment to flush.
	time.Sleep(500 * time.Millisecond)

	chConn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{clickhouseAddr},
		Auth: clickhouse.Auth{Username: "default", Password: "agentmesh"},
	})
	if err != nil {
		t.Fatalf("opening clickhouse connection: %v", err)
	}
	defer chConn.Close()

	row := chConn.QueryRow(context.Background(),
		`SELECT span_kind, name, status, attributes['deny_reason']
		 FROM spans
		 WHERE project_id = ? AND span_kind = 'mcp.call' AND name = ?
		 ORDER BY start_time DESC LIMIT 1`,
		projectID.String(), "execute_query",
	)

	var gotKind, gotName, gotStatus, gotReason string
	if err := row.Scan(&gotKind, &gotName, &gotStatus, &gotReason); err != nil {
		t.Fatalf("scanning row back from clickhouse (span may not have landed): %v", err)
	}

	if gotKind != string(span.KindMCPCall) {
		t.Errorf("span_kind = %q, want %q", gotKind, span.KindMCPCall)
	}
	if gotName != "execute_query" {
		t.Errorf("name = %q, want %q", gotName, "execute_query")
	}
	if gotStatus != string(span.StatusDenied) {
		t.Errorf("status = %q, want %q", gotStatus, span.StatusDenied)
	}
	if gotReason == "" {
		t.Error("agentmesh.deny_reason attribute is empty, want the policy denial message")
	}
}

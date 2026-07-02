// Package writer implements the Collector's ClickHouse persistence path:
// batch-inserting decoded spans into the `spans` table defined in
// schema/clickhouse/001_spans.sql.
//
// System Design.md §3 requires batching on the Collector side ("ClickHouse
// strongly prefers batch inserts over row-by-row writes") as a
// "deliberate, non-negotiable design constraint" — this package's Writer
// buffers spans and flushes them as a single ClickHouse Batch, never issuing
// one INSERT per span.
package writer

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/span"
	"github.com/shopspring/decimal"
)

// insertQuery's column list must match schema/clickhouse/001_spans.sql and
// schema/clickhouse/002_chaos_columns.sql exactly, in the same order
// appendSpan below calls batch.Append — this list is kept adjacent to
// appendSpan (not column-name bound) so a drift between the two is a
// same-file, single fix.
const insertQuery = `
INSERT INTO spans (
	schema_version, project_id, trace_id, span_id, parent_span_id,
	span_kind, name, start_time, end_time, status,
	input_inline, output_inline, input_blob_ref, output_blob_ref,
	token_input, token_output, cost_usd, attributes,
	chaos_injected, chaos_fault_type
)`

// Writer batch-inserts shared/span.Span values into ClickHouse.
type Writer struct {
	conn driver.Conn
}

// New returns a Writer backed by the given ClickHouse connection. The
// connection's lifecycle (Open/Close) is owned by the caller.
func New(conn driver.Conn) *Writer {
	return &Writer{conn: conn}
}

// WriteBatch inserts every span in spans as a single ClickHouse batch. An
// empty spans slice is a no-op, not an error — callers on a timer-based
// flush loop will routinely call this with nothing buffered.
func (w *Writer) WriteBatch(ctx context.Context, spans []span.Span) error {
	if len(spans) == 0 {
		return nil
	}

	batch, err := w.conn.PrepareBatch(ctx, insertQuery)
	if err != nil {
		return amerrors.Wrap(amerrors.CodeUnavailable, "preparing clickhouse batch", err)
	}
	defer batch.Close() //nolint:errcheck // Close after Send is a documented no-op; the error path is Send's.

	for _, s := range spans {
		if err := appendSpan(batch, s); err != nil {
			return amerrors.Wrap(amerrors.CodeInternal, fmt.Sprintf("appending span %s to batch", s.SpanID), err)
		}
	}

	if err := batch.Send(); err != nil {
		// A failed Send is retryable (System Design.md §3 / Architecture.md
		// §17's ingestion-path philosophy: "a Collector outage degrades to
		// traces delayed, never to agent crashes") — the caller's buffering
		// layer is expected to retry a CodeUnavailable error.
		return amerrors.Wrap(amerrors.CodeUnavailable, "sending clickhouse batch", err)
	}
	return nil
}

func appendSpan(batch driver.Batch, s span.Span) error {
	var parentSpanID *string
	if s.HasParent() {
		id := s.ParentSpanID.String()
		parentSpanID = &id
	}

	var endTime *time.Time
	if !s.EndTime.IsZero() {
		endTime = &s.EndTime
	}

	var status *string
	if s.Status != "" {
		st := string(s.Status)
		status = &st
	}

	var inputInline, outputInline, inputBlobRef, outputBlobRef *string
	if s.Input.Inline != "" {
		inputInline = &s.Input.Inline
	}
	if s.Input.BlobRef != "" {
		inputBlobRef = &s.Input.BlobRef
	}
	if s.Output.Inline != "" {
		outputInline = &s.Output.Inline
	}
	if s.Output.BlobRef != "" {
		outputBlobRef = &s.Output.BlobRef
	}

	// cost_usd is a ClickHouse Decimal(12,6) column; clickhouse-go requires
	// shopspring/decimal.Decimal for Decimal-typed columns, not a raw
	// float64 — verified against a live ClickHouse instance, which rejected
	// a *float64 with "converting *float64 to Decimal(12, 6) is unsupported"
	// before this conversion was added.
	var cost *decimal.Decimal
	if s.CostUSD != nil {
		d := decimal.NewFromFloat(*s.CostUSD)
		cost = &d
	}

	var chaosFaultType *string
	if s.ChaosFaultType != "" {
		chaosFaultType = &s.ChaosFaultType
	}

	return batch.Append(
		uint16(s.SchemaVersion),
		s.ProjectID.String(),
		s.TraceID.String(),
		s.SpanID.String(),
		parentSpanID,
		string(s.Kind),
		s.Name,
		s.StartTime,
		endTime,
		status,
		inputInline,
		outputInline,
		inputBlobRef,
		outputBlobRef,
		s.TokenInput,
		s.TokenOutput,
		cost,
		s.Attributes,
		s.ChaosInjected,
		chaosFaultType,
	)
}

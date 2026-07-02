// Package store implements the Query API's read path against ClickHouse:
// the concrete rest.TraceReader used in production (cmd/main.go wires it
// in; unit tests for the HTTP layer use a fake instead, per
// internal/rest/traces.go's stated testability rationale).
package store

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/agentmesh/agentmesh/services/query-api/internal/rest"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	"github.com/shopspring/decimal"
)

// ClickHouseReader implements rest.TraceReader against a live ClickHouse
// connection.
type ClickHouseReader struct {
	conn driver.Conn
}

// NewClickHouseReader returns a ClickHouseReader backed by conn. The
// connection's lifecycle is owned by the caller.
func NewClickHouseReader(conn driver.Conn) *ClickHouseReader {
	return &ClickHouseReader{conn: conn}
}

// ListTraceSummaries reads from the trace_rollups materialized view
// (schema/clickhouse/001_spans.sql), per System Design.md §5's requirement
// that the trace-list query never scan raw spans.
func (r *ClickHouseReader) ListTraceSummaries(ctx context.Context, projectID ids.ProjectID, limit int) ([]rest.TraceSummary, error) {
	rows, err := r.conn.Query(ctx, `
		SELECT
			trace_id,
			countMerge(span_count) AS span_count,
			sumMerge(error_span_count) AS error_span_count,
			sumMerge(total_cost_usd) AS total_cost_usd,
			sumMerge(total_token_input) AS total_token_input,
			sumMerge(total_token_output) AS total_token_output
		FROM trace_rollups
		WHERE project_id = ?
		GROUP BY trace_id
		ORDER BY trace_id DESC
		LIMIT ?`,
		projectID.String(), limit,
	)
	if err != nil {
		return nil, amerrors.Wrap(amerrors.CodeUnavailable, "querying trace_rollups", err)
	}
	defer rows.Close()

	summaries := make([]rest.TraceSummary, 0)
	for rows.Next() {
		var s rest.TraceSummary
		var cost decimal.Decimal
		if err := rows.Scan(&s.TraceID, &s.SpanCount, &s.ErrorSpans, &cost, &s.TotalTokensIn, &s.TotalTokensOut); err != nil {
			return nil, amerrors.Wrap(amerrors.CodeInternal, "scanning trace_rollups row", err)
		}
		s.TotalCostUSD, _ = cost.Float64()
		summaries = append(summaries, s)
	}
	if err := rows.Err(); err != nil {
		return nil, amerrors.Wrap(amerrors.CodeUnavailable, "iterating trace_rollups rows", err)
	}
	return summaries, nil
}

// GetTraceSpans reads every span for a single trace, ordered by start_time
// so callers receive the DAG in execution order (Architecture.md §2: "fetch
// a trace DAG").
func (r *ClickHouseReader) GetTraceSpans(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]span.Span, error) {
	rows, err := r.conn.Query(ctx, `
		SELECT
			schema_version, project_id, trace_id, span_id, parent_span_id,
			span_kind, name, start_time, end_time, status,
			input_inline, output_inline, input_blob_ref, output_blob_ref,
			token_input, token_output, cost_usd, attributes
		FROM spans
		WHERE project_id = ? AND trace_id = ?
		ORDER BY start_time ASC`,
		projectID.String(), traceID.String(),
	)
	if err != nil {
		return nil, amerrors.Wrap(amerrors.CodeUnavailable, "querying spans", err)
	}
	defer rows.Close()

	var spans []span.Span
	for rows.Next() {
		s, err := scanSpanRow(rows)
		if err != nil {
			return nil, err
		}
		spans = append(spans, s)
	}
	if err := rows.Err(); err != nil {
		return nil, amerrors.Wrap(amerrors.CodeUnavailable, "iterating spans rows", err)
	}
	return spans, nil
}

// scanSpanRow scans one row from the `spans` query above into a
// shared/span.Span, in the exact column order that query selects — kept
// adjacent to GetTraceSpans's SELECT list so a drift between the two is a
// same-file, single fix, mirroring writer.go's insertQuery/appendSpan
// pairing convention.
func scanSpanRow(rows driver.Rows) (span.Span, error) {
	var (
		schemaVersion                       uint16
		projectIDStr, traceIDStr, spanIDStr string
		parentSpanIDStr                     *string
		kindStr                             string
		name                                string
		startTime                           time.Time
		endTime                             *time.Time
		statusStr                           *string
		inputInline, outputInline           *string
		inputBlobRef, outputBlobRef         *string
		tokenInput, tokenOutput             *uint32
		cost                                *decimal.Decimal
		attributes                          map[string]string
	)

	if err := rows.Scan(
		&schemaVersion, &projectIDStr, &traceIDStr, &spanIDStr, &parentSpanIDStr,
		&kindStr, &name, &startTime, &endTime, &statusStr,
		&inputInline, &outputInline, &inputBlobRef, &outputBlobRef,
		&tokenInput, &tokenOutput, &cost, &attributes,
	); err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInternal, "scanning spans row", err)
	}

	projectID, err := ids.ParseProjectID(projectIDStr)
	if err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInternal, "parsing project_id from spans row", err)
	}
	traceID, err := ids.ParseTraceID(traceIDStr)
	if err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInternal, "parsing trace_id from spans row", err)
	}
	spanID, err := ids.ParseSpanID(spanIDStr)
	if err != nil {
		return span.Span{}, amerrors.Wrap(amerrors.CodeInternal, "parsing span_id from spans row", err)
	}

	s := span.Span{
		SchemaVersion: int(schemaVersion),
		ProjectID:     projectID,
		TraceID:       traceID,
		SpanID:        spanID,
		Kind:          span.Kind(kindStr),
		Name:          name,
		StartTime:     startTime,
		Attributes:    attributes,
	}

	if parentSpanIDStr != nil {
		parentSpanID, err := ids.ParseSpanID(*parentSpanIDStr)
		if err != nil {
			return span.Span{}, amerrors.Wrap(amerrors.CodeInternal, "parsing parent_span_id from spans row", err)
		}
		s.ParentSpanID = parentSpanID
	}
	if endTime != nil {
		s.EndTime = *endTime
	}
	if statusStr != nil {
		s.Status = span.Status(*statusStr)
	}
	if inputInline != nil {
		s.Input.Inline = *inputInline
	}
	if inputBlobRef != nil {
		s.Input.BlobRef = *inputBlobRef
	}
	if outputInline != nil {
		s.Output.Inline = *outputInline
	}
	if outputBlobRef != nil {
		s.Output.BlobRef = *outputBlobRef
	}
	s.TokenInput = tokenInput
	s.TokenOutput = tokenOutput
	if cost != nil {
		f, _ := cost.Float64()
		s.CostUSD = &f
	}

	return s, nil
}

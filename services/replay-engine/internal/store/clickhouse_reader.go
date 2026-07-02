// Package store implements the Replay Engine's two storage dependencies:
// reading a trace's ordered span list from ClickHouse (the same table the
// Query API reads, per Architecture.md §2's "reads Trace Store"
// responsibility) and CRUD on the `replay_runs` control-plane table
// (schema/postgres/002_replay_runs.sql).
//
// This package duplicates, rather than imports, the Query API's
// structurally-identical ClickHouse reader
// (services/query-api/internal/store/clickhouse_reader.go): Go's
// `internal/` visibility rule forbids one service importing another's
// internal packages, and Repository Structure.md §100 states this is
// deliberate ("no service imports another service's internals"). The two
// copies read the exact same `spans` table schema (schema/clickhouse/) and
// must be kept in sync if that schema changes — an acceptable, explicitly
// documented tradeoff at this project's size (see Risks.md, Maintenance
// Risks) rather than introducing a shared internal-storage package that
// would blur service ownership boundaries.
package store

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	"github.com/shopspring/decimal"
)

// ClickHouseSpanReader reads a trace's span list from ClickHouse.
type ClickHouseSpanReader struct {
	conn driver.Conn
}

// NewClickHouseSpanReader returns a ClickHouseSpanReader backed by conn.
// The connection's lifecycle is owned by the caller.
func NewClickHouseSpanReader(conn driver.Conn) *ClickHouseSpanReader {
	return &ClickHouseSpanReader{conn: conn}
}

// GetTraceSpans reads every span for a single trace, ordered by
// start_time — the ordering the Replay Engine's trajectory reconstruction
// and execution-mode call-position indexing both depend on (System
// Design.md §4: "fetch ordered span list for trace_id").
func (r *ClickHouseSpanReader) GetTraceSpans(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]span.Span, error) {
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

// GetSpansByReplayID finds every span tagged with the given replay_id
// (sdk/python/agentmesh/tracer.py's Tracer.start_span attaches
// `replay.replay_id` to every span produced during an active replay run).
// The replaying process generates a fresh trace_id for its run (it is a
// new execution, not a mutation of the original), so this attribute — not
// trace_id — is the only way to find the spans a specific replay run
// produced.
func (r *ClickHouseSpanReader) GetSpansByReplayID(ctx context.Context, projectID ids.ProjectID, replayID string) ([]span.Span, error) {
	rows, err := r.conn.Query(ctx, `
		SELECT
			schema_version, project_id, trace_id, span_id, parent_span_id,
			span_kind, name, start_time, end_time, status,
			input_inline, output_inline, input_blob_ref, output_blob_ref,
			token_input, token_output, cost_usd, attributes
		FROM spans
		WHERE project_id = ? AND attributes['replay.replay_id'] = ?
		ORDER BY start_time ASC`,
		projectID.String(), replayID,
	)
	if err != nil {
		return nil, amerrors.Wrap(amerrors.CodeUnavailable, "querying spans by replay_id", err)
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
		return nil, amerrors.Wrap(amerrors.CodeUnavailable, "iterating spans-by-replay_id rows", err)
	}
	return spans, nil
}

// scanSpanRow scans one row from the query above into a shared/span.Span,
// in the exact column order that query selects (mirroring
// services/query-api/internal/store/clickhouse_reader.go's
// scanSpanRow/GetTraceSpans pairing convention).
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

// Package rest implements the Query API's REST surface (Architecture.md
// §11): "GET /v1/traces, GET /v1/traces/{id}" for Milestone 2's v0.1 scope
// (GraphQL is deferred to Milestone 3 per Product Requirements.md §7).
package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// TraceSummary is the trace-list response shape, sourced from ClickHouse's
// trace_rollups materialized view (System Design.md §5: "the list recent
// traces query never scans raw spans").
type TraceSummary struct {
	TraceID        string  `json:"trace_id"`
	SpanCount      uint64  `json:"span_count"`
	ErrorSpans     uint64  `json:"error_span_count"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	TotalTokensIn  uint64  `json:"total_token_input"`
	TotalTokensOut uint64  `json:"total_token_output"`
}

// TraceDetail is the single-trace response shape: the full ordered span
// tree (Architecture.md §2's Query API responsibility: "fetch a trace DAG").
type TraceDetail struct {
	TraceID string     `json:"trace_id"`
	Spans   []SpanView `json:"spans"`
}

// SpanView is the JSON-serializable projection of shared/span.Span for API
// responses — kept distinct from span.Span itself so the wire format can
// evolve independently of the internal domain model (e.g., hex-encoded IDs
// as strings, omitted internal-only fields).
type SpanView struct {
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	StartTime    string            `json:"start_time"`
	EndTime      string            `json:"end_time,omitempty"`
	Status       string            `json:"status,omitempty"`
	TokenInput   *uint32           `json:"token_input,omitempty"`
	TokenOutput  *uint32           `json:"token_output,omitempty"`
	CostUSD      *float64          `json:"cost_usd,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// TraceReader is the read dependency this handler needs, declared as an
// interface so the HTTP layer stays independently testable against a fake
// (Phase 3's "independently testable" standard) without a live ClickHouse
// connection in every handler-level unit test.
type TraceReader interface {
	ListTraceSummaries(ctx context.Context, projectID ids.ProjectID, limit int) ([]TraceSummary, error)
	GetTraceSpans(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]span.Span, error)
}

// TracesHandler serves both GET /v1/traces and GET /v1/traces/{id} — a
// single handler dispatching on path shape keeps the route-to-handler
// mapping in cmd/main.go trivial (one registration) while the two response
// shapes stay distinct types above.
type TracesHandler struct {
	reader    TraceReader
	projectID func(r *http.Request) (ids.ProjectID, error)
}

// NewTracesHandler returns a handler backed by reader. projectIDFromRequest
// extracts the authenticated caller's ProjectID from the request (populated
// by the authz middleware that wraps this handler) — injected as a function
// rather than hardcoding header-parsing here, keeping this handler
// independent of the specific auth transport.
func NewTracesHandler(reader TraceReader, projectIDFromRequest func(r *http.Request) (ids.ProjectID, error)) *TracesHandler {
	return &TracesHandler{reader: reader, projectID: projectIDFromRequest}
}

func (h *TracesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	projectID, err := h.projectID(r)
	if err != nil {
		writeError(w, err, http.StatusUnauthorized)
		return
	}

	traceIDStr := strings.TrimPrefix(r.URL.Path, "/v1/traces")
	traceIDStr = strings.Trim(traceIDStr, "/")

	if traceIDStr == "" {
		h.listTraces(w, r, projectID)
		return
	}
	h.getTrace(w, r, projectID, traceIDStr)
}

func (h *TracesHandler) listTraces(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "limit must be a positive integer"), http.StatusBadRequest)
			return
		}
		limit = parsed
	}

	summaries, err := h.reader.ListTraceSummaries(r.Context(), projectID, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"traces": summaries})
}

func (h *TracesHandler) getTrace(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID, traceIDStr string) {
	traceID, err := ids.ParseTraceID(traceIDStr)
	if err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "invalid trace id", err), http.StatusBadRequest)
		return
	}

	spans, err := h.reader.GetTraceSpans(r.Context(), projectID, traceID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(spans) == 0 {
		writeError(w, amerrors.New(amerrors.CodeNotFound, "trace not found"), http.StatusNotFound)
		return
	}

	detail := TraceDetail{TraceID: traceID.String(), Spans: make([]SpanView, 0, len(spans))}
	for _, s := range spans {
		detail.Spans = append(detail.Spans, toSpanView(s))
	}
	writeJSON(w, http.StatusOK, detail)
}

func toSpanView(s span.Span) SpanView {
	view := SpanView{
		SpanID:     s.SpanID.String(),
		Kind:       string(s.Kind),
		Name:       s.Name,
		StartTime:  s.StartTime.Format("2006-01-02T15:04:05.000000Z07:00"),
		Attributes: s.Attributes,
	}
	if s.HasParent() {
		view.ParentSpanID = s.ParentSpanID.String()
	}
	if !s.EndTime.IsZero() {
		view.EndTime = s.EndTime.Format("2006-01-02T15:04:05.000000Z07:00")
	}
	if s.Status != "" {
		view.Status = string(s.Status)
	}
	view.TokenInput = s.TokenInput
	view.TokenOutput = s.TokenOutput
	view.CostUSD = s.CostUSD
	return view
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // a failed write to the response body has no recovery action
}

// writeError writes a stable error-code enum response, per Architecture.md
// §17: "the Query API returns typed error responses ... with a stable
// error-code enum consumed by both the Web Console and CLI."
func writeError(w http.ResponseWriter, err error, status int) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    string(amerrors.CodeOf(err)),
			"message": err.Error(),
		},
	})
}

// writeStoreError maps a data-layer error's amerrors.Code to the
// appropriate HTTP status, keeping this mapping in one place rather than
// duplicating a switch in every handler method.
func writeStoreError(w http.ResponseWriter, err error) {
	switch amerrors.CodeOf(err) {
	case amerrors.CodeNotFound:
		writeError(w, err, http.StatusNotFound)
	case amerrors.CodeInvalidArgument:
		writeError(w, err, http.StatusBadRequest)
	case amerrors.CodeUnavailable:
		writeError(w, err, http.StatusServiceUnavailable)
	default:
		writeError(w, err, http.StatusInternalServerError)
	}
}

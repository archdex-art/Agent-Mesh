package demo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/agentmesh/agentmesh/shared/authkeys"
	"github.com/agentmesh/agentmesh/shared/span"
)

// apiKeyHeader matches authz.APIKeyHeader (query-api) and
// docs/otlp-mapping.md's authentication convention: every AgentMesh HTTP
// surface that authenticates via a raw project API key uses this same
// header name.
const apiKeyHeader = "X-AgentMesh-API-Key"

// maxTracesPerRequest caps a single POST /v1/demo/seed call so "Generate
// 1,000 traces" from the Console fans out as multiple requests rather
// than one handler call blocking on thousands of synchronous ClickHouse
// batch inserts.
const maxTracesPerRequest = 50

// SpanWriter is the persistence dependency, satisfied by
// writer.Writer — declared locally (not imported as a concrete type)
// for the same independent-testability reason ingest.Server's SpanWriter
// interface documents.
type SpanWriter interface {
	WriteBatch(ctx context.Context, spans []span.Span) error
}

// SpanPublisher is the realtime fan-out dependency, satisfied by
// publisher.Publisher. Optional, matching ingest.Server's
// SpanPublisher contract: a demo request must still succeed if Redis is
// unavailable.
type SpanPublisher interface {
	PublishBatch(ctx context.Context, spans []span.Span)
}

// Handler serves POST /v1/demo/seed: authenticates the caller's project
// API key, generates one or more synthetic traces for that project, and
// persists them through the exact same write+publish path real
// ingestion uses.
type Handler struct {
	authStore authkeys.Store
	writer    SpanWriter
	publisher SpanPublisher // nil disables realtime fan-out, same convention as ingest.Server
}

// NewHandler returns a ready-to-use Handler. publisher may be nil.
func NewHandler(authStore authkeys.Store, writer SpanWriter, publisher SpanPublisher) *Handler {
	return &Handler{authStore: authStore, writer: writer, publisher: publisher}
}

type seedRequest struct {
	// Scenario selects the trace shape (see Scenario's doc comment for
	// the full list); empty/unrecognized falls back to ScenarioDefault.
	Scenario string `json:"scenario"`
	// Count is how many traces to generate, clamped to
	// [1, maxTracesPerRequest].
	Count int `json:"count"`
}

type seedResponse struct {
	TracesCreated int      `json:"traces_created"`
	TraceIDs      []string `json:"trace_ids"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+apiKeyHeader)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path != "/v1/demo/seed" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rawKey := r.Header.Get(apiKeyHeader)
	if rawKey == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing "+apiKeyHeader+" header")
		return
	}
	record, err := authkeys.Authenticate(r.Context(), h.authStore, rawKey)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid API key")
		return
	}
	if record.Role != authkeys.RoleIngest && record.Role != authkeys.RoleAdmin {
		writeJSONError(w, http.StatusForbidden, "API key does not have ingest permission")
		return
	}

	var req seedRequest
	if r.Body != nil {
		defer r.Body.Close()
		// A bare POST with no body (or "{}") is the common case from the
		// Console's "Run Demo" button — decode errors are only real for
		// a genuinely malformed body, not an empty one, so io.EOF is
		// treated as "use every default" rather than rejected.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !isEOF(err) {
			writeJSONError(w, http.StatusBadRequest, "decoding request body: "+err.Error())
			return
		}
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	if req.Count > maxTracesPerRequest {
		req.Count = maxTracesPerRequest
	}
	scenario := Scenario(strings.ToLower(strings.TrimSpace(req.Scenario)))

	var allSpans []span.Span
	traceIDs := make([]string, 0, req.Count)
	for range req.Count {
		spans, err := Generate(record.ProjectID, scenario)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "generating demo trace: "+err.Error())
			return
		}
		allSpans = append(allSpans, spans...)
		if len(spans) > 0 {
			traceIDs = append(traceIDs, spans[0].TraceID.String())
		}
	}

	if err := h.writer.WriteBatch(r.Context(), allSpans); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "persisting demo traces: "+err.Error())
		return
	}
	if h.publisher != nil {
		h.publisher.PublishBatch(r.Context(), allSpans)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(seedResponse{TracesCreated: len(traceIDs), TraceIDs: traceIDs})
}

func isEOF(err error) bool {
	return errors.Is(err, io.EOF)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

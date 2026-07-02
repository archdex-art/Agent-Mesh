package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

type fakeTraceReader struct {
	summaries []TraceSummary
	spans     map[string][]span.Span // keyed by trace id string
	listErr   error
	getErr    error
}

func (f *fakeTraceReader) ListTraceSummaries(ctx context.Context, projectID ids.ProjectID, limit int) ([]TraceSummary, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if limit < len(f.summaries) {
		return f.summaries[:limit], nil
	}
	return f.summaries, nil
}

func (f *fakeTraceReader) GetTraceSpans(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]span.Span, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.spans[traceID.String()], nil
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

func fixedProjectIDFunc(id ids.ProjectID) func(*http.Request) (ids.ProjectID, error) {
	return func(r *http.Request) (ids.ProjectID, error) { return id, nil }
}

func failingProjectIDFunc() func(*http.Request) (ids.ProjectID, error) {
	return func(r *http.Request) (ids.ProjectID, error) {
		return ids.ProjectID{}, amerrors.New(amerrors.CodeUnauthenticated, "no key")
	}
}

func TestListTracesReturnsSummaries(t *testing.T) {
	projectID := mustProjectID(t)
	reader := &fakeTraceReader{summaries: []TraceSummary{
		{TraceID: "abc123", SpanCount: 3, TotalCostUSD: 0.05},
	}}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Traces []TraceSummary `json:"traces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshaling response: %v", err)
	}
	if len(body.Traces) != 1 || body.Traces[0].TraceID != "abc123" {
		t.Fatalf("unexpected traces: %+v", body.Traces)
	}
}

func TestListTracesRespectsLimitParam(t *testing.T) {
	projectID := mustProjectID(t)
	reader := &fakeTraceReader{summaries: []TraceSummary{
		{TraceID: "a"}, {TraceID: "b"}, {TraceID: "c"},
	}}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces?limit=2", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body struct {
		Traces []TraceSummary `json:"traces"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Traces) != 2 {
		t.Fatalf("got %d traces, want 2 (limit applied)", len(body.Traces))
	}
}

func TestListTracesRejectsInvalidLimit(t *testing.T) {
	projectID := mustProjectID(t)
	reader := &fakeTraceReader{}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces?limit=-5", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetTraceReturnsSpans(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	spanID, err := ids.NewSpanID()
	if err != nil {
		t.Fatalf("NewSpanID: %v", err)
	}
	cost := 0.0021
	reader := &fakeTraceReader{spans: map[string][]span.Span{
		traceID.String(): {
			{
				SpanID:    spanID,
				Kind:      span.KindLLMCall,
				Name:      "gpt-4.1",
				StartTime: time.Now(),
				Status:    span.StatusOK,
				CostUSD:   &cost,
			},
		},
	}}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces/"+traceID.String(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var detail TraceDetail
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("unmarshaling response: %v", err)
	}
	if len(detail.Spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(detail.Spans))
	}
	if detail.Spans[0].Kind != string(span.KindLLMCall) {
		t.Errorf("Kind = %q, want %q", detail.Spans[0].Kind, span.KindLLMCall)
	}
	if detail.Spans[0].CostUSD == nil || *detail.Spans[0].CostUSD != 0.0021 {
		t.Errorf("CostUSD = %v, want 0.0021", detail.Spans[0].CostUSD)
	}
}

func TestGetTraceReturns404ForUnknownTrace(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	reader := &fakeTraceReader{spans: map[string][]span.Span{}}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces/"+traceID.String(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetTraceReturns400ForMalformedID(t *testing.T) {
	projectID := mustProjectID(t)
	reader := &fakeTraceReader{}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces/not-a-valid-hex-id", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandlerReturns401WhenProjectIDResolutionFails(t *testing.T) {
	reader := &fakeTraceReader{}
	handler := NewTracesHandler(reader, failingProjectIDFunc())

	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandlerRejectsNonGETMethod(t *testing.T) {
	projectID := mustProjectID(t)
	reader := &fakeTraceReader{}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestListTracesPropagatesUnavailableAs503(t *testing.T) {
	projectID := mustProjectID(t)
	reader := &fakeTraceReader{listErr: amerrors.New(amerrors.CodeUnavailable, "clickhouse down")}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestErrorResponseBodyHasStableCodeField(t *testing.T) {
	projectID := mustProjectID(t)
	reader := &fakeTraceReader{}
	handler := NewTracesHandler(reader, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/traces/malformed", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshaling error response: %v", err)
	}
	if body.Error.Code != string(amerrors.CodeInvalidArgument) {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, amerrors.CodeInvalidArgument)
	}
}

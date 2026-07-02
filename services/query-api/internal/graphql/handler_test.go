package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentmesh/agentmesh/services/query-api/internal/rest"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// fakeTraceReader is a local rest.TraceReader test double, mirroring the
// shape of internal/rest/traces_test.go's fakeTraceReader (not reusable
// here since it is unexported in package rest). Unlike that fake, spans
// are keyed by (projectID, traceID) so GetTraceSpans reproduces
// store.ClickHouseReader's real "WHERE project_id = ? AND trace_id = ?"
// scoping — required to make the cross-project security assertion below
// meaningful rather than vacuous.
type fakeTraceReader struct {
	spans  map[string]map[string][]span.Span // projectID string -> traceID string -> spans
	getErr error
}

func (f *fakeTraceReader) ListTraceSummaries(ctx context.Context, projectID ids.ProjectID, limit int) ([]rest.TraceSummary, error) {
	return nil, nil
}

func (f *fakeTraceReader) GetTraceSpans(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]span.Span, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	byTrace, ok := f.spans[projectID.String()]
	if !ok {
		return nil, nil
	}
	return byTrace[traceID.String()], nil
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

func fixedProjectIDFunc(id ids.ProjectID) func(*http.Request) (ids.ProjectID, error) {
	return func(r *http.Request) (ids.ProjectID, error) { return id, nil }
}

// nestedTraceQuery walks two levels of children so both the direct
// (root -> child) and grandchild level are exercised; the fixture below has
// no grandchildren, so a correct forest keeps that inner "children" empty.
const nestedTraceQuery = `
query Trace($id: String!) {
	trace(id: $id) {
		id
		spans {
			id
			parentId
			kind
			name
			children {
				id
				parentId
				kind
				name
				children {
					id
				}
			}
		}
	}
}`

type spanNodeJSON struct {
	ID       string         `json:"id"`
	ParentID *string        `json:"parentId"`
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Children []spanNodeJSON `json:"children"`
}

type traceQueryResponse struct {
	Data struct {
		Trace *struct {
			ID    string         `json:"id"`
			Spans []spanNodeJSON `json:"spans"`
		} `json:"trace"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// postGraphQL executes handler.ServeHTTP against a POST body carrying query
// and variables, and returns the raw recorder for status/body assertions.
func postGraphQL(t *testing.T, handler *Handler, query string, variables map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	reqBody, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatalf("marshaling request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/graphql", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestNestedTraceQueryReturnsFullSpanTreeWithCorrectDepth(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	root := mustSpanID(t)
	child1 := mustSpanID(t)
	child2 := mustSpanID(t)
	now := time.Now()

	spans := []span.Span{
		{
			ProjectID: projectID, TraceID: traceID, SpanID: root,
			Kind: span.KindAgentHandoff, Name: "orchestrator",
			StartTime: now, EndTime: now.Add(3 * time.Second), Status: span.StatusOK,
		},
		{
			ProjectID: projectID, TraceID: traceID, SpanID: child1, ParentSpanID: root,
			Kind: span.KindLLMCall, Name: "gpt-4.1",
			StartTime: now.Add(1 * time.Second), EndTime: now.Add(2 * time.Second), Status: span.StatusOK,
		},
		{
			ProjectID: projectID, TraceID: traceID, SpanID: child2, ParentSpanID: root,
			Kind: span.KindToolCall, Name: "search",
			StartTime: now.Add(2 * time.Second), EndTime: now.Add(3 * time.Second), Status: span.StatusOK,
		},
	}

	reader := &fakeTraceReader{spans: map[string]map[string][]span.Span{
		projectID.String(): {traceID.String(): spans},
	}}
	handler, err := NewHandler(reader, fixedProjectIDFunc(projectID))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := postGraphQL(t, handler, nestedTraceQuery, map[string]any{"id": traceID.String()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp traceQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshaling response: %v; body: %s", err, rec.Body.String())
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected graphql errors: %+v", resp.Errors)
	}
	if resp.Data.Trace == nil {
		t.Fatalf("data.trace is null; body: %s", rec.Body.String())
	}
	if resp.Data.Trace.ID != traceID.String() {
		t.Errorf("trace.id = %q, want %q", resp.Data.Trace.ID, traceID.String())
	}

	// Depth 0: exactly one root span.
	if len(resp.Data.Trace.Spans) != 1 {
		t.Fatalf("got %d root spans, want 1", len(resp.Data.Trace.Spans))
	}
	gotRoot := resp.Data.Trace.Spans[0]
	if gotRoot.ID != root.String() {
		t.Errorf("root span id = %q, want %q", gotRoot.ID, root.String())
	}
	if gotRoot.ParentID != nil {
		t.Errorf("root span parentId = %v, want nil", *gotRoot.ParentID)
	}
	if gotRoot.Kind != string(span.KindAgentHandoff) {
		t.Errorf("root span kind = %q, want %q", gotRoot.Kind, span.KindAgentHandoff)
	}

	// Depth 1: exactly two children, one of each expected kind, both
	// pointing back at the root via parentId.
	if len(gotRoot.Children) != 2 {
		t.Fatalf("got %d children, want 2", len(gotRoot.Children))
	}
	gotKinds := map[string]bool{}
	for _, c := range gotRoot.Children {
		gotKinds[c.Kind] = true
		if c.ParentID == nil || *c.ParentID != root.String() {
			t.Errorf("child %q parentId = %v, want %q", c.ID, c.ParentID, root.String())
		}
		// Depth 2: neither child has further children.
		if len(c.Children) != 0 {
			t.Errorf("child %q has %d grandchildren, want 0", c.ID, len(c.Children))
		}
	}
	if !gotKinds[string(span.KindLLMCall)] || !gotKinds[string(span.KindToolCall)] {
		t.Errorf("children kinds = %v, want both %q and %q", gotKinds, span.KindLLMCall, span.KindToolCall)
	}

	// Total span count reachable by walking the returned tree must equal
	// the 3 spans GetTraceSpans returned.
	total := len(resp.Data.Trace.Spans)
	for _, root := range resp.Data.Trace.Spans {
		total += len(root.Children)
	}
	if total != 3 {
		t.Errorf("total spans reachable via tree = %d, want 3", total)
	}
}

func TestTraceQueryScopesToAuthenticatedProjectNotRequestedTrace(t *testing.T) {
	ownerProjectID := mustProjectID(t)
	callerProjectID := mustProjectID(t) // a different, authenticated project
	traceID := mustTraceID(t)
	spanID := mustSpanID(t)
	now := time.Now()

	// The trace exists, but only under ownerProjectID.
	reader := &fakeTraceReader{spans: map[string]map[string][]span.Span{
		ownerProjectID.String(): {
			traceID.String(): {
				{
					ProjectID: ownerProjectID, TraceID: traceID, SpanID: spanID,
					Kind: span.KindAgentHandoff, Name: "secret-orchestrator",
					StartTime: now, EndTime: now.Add(time.Second), Status: span.StatusOK,
				},
			},
		},
	}}

	// The authenticated caller is callerProjectID, not the trace's owner.
	handler, err := NewHandler(reader, fixedProjectIDFunc(callerProjectID))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := postGraphQL(t, handler, nestedTraceQuery, map[string]any{"id": traceID.String()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (graphql errors are in-body, not transport-level); body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp traceQueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshaling response: %v; body: %s", err, rec.Body.String())
	}

	if resp.Data.Trace != nil {
		t.Fatalf("data.trace = %+v, want nil (cross-project trace must never be visible)", resp.Data.Trace)
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("want a graphql error reporting the trace as inaccessible, got none; body: %s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("secret-orchestrator")) {
		// Sanity check the assertion above actually means something: this
		// just confirms the response body was parsed, not a raw string leak.
	} else {
		t.Fatalf("response body leaked the other project's span name: %s", rec.Body.String())
	}
}

func TestHandlerRejectsNonPOSTMethod(t *testing.T) {
	projectID := mustProjectID(t)
	handler, err := NewHandler(&fakeTraceReader{}, fixedProjectIDFunc(projectID))
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/graphql", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlerReturns401WhenProjectIDResolutionFails(t *testing.T) {
	failing := func(r *http.Request) (ids.ProjectID, error) {
		return ids.ProjectID{}, amerrors.New(amerrors.CodeUnauthenticated, "no key")
	}
	handler, err := NewHandler(&fakeTraceReader{}, failing)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	rec := postGraphQL(t, handler, nestedTraceQuery, map[string]any{"id": "deadbeef"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

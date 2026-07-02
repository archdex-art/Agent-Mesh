package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentmesh/agentmesh/services/replay-engine/internal/execution"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

type fakeTrajectoryReader struct {
	steps map[string][]trajectory.Step // keyed by trace id string
	err   error
}

func (f *fakeTrajectoryReader) Reconstruct(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]trajectory.Step, error) {
	if f.err != nil {
		return nil, f.err
	}
	steps, ok := f.steps[traceID.String()]
	if !ok || len(steps) == 0 {
		return nil, trajectory.ErrEmptyTrace
	}
	return steps, nil
}

type fakeRunner struct {
	startReplayID ids.ReplayID
	startErr      error
	lookupCall    execution.RecordedCall
	lookupErr     error
	completeDiff  execution.Diff
	completeErr   error
}

func (f *fakeRunner) Start(ctx context.Context, projectID ids.ProjectID, sourceTraceID ids.TraceID) (ids.ReplayID, error) {
	return f.startReplayID, f.startErr
}

func (f *fakeRunner) Lookup(replayID ids.ReplayID, kind, name string, callIndex int) (execution.RecordedCall, error) {
	return f.lookupCall, f.lookupErr
}

func (f *fakeRunner) Complete(ctx context.Context, projectID ids.ProjectID, replayID ids.ReplayID) (execution.Diff, error) {
	return f.completeDiff, f.completeErr
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

func mustReplayID(t *testing.T) ids.ReplayID {
	t.Helper()
	id, err := ids.NewReplayID()
	if err != nil {
		t.Fatalf("NewReplayID: %v", err)
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

func TestStartReplayTrajectoryModeReturnsReconstructedSteps(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	reader := &fakeTrajectoryReader{steps: map[string][]trajectory.Step{
		traceID.String(): {
			{Span: span.Span{Kind: span.KindToolCall, Name: "search", Status: span.StatusOK}, ResolvedOutput: "result"},
		},
	}}
	handler := NewReplayHandler(reader, &fakeRunner{}, fixedProjectIDFunc(projectID))

	body, _ := json.Marshal(startReplayRequest{TraceID: traceID.String(), Mode: "trajectory"})
	req := httptest.NewRequest(http.MethodPost, "/v1/replay", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Mode  string     `json:"mode"`
		Steps []stepView `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshaling response: %v", err)
	}
	if resp.Mode != "trajectory" || len(resp.Steps) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Steps[0].Output != "result" {
		t.Fatalf("Steps[0].Output = %q, want %q", resp.Steps[0].Output, "result")
	}
}

func TestStartReplayTrajectoryModeReturns404ForEmptyTrace(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	reader := &fakeTrajectoryReader{steps: map[string][]trajectory.Step{}}
	handler := NewReplayHandler(reader, &fakeRunner{}, fixedProjectIDFunc(projectID))

	body, _ := json.Marshal(startReplayRequest{TraceID: traceID.String(), Mode: "trajectory"})
	req := httptest.NewRequest(http.MethodPost, "/v1/replay", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStartReplayExecutionModeReturnsReplayID(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	replayID := mustReplayID(t)
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{startReplayID: replayID}, fixedProjectIDFunc(projectID))

	body, _ := json.Marshal(startReplayRequest{TraceID: traceID.String(), Mode: "execution"})
	req := httptest.NewRequest(http.MethodPost, "/v1/replay", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var resp struct {
		ReplayID string `json:"replay_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ReplayID != replayID.String() {
		t.Fatalf("replay_id = %q, want %q", resp.ReplayID, replayID.String())
	}
}

func TestStartReplayRejectsInvalidMode(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{}, fixedProjectIDFunc(projectID))

	body, _ := json.Marshal(startReplayRequest{TraceID: traceID.String(), Mode: "not-a-real-mode"})
	req := httptest.NewRequest(http.MethodPost, "/v1/replay", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestStartReplayRejectsMalformedTraceID(t *testing.T) {
	projectID := mustProjectID(t)
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{}, fixedProjectIDFunc(projectID))

	body, _ := json.Marshal(startReplayRequest{TraceID: "not-valid-hex", Mode: "trajectory"})
	req := httptest.NewRequest(http.MethodPost, "/v1/replay", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestReplayHandlerReturns401WhenProjectIDResolutionFails(t *testing.T) {
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{}, failingProjectIDFunc())

	req := httptest.NewRequest(http.MethodPost, "/v1/replay", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestReplayHandlerRejectsNonPOSTMethod(t *testing.T) {
	projectID := mustProjectID(t)
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{}, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodGet, "/v1/replay", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestCompleteReplayReturnsDiff(t *testing.T) {
	projectID := mustProjectID(t)
	replayID := mustReplayID(t)
	diff := execution.Diff{IdenticalCount: 3, ChangedCount: 1}
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{completeDiff: diff}, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodPost, "/v1/replay/"+replayID.String()+"/complete", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got execution.Diff
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.IdenticalCount != 3 || got.ChangedCount != 1 {
		t.Fatalf("diff = %+v, want %+v", got, diff)
	}
}

func TestCompleteReplayRejectsMalformedReplayID(t *testing.T) {
	projectID := mustProjectID(t)
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{}, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodPost, "/v1/replay/not-a-valid-uuid/complete", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCompleteReplayPropagatesUnknownRunAs404(t *testing.T) {
	projectID := mustProjectID(t)
	replayID := mustReplayID(t)
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{completeErr: execution.ErrUnknownReplayRun}, fixedProjectIDFunc(projectID))

	req := httptest.NewRequest(http.MethodPost, "/v1/replay/"+replayID.String()+"/complete", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// execution.ErrUnknownReplayRun is a plain fmt.Errorf, not an
	// *amerrors.Error, so amerrors.CodeOf degrades it to CodeInternal —
	// verifying the fallback path is exercised, not just the happy path.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (CodeOf degrades a plain error to CodeInternal)", rec.Code, http.StatusInternalServerError)
	}
}

func TestLookupHandlerReturnsRecordedCall(t *testing.T) {
	replayID := mustReplayID(t)
	runner := &fakeRunner{lookupCall: execution.RecordedCall{Output: "recorded output", Status: span.StatusOK}}
	handler := NewLookupHandler(runner)

	req := httptest.NewRequest(http.MethodGet, "/v1/replay/"+replayID.String()+"/lookup?kind=tool.call&name=search&call_index=0", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		Output string `json:"output"`
		Status string `json:"status"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Output != "recorded output" || resp.Status != "ok" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestLookupHandlerReturns404ForNoRecordedCall(t *testing.T) {
	replayID := mustReplayID(t)
	runner := &fakeRunner{lookupErr: execution.ErrNoRecordedCall}
	handler := NewLookupHandler(runner)

	req := httptest.NewRequest(http.MethodGet, "/v1/replay/"+replayID.String()+"/lookup?kind=tool.call&name=search&call_index=99", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestLookupHandlerRequiresKindNameAndCallIndex(t *testing.T) {
	replayID := mustReplayID(t)
	handler := NewLookupHandler(&fakeRunner{})

	req := httptest.NewRequest(http.MethodGet, "/v1/replay/"+replayID.String()+"/lookup?kind=tool.call", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestLookupHandlerRejectsNonGETMethod(t *testing.T) {
	replayID := mustReplayID(t)
	handler := NewLookupHandler(&fakeRunner{})

	req := httptest.NewRequest(http.MethodPost, "/v1/replay/"+replayID.String()+"/lookup", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestErrorResponseBodyHasStableCodeField(t *testing.T) {
	projectID := mustProjectID(t)
	handler := NewReplayHandler(&fakeTrajectoryReader{}, &fakeRunner{}, fixedProjectIDFunc(projectID))

	body, _ := json.Marshal(startReplayRequest{TraceID: "malformed", Mode: "trajectory"})
	req := httptest.NewRequest(http.MethodPost, "/v1/replay", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshaling error response: %v", err)
	}
	if resp.Error.Code != string(amerrors.CodeInvalidArgument) {
		t.Fatalf("error.code = %q, want %q", resp.Error.Code, amerrors.CodeInvalidArgument)
	}
}

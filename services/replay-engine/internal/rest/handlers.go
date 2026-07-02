// Package rest implements the Replay Engine's two HTTP surfaces:
//
//   - The developer/CLI-facing API (System Design.md §4: "POST /replay
//     {trace_id, mode}") to start a trajectory reconstruction or an
//     execution-mode replay run, and to fetch its result once complete.
//   - The SDK-facing lookup API the replay shim calls during
//     execution-mode replay (sdk/python/agentmesh/replay_shim.py's
//     fetch_recorded_response), unauthenticated by API key since it is
//     reached only from the same trusted developer machine the
//     `agentmesh replay` CLI command (Architecture.md §10) is already
//     running on — a deliberate scope boundary distinct from the public,
//     API-key-authenticated ingestion/query paths.
package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/agentmesh/agentmesh/services/replay-engine/internal/execution"
	"github.com/agentmesh/agentmesh/services/replay-engine/internal/trajectory"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
)

// TrajectoryReader is the read-only-mode dependency: fetch and reconstruct
// a trace's step-by-step view (internal/trajectory.Reconstruct's shape).
type TrajectoryReader interface {
	Reconstruct(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]trajectory.Step, error)
}

// ExecutionRunner is the execution-mode dependency: start, look up, and
// complete an interactive replay run (internal/execution.Runner's shape).
type ExecutionRunner interface {
	Start(ctx context.Context, projectID ids.ProjectID, sourceTraceID ids.TraceID) (ids.ReplayID, error)
	Lookup(replayID ids.ReplayID, kind, name string, callIndex int) (execution.RecordedCall, error)
	Complete(ctx context.Context, projectID ids.ProjectID, replayID ids.ReplayID) (execution.Diff, error)
}

// ReplayHandler serves the developer-facing replay API:
//
//	POST /v1/replay                 — start a run  ({trace_id, mode})
//	POST /v1/replay/{id}/complete   — mark an execution run complete, get the diff
//	GET  /v1/trajectory/{trace_id}  — trajectory-mode reconstruction (read-only, no run created)
type ReplayHandler struct {
	trajectoryReader TrajectoryReader
	runner           ExecutionRunner
	projectID        func(r *http.Request) (ids.ProjectID, error)
}

// NewReplayHandler returns a handler backed by the given dependencies.
// projectIDFromRequest mirrors the Query API's authz.ProjectIDFromRequest
// injection pattern, keeping this package independent of the specific
// auth transport.
func NewReplayHandler(trajectoryReader TrajectoryReader, runner ExecutionRunner, projectIDFromRequest func(r *http.Request) (ids.ProjectID, error)) *ReplayHandler {
	return &ReplayHandler{trajectoryReader: trajectoryReader, runner: runner, projectID: projectIDFromRequest}
}

type startReplayRequest struct {
	TraceID string `json:"trace_id"`
	Mode    string `json:"mode"`
}

// ServeHTTP dispatches POST /v1/replay and POST /v1/replay/{id}/complete.
func (h *ReplayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	projectID, err := h.projectID(r)
	if err != nil {
		writeError(w, err, http.StatusUnauthorized)
		return
	}

	suffix := pathSuffix(r, "/v1/replay")
	if suffix == "" {
		h.startReplay(w, r, projectID)
		return
	}

	replayIDStr, action, ok := strings.Cut(suffix, "/")
	if !ok || action != "complete" {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "unrecognized replay path"), http.StatusNotFound)
		return
	}
	h.completeReplay(w, r, projectID, replayIDStr)
}

func (h *ReplayHandler) startReplay(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID) {
	var req startReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "invalid request body", err), http.StatusBadRequest)
		return
	}
	traceID, err := ids.ParseTraceID(req.TraceID)
	if err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "invalid trace_id", err), http.StatusBadRequest)
		return
	}

	switch req.Mode {
	case "trajectory":
		steps, err := h.trajectoryReader.Reconstruct(r.Context(), projectID, traceID)
		if err != nil {
			writeReplayError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":  "trajectory",
			"steps": toStepViews(steps),
		})
	case "execution":
		replayID, err := h.runner.Start(r.Context(), projectID, traceID)
		if err != nil {
			writeReplayError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"replay_id": replayID.String(),
			"mode":      "execution",
			"trace_id":  traceID.String(),
		})
	default:
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, `mode must be "trajectory" or "execution"`), http.StatusBadRequest)
	}
}

func (h *ReplayHandler) completeReplay(w http.ResponseWriter, r *http.Request, projectID ids.ProjectID, replayIDStr string) {
	replayID, err := ids.ParseReplayID(replayIDStr)
	if err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "invalid replay id", err), http.StatusBadRequest)
		return
	}
	diff, err := h.runner.Complete(r.Context(), projectID, replayID)
	if err != nil {
		writeReplayError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

// LookupHandler serves GET /v1/replay/{id}/lookup?kind=&name=&call_index=,
// the endpoint sdk/python/agentmesh/replay_shim.py's
// fetch_recorded_response calls. Deliberately unauthenticated (see package
// doc) — the caller is the SDK running inside a process the developer
// themselves launched on their own machine, not an external client.
type LookupHandler struct {
	runner ExecutionRunner
}

// NewLookupHandler returns a handler backed by runner.
func NewLookupHandler(runner ExecutionRunner) *LookupHandler {
	return &LookupHandler{runner: runner}
}

func (h *LookupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	replayIDStr, action, ok := strings.Cut(pathSuffix(r, "/v1/replay"), "/")
	if !ok || action != "lookup" {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "unrecognized lookup path"), http.StatusNotFound)
		return
	}
	replayID, err := ids.ParseReplayID(replayIDStr)
	if err != nil {
		writeError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "invalid replay id", err), http.StatusBadRequest)
		return
	}

	query := r.URL.Query()
	kind := query.Get("kind")
	name := query.Get("name")
	callIndex, err := strconv.Atoi(query.Get("call_index"))
	if kind == "" || name == "" || err != nil {
		writeError(w, amerrors.New(amerrors.CodeInvalidArgument, "kind, name, and call_index are required"), http.StatusBadRequest)
		return
	}

	call, err := h.runner.Lookup(replayID, kind, name, callIndex)
	if err != nil {
		if errors.Is(err, execution.ErrNoRecordedCall) || errors.Is(err, execution.ErrUnknownReplayRun) {
			writeError(w, amerrors.Wrap(amerrors.CodeNotFound, "no recorded call at this position", err), http.StatusNotFound)
			return
		}
		writeError(w, amerrors.Wrap(amerrors.CodeInternal, "looking up recorded call", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"output": call.Output,
		"status": string(call.Status),
	})
}

type stepView struct {
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	Status       string            `json:"status,omitempty"`
	Input        string            `json:"input,omitempty"`
	Output       string            `json:"output,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

func toStepViews(steps []trajectory.Step) []stepView {
	views := make([]stepView, 0, len(steps))
	for _, step := range steps {
		views = append(views, toStepView(step))
	}
	return views
}

func toStepView(step trajectory.Step) stepView {
	s := step.Span
	view := stepView{
		SpanID:     s.SpanID.String(),
		Kind:       string(s.Kind),
		Name:       s.Name,
		Status:     string(s.Status),
		Input:      step.ResolvedInput,
		Output:     step.ResolvedOutput,
		Attributes: s.Attributes,
	}
	if s.HasParent() {
		view.ParentSpanID = s.ParentSpanID.String()
	}
	return view
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // a failed write to the response body has no recovery action
}

func writeError(w http.ResponseWriter, err error, status int) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    string(amerrors.CodeOf(err)),
			"message": err.Error(),
		},
	})
}

// writeReplayError maps a data-layer error's amerrors.Code to the
// appropriate HTTP status, distinguishing trajectory.ErrEmptyTrace (no
// spans found — a client-correctable 404) from an unexpected backend
// failure.
func writeReplayError(w http.ResponseWriter, err error) {
	if errors.Is(err, trajectory.ErrEmptyTrace) {
		writeError(w, amerrors.Wrap(amerrors.CodeNotFound, "trace not found or has no spans", err), http.StatusNotFound)
		return
	}
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

func pathSuffix(r *http.Request, prefix string) string {
	return strings.Trim(strings.TrimPrefix(r.URL.Path, prefix), "/")
}


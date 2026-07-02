package graphql

import (
	"encoding/json"
	"net/http"

	"github.com/agentmesh/agentmesh/services/query-api/internal/rest"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/graphql-go/graphql"
)

// requestBody is the standard "GraphQL over HTTP" POST envelope: a query
// document plus its variables.
type requestBody struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

// Handler serves POST /v1/graphql. It is a second transport over the exact
// same rest.TraceReader dependency internal/rest's TracesHandler uses (no
// second data-access path), and takes its projectIDFromRequest dependency
// in the same shape as rest.NewTracesHandler for the same reason: the HTTP
// layer stays testable against a fake without a live ClickHouse connection,
// and cmd/main.go can wire both handlers to the same
// authz.ProjectIDFromRequest function.
type Handler struct {
	reader    rest.TraceReader
	projectID func(r *http.Request) (ids.ProjectID, error)
	schema    graphql.Schema
}

// NewHandler returns a Handler backed by reader, with its GraphQL schema
// built once at construction time (schema construction is a fixed, static
// cost independent of any request; graphql.Schema is safe for concurrent
// use by graphql.Do across goroutines).
func NewHandler(reader rest.TraceReader, projectIDFromRequest func(r *http.Request) (ids.ProjectID, error)) (*Handler, error) {
	schema, err := newSchema(reader)
	if err != nil {
		return nil, amerrors.Wrap(amerrors.CodeInternal, "building graphql schema", err)
	}
	return &Handler{reader: reader, projectID: projectIDFromRequest, schema: schema}, nil
}

// ServeHTTP decodes a GraphQL-over-HTTP POST body, executes it against
// h.schema scoped to the authenticated caller's ProjectID, and writes the
// result.
//
// Per GraphQL-over-HTTP convention, a successfully executed query that
// contains field-resolver errors (e.g. "trace not found") is still an HTTP
// 200 with a non-empty top-level "errors" array in the body — GraphQL
// errors are part of the response payload, not the transport status. Only
// genuine transport/auth failures (wrong method, missing/invalid API key,
// malformed JSON body) use a non-200 status, mirroring
// internal/rest/traces.go's writeError convention for those cases.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeTransportError(w, amerrors.New(amerrors.CodeInvalidArgument, "method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	projectID, err := h.projectID(r)
	if err != nil {
		writeTransportError(w, err, http.StatusUnauthorized)
		return
	}

	var body requestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeTransportError(w, amerrors.Wrap(amerrors.CodeInvalidArgument, "malformed request body", err), http.StatusBadRequest)
		return
	}

	ctx := contextWithProjectID(r.Context(), projectID)
	result := graphql.Do(graphql.Params{
		Schema:         h.schema,
		RequestString:  body.Query,
		VariableValues: body.Variables,
		Context:        ctx,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result) //nolint:errcheck // a failed write to the response body has no recovery action
}

// writeJSON mirrors internal/rest/traces.go's helper of the same name;
// duplicated rather than exported from rest because it is a trivial,
// transport-only helper and exporting it would widen rest's public surface
// for no shared-behavior benefit.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // a failed write to the response body has no recovery action
}

// writeTransportError writes the same stable error-code envelope
// internal/rest/traces.go's writeError uses, for the transport/auth
// failures that happen before a GraphQL document is ever executed.
func writeTransportError(w http.ResponseWriter, err error, status int) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    string(amerrors.CodeOf(err)),
			"message": err.Error(),
		},
	})
}

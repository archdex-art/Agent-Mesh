// Package graphql implements the Query API's GraphQL surface
// (Milestone 3, Product Requirements.md §7: GraphQL was deferred out of
// Milestone 2's v0.1 REST scope — see internal/rest's package doc — and is
// added here as a second transport over the exact same read path).
//
// This package deliberately introduces no second data-access path: both
// the schema's resolvers and internal/rest's handlers depend on the same
// rest.TraceReader interface, backed in production by the same
// store.ClickHouseReader. GraphQL only adds a query shape (a single
// `trace(id)` root field whose `Span.children` resolves recursively
// in-memory) on top of data internal/rest already knows how to fetch.
//
// Built with github.com/graphql-go/graphql, a pure-Go GraphQL execution
// engine with no code-generation step, keeping this consistent with the
// rest of the repo's hand-written-Go style: the schema below is assembled
// programmatically via graphql.NewObject/graphql.NewSchema, not generated
// from a .graphql schema file.
package graphql

import (
	"context"
	"strconv"

	"github.com/agentmesh/agentmesh/services/query-api/internal/rest"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	"github.com/graphql-go/graphql"
)

// projectIDCtxKey is the context key ServeHTTP uses to hand the
// authenticated caller's ProjectID to the "trace" root resolver. A
// dedicated unexported type (rather than a string) avoids collisions with
// any other package's use of context.WithValue, mirroring
// internal/authz/middleware.go's contextKey pattern for the same reason.
type projectIDCtxKey struct{}

// contextWithProjectID stashes projectID for the resolvers below to read.
func contextWithProjectID(ctx context.Context, projectID ids.ProjectID) context.Context {
	return context.WithValue(ctx, projectIDCtxKey{}, projectID)
}

// projectIDFromContext retrieves the ProjectID stashed by
// contextWithProjectID. ok is false if called outside a request ServeHTTP
// set up — resolvers treat that as an authentication failure rather than
// falling back to an unscoped query, since project_id is AgentMesh's
// tenant boundary (Architecture.md §13).
func projectIDFromContext(ctx context.Context) (ids.ProjectID, bool) {
	id, ok := ctx.Value(projectIDCtxKey{}).(ids.ProjectID)
	return id, ok
}

// spanNode is the in-memory tree node the Span GraphQL type resolves
// against. GetTraceSpans returns a flat, start-time-ordered list; buildForest
// groups it into a parent->children map exactly once per request so the
// `children` field can walk pointers instead of re-querying per node.
type spanNode struct {
	span     span.Span
	children []*spanNode
}

// buildForest groups a flat span list into a forest of spanNodes keyed by
// parent_span_id. A span whose declared parent is not present in the same
// list (e.g. the list is scoped to one trace but references a parent
// outside it, which should not happen but is not assumed) is treated as a
// root defensively, the same "never trust adjacency blindly" stance
// scanSpanRow's callers take with malformed data.
func buildForest(spans []span.Span) []*spanNode {
	nodes := make(map[ids.SpanID]*spanNode, len(spans))
	for i := range spans {
		nodes[spans[i].SpanID] = &spanNode{span: spans[i]}
	}
	roots := make([]*spanNode, 0, len(spans))
	for i := range spans {
		node := nodes[spans[i].SpanID]
		if spans[i].HasParent() {
			if parent, ok := nodes[spans[i].ParentSpanID]; ok {
				parent.children = append(parent.children, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	return roots
}

// traceResult is the root "trace" field's resolved value: the trace's own
// id plus its root-level spans (spans with no parent in the trace, or whose
// parent fell outside the returned set). Every span below a root is reached
// by walking Span.children, so the wire shape never repeats a span at both
// the top level and nested under its parent.
type traceResult struct {
	id    string
	roots []*spanNode
}

// newSpanType builds the recursive Span GraphQL type:
//
//	{ id, parentId, kind, name, startTimeNs, endTimeNs, status, children }
//
// "kind" is deliberately the field name (not "type") to match the naming
// shared/span.Kind and rest.SpanView already use for this concept — a
// second vocabulary for the same field would be its own bug.
//
// Fields is a graphql.FieldsThunk (a func() graphql.Fields) rather than a
// plain graphql.Fields map because "children" must reference spanType
// itself; the thunk defers evaluation until spanType is fully assigned,
// which is the standard graphql-go idiom for self-referential types.
func newSpanType() *graphql.Object {
	var spanType *graphql.Object
	spanType = graphql.NewObject(graphql.ObjectConfig{
		Name: "Span",
		Fields: graphql.FieldsThunk(func() graphql.Fields {
			return graphql.Fields{
				"id": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return p.Source.(*spanNode).span.SpanID.String(), nil
					},
				},
				"parentId": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						s := p.Source.(*spanNode).span
						if !s.HasParent() {
							return nil, nil
						}
						return s.ParentSpanID.String(), nil
					},
				},
				"kind": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return string(p.Source.(*spanNode).span.Kind), nil
					},
				},
				"name": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return p.Source.(*spanNode).span.Name, nil
					},
				},
				"startTimeNs": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return strconv.FormatInt(p.Source.(*spanNode).span.StartTime.UnixNano(), 10), nil
					},
				},
				"endTimeNs": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						s := p.Source.(*spanNode).span
						if s.EndTime.IsZero() {
							return nil, nil
						}
						return strconv.FormatInt(s.EndTime.UnixNano(), 10), nil
					},
				},
				"status": &graphql.Field{
					Type: graphql.String,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						s := p.Source.(*spanNode).span
						if s.Status == "" {
							return nil, nil
						}
						return string(s.Status), nil
					},
				},
				"children": &graphql.Field{
					Type: graphql.NewList(spanType),
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return p.Source.(*spanNode).children, nil
					},
				},
			}
		}),
	})
	return spanType
}

// newSchema builds the GraphQL schema backed by reader: a single root query
// field, `trace(id: String!): Trace`, where Trace is `{ id, spans }` and
// spans holds the trace's root-level Spans (see traceResult).
//
// The resolver scopes every GetTraceSpans call to the ProjectID stashed in
// the request context (projectIDFromContext) — never to a client-supplied
// project argument — because project_id is the tenant boundary
// (Architecture.md §13). There is no way to express "give me another
// project's trace" through this schema at all.
func newSchema(reader rest.TraceReader) (graphql.Schema, error) {
	spanType := newSpanType()

	traceType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Trace",
		Fields: graphql.Fields{
			"id": &graphql.Field{
				Type: graphql.String,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return p.Source.(*traceResult).id, nil
				},
			},
			"spans": &graphql.Field{
				Type: graphql.NewList(spanType),
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return p.Source.(*traceResult).roots, nil
				},
			},
		},
	})

	queryType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"trace": &graphql.Field{
				Type: traceType,
				Args: graphql.FieldConfigArgument{
					"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					projectID, ok := projectIDFromContext(p.Context)
					if !ok {
						return nil, amerrors.New(amerrors.CodeUnauthenticated, "graphql: no authenticated project id in context")
					}

					idArg, _ := p.Args["id"].(string)
					traceID, err := ids.ParseTraceID(idArg)
					if err != nil {
						return nil, amerrors.Wrap(amerrors.CodeInvalidArgument, "invalid trace id", err)
					}

					spans, err := reader.GetTraceSpans(p.Context, projectID, traceID)
					if err != nil {
						return nil, err
					}
					if len(spans) == 0 {
						// Covers both "no such trace" and "trace belongs to a
						// different project" identically — like
						// rest.TracesHandler.getTrace's 404, this never
						// distinguishes the two so a caller cannot probe
						// for another project's trace IDs.
						return nil, amerrors.New(amerrors.CodeNotFound, "trace not found")
					}

					return &traceResult{id: traceID.String(), roots: buildForest(spans)}, nil
				},
			},
		},
	})

	return graphql.NewSchema(graphql.SchemaConfig{Query: queryType})
}

// Package logging provides AgentMesh's shared structured-logging setup.
//
// Architecture.md §16 requires "structured JSON logs (one line per event) on
// stdout for every service, following a shared log-schema library so level,
// service, trace_id, and message fields are consistent."
//
// This package is a thin wrapper around the standard library's log/slog
// (stable since Go 1.21) rather than a hand-rolled logger: slog already
// produces structured JSON, supports leveling, and lets handlers attach
// persistent fields (via With) with zero external dependencies — writing a
// custom logger here would be exactly the kind of "sharp edge where it
// doesn't add value" that Product Requirements.md §2 warns against.
//
// The one piece of shared policy this package adds on top of slog is the
// service field convention and the trace_id/internal_trace_id disambiguation
// from System Design.md §1: a customer's agent trace_id must never be
// confused with AgentMesh's own internal_trace_id in a log line, so this
// package exposes distinct, explicitly named helpers instead of a single
// generic "WithID" that invites mixing the two up.
package logging

import (
	"context"
	"log/slog"
	"os"

	"github.com/agentmesh/agentmesh/shared/ids"
)

// New returns a slog.Logger configured for AgentMesh's shared JSON schema:
// every record carries a "service" field identifying which AgentMesh
// component emitted it, writes to stdout (Architecture.md §16: "no service
// writes logs to local disk"), and defaults to Info level.
func New(service string, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(handler).With(slog.String("service", service))
}

// WithTraceID returns a logger annotated with a customer-scoped agent
// trace ID (System Design.md §1's `trace_id`). Never use this for
// AgentMesh's own internal operational tracing — see WithInternalTraceID.
func WithTraceID(logger *slog.Logger, traceID ids.TraceID) *slog.Logger {
	return logger.With(slog.String("trace_id", traceID.String()))
}

// WithProjectID returns a logger annotated with the tenant-scoping
// ProjectID (Architecture.md §13).
func WithProjectID(logger *slog.Logger, projectID ids.ProjectID) *slog.Logger {
	return logger.With(slog.String("project_id", projectID.String()))
}

// WithInternalTraceID returns a logger annotated with AgentMesh's own
// operational trace ID (Architecture.md §15) — distinct from a customer's
// agent trace_id so the two are never confused when reading a log line.
func WithInternalTraceID(logger *slog.Logger, internalTraceID string) *slog.Logger {
	return logger.With(slog.String("internal_trace_id", internalTraceID))
}

// FromContext retrieves a logger stashed in ctx by WithContext, or returns
// slog.Default() if none was stashed — services should never panic on a
// missing logger in context, only degrade to the default.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerContextKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithContext returns a new context carrying logger, retrievable via
// FromContext. This lets request-scoped fields (e.g., a request's trace_id)
// propagate through a call chain without threading a *slog.Logger through
// every function signature.
func WithContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey{}, logger)
}

type loggerContextKey struct{}

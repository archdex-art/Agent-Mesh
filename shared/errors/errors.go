// Package errors defines AgentMesh's shared error taxonomy.
//
// Architecture.md §17 requires two things a plain `error` cannot express on
// its own:
//
//  1. The Query API must return "typed error responses ... with a stable
//     error-code enum consumed by both the Web Console and CLI" — a wire
//     format both clients can switch on without parsing message strings.
//  2. The Collector's ingestion path and the MCP Gateway's request path have
//     opposite failure philosophies (favor availability vs. fail closed).
//     Callers need to distinguish "retryable" from "terminal" failures
//     programmatically, not by string-matching.
//
// This package provides a single Error type carrying a stable Code, a
// human-readable Message, and an optional wrapped cause, plus the sentinel
// Code values shared across every service.
package errors

import (
	"errors"
	"fmt"
)

// Code is a stable, wire-safe error classification. Codes are never renamed
// once shipped (a client's `switch err.Code` must keep working across
// AgentMesh releases) — a new failure mode gets a new Code, never a repurposed
// old one.
type Code string

const (
	// CodeNotFound indicates the requested resource does not exist.
	CodeNotFound Code = "not_found"
	// CodeInvalidArgument indicates the caller supplied a malformed or
	// semantically invalid argument (e.g., a trace ID that isn't valid hex).
	CodeInvalidArgument Code = "invalid_argument"
	// CodeUnauthenticated indicates missing or invalid credentials.
	CodeUnauthenticated Code = "unauthenticated"
	// CodePermissionDenied indicates valid credentials lacking the required
	// scope/role for the requested operation.
	CodePermissionDenied Code = "permission_denied"
	// CodeAlreadyExists indicates a create operation collided with an
	// existing resource (e.g., duplicate project name).
	CodeAlreadyExists Code = "already_exists"
	// CodeUnavailable indicates a transient failure the caller should retry
	// (e.g., the Collector's Trace Store is temporarily unreachable). This is
	// the one Code that callers are expected to programmatically retry on.
	CodeUnavailable Code = "unavailable"
	// CodeInternal indicates an unexpected, non-retryable server-side failure.
	CodeInternal Code = "internal"
	// CodeSchemaVersionMismatch indicates a span or request was rejected
	// because its schema_version is not recognized by this service version
	// (Technical Roadmap.md §9's rolling-upgrade safety net).
	CodeSchemaVersionMismatch Code = "schema_version_mismatch"
)

// Error is AgentMesh's shared error type: a stable Code plus a human-readable
// Message and an optional wrapped cause.
type Error struct {
	Code    Code
	Message string
	cause   error
}

// Error implements the standard error interface.
func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the wrapped cause to errors.Is / errors.As.
func (e *Error) Unwrap() error { return e.cause }

// New creates an *Error with the given Code and Message and no wrapped cause.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap creates an *Error with the given Code and Message, wrapping cause so
// callers can still recover the original error via errors.Unwrap/errors.Is.
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// CodeOf extracts the Code from err if it is an *Error (directly or via
// wrapping), and CodeInternal otherwise — every code path through AgentMesh's
// services is expected to surface a Code, but CodeOf never panics on a
// plain error that slipped through, it degrades to the safest classification.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// IsRetryable reports whether the error's Code indicates the caller should
// retry the operation (Architecture.md §17's ingestion-path philosophy:
// "a Collector outage degrades to traces delayed, never to agent crashes").
func IsRetryable(err error) bool {
	return CodeOf(err) == CodeUnavailable
}

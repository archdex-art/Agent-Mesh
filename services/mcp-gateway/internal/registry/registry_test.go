package registry

import (
	"errors"
	"testing"

	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/jackc/pgx/v5"
)

// TestWrapRowErrorMapsNoRowsToNotFound exercises the exact classification
// the router depends on to turn "server doesn't exist" into a clean
// JSON-RPC error instead of an opaque 500 — no live Postgres required
// since pgx.ErrNoRows is just a sentinel value, not a connection.
func TestWrapRowErrorMapsNoRowsToNotFound(t *testing.T) {
	err := wrapRowError(pgx.ErrNoRows, "no server found", "querying mcp_servers")

	if amerrors.CodeOf(err) != amerrors.CodeNotFound {
		t.Fatalf("CodeOf(err) = %v, want %v", amerrors.CodeOf(err), amerrors.CodeNotFound)
	}
	if got := err.Error(); got == "" {
		t.Fatal("wrapRowError produced an empty message")
	}
}

// TestWrapRowErrorMapsWrappedNoRowsToNotFound confirms the classification
// survives errors.Is-compatible wrapping, not just an exact sentinel
// match (pgx callers sometimes return wrapped variants).
func TestWrapRowErrorMapsWrappedNoRowsToNotFound(t *testing.T) {
	wrapped := errors.Join(pgx.ErrNoRows)
	err := wrapRowError(wrapped, "no server found", "querying mcp_servers")

	if amerrors.CodeOf(err) != amerrors.CodeNotFound {
		t.Fatalf("CodeOf(err) = %v, want %v", amerrors.CodeOf(err), amerrors.CodeNotFound)
	}
}

// TestWrapRowErrorMapsOtherErrorsToUnavailable ensures a genuine
// connectivity/query failure is distinguishable from "not found" — the
// router must not turn a Postgres outage into a false "unknown server"
// response.
func TestWrapRowErrorMapsOtherErrorsToUnavailable(t *testing.T) {
	cause := errors.New("connection refused")
	err := wrapRowError(cause, "no server found", "querying mcp_servers")

	if amerrors.CodeOf(err) != amerrors.CodeUnavailable {
		t.Fatalf("CodeOf(err) = %v, want %v", amerrors.CodeOf(err), amerrors.CodeUnavailable)
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapRowError did not preserve the original cause via Unwrap")
	}
}

// TestIsNoRowsRecognizesSentinelAndItsWrappers exercises the pgx-specific
// detection isNoRows exists to isolate, so registry.go's classification
// logic can stay decoupled from exactly how pgx signals "no rows" without
// a live database.
func TestIsNoRowsRecognizesSentinelAndItsWrappers(t *testing.T) {
	if !isNoRows(pgx.ErrNoRows) {
		t.Fatal("isNoRows(pgx.ErrNoRows) = false, want true")
	}
	if !isNoRows(errors.Join(pgx.ErrNoRows)) {
		t.Fatal("isNoRows(wrapped pgx.ErrNoRows) = false, want true")
	}
	if isNoRows(errors.New("some other failure")) {
		t.Fatal("isNoRows(unrelated error) = true, want false")
	}
}

// Package ids defines AgentMesh's identifier types and generators.
//
// System Design.md §1 draws an explicit line between customer-scoped
// identifiers (TraceID, SpanID — W3C Trace Context compatible, so a
// customer's existing OTel tooling interoperates with AgentMesh for free)
// and AgentMesh's own control-plane identifiers (ProjectID, ReplayID —
// UUIDv7, chosen over UUIDv4 for time-ordered index locality in Postgres
// B-tree indexes).
//
// Every ID is a distinct Go type, not a bare string. This is deliberate:
// a function signature like `func Lookup(trace TraceID, project ProjectID)`
// cannot have its arguments silently swapped by the compiler, whereas
// `func Lookup(trace, project string)` can. The cost (explicit conversions
// at I/O boundaries) is paid once, in the exporter/parser code; the safety
// benefit is paid back on every internal call site for the life of the
// project.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrInvalidID is returned when a string fails to parse as the expected ID format.
var ErrInvalidID = errors.New("ids: invalid identifier format")

// TraceID identifies one agent run (System Design.md §1). It is a 16-byte
// value rendered as 32 lowercase hex characters, matching the W3C Trace
// Context "trace-id" field so it interoperates with any OTel-based tooling
// a customer already runs.
type TraceID [16]byte

// SpanID identifies one span within a TraceID's span tree. It is an 8-byte
// value rendered as 16 lowercase hex characters, matching W3C Trace Context's
// "parent-id" field width.
type SpanID [8]byte

// ProjectID is AgentMesh's tenant-boundary identifier: every span, API key,
// registry entry, and policy belongs to exactly one ProjectID (Architecture.md
// §13). It is a UUIDv7 for time-ordered locality in the control-plane store.
type ProjectID [16]byte

// ReplayID identifies one execution of the Replay Engine against a source
// TraceID (System Design.md §1). A single trace may have many ReplayIDs.
type ReplayID [16]byte

// NewTraceID generates a new random TraceID.
func NewTraceID() (TraceID, error) {
	var id TraceID
	if _, err := rand.Read(id[:]); err != nil {
		return TraceID{}, fmt.Errorf("ids: generating trace id: %w", err)
	}
	return id, nil
}

// NewSpanID generates a new random SpanID.
func NewSpanID() (SpanID, error) {
	var id SpanID
	if _, err := rand.Read(id[:]); err != nil {
		return SpanID{}, fmt.Errorf("ids: generating span id: %w", err)
	}
	return id, nil
}

// NewProjectID generates a new time-ordered UUIDv7 ProjectID.
func NewProjectID() (ProjectID, error) {
	raw, err := newUUIDv7()
	if err != nil {
		return ProjectID{}, fmt.Errorf("ids: generating project id: %w", err)
	}
	return ProjectID(raw), nil
}

// NewReplayID generates a new time-ordered UUIDv7 ReplayID.
func NewReplayID() (ReplayID, error) {
	raw, err := newUUIDv7()
	if err != nil {
		return ReplayID{}, fmt.Errorf("ids: generating replay id: %w", err)
	}
	return ReplayID(raw), nil
}

// String renders the TraceID as 32 lowercase hex characters.
func (id TraceID) String() string { return hex.EncodeToString(id[:]) }

// String renders the SpanID as 16 lowercase hex characters.
func (id SpanID) String() string { return hex.EncodeToString(id[:]) }

// String renders the ProjectID in canonical UUID form.
func (id ProjectID) String() string { return formatUUID(id) }

// String renders the ReplayID in canonical UUID form.
func (id ReplayID) String() string { return formatUUID(id) }

// IsZero reports whether the TraceID is the zero value (unset).
func (id TraceID) IsZero() bool { return id == TraceID{} }

// IsZero reports whether the SpanID is the zero value (unset).
func (id SpanID) IsZero() bool { return id == SpanID{} }

// ParseTraceID parses a 32-character lowercase hex string into a TraceID.
func ParseTraceID(s string) (TraceID, error) {
	var id TraceID
	if len(s) != 32 {
		return id, fmt.Errorf("%w: trace id must be 32 hex chars, got %d", ErrInvalidID, len(s))
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return TraceID{}, fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	return id, nil
}

// ParseSpanID parses a 16-character lowercase hex string into a SpanID.
func ParseSpanID(s string) (SpanID, error) {
	var id SpanID
	if len(s) != 16 {
		return id, fmt.Errorf("%w: span id must be 16 hex chars, got %d", ErrInvalidID, len(s))
	}
	if _, err := hex.Decode(id[:], []byte(s)); err != nil {
		return SpanID{}, fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	return id, nil
}

// ParseProjectID parses a canonical UUID string into a ProjectID.
func ParseProjectID(s string) (ProjectID, error) {
	raw, err := parseUUID(s)
	if err != nil {
		return ProjectID{}, err
	}
	return ProjectID(raw), nil
}

// ParseReplayID parses a canonical UUID string into a ReplayID.
func ParseReplayID(s string) (ReplayID, error) {
	raw, err := parseUUID(s)
	if err != nil {
		return ReplayID{}, err
	}
	return ReplayID(raw), nil
}

// newUUIDv7 generates a version-7 UUID: a 48-bit big-endian Unix millisecond
// timestamp followed by 74 random bits (version/variant bits fixed per
// RFC 9562). Time-ordering means new rows cluster at the tail of a B-tree
// index instead of scattering randomly, which matters for the
// projects/api_keys/registry tables this backs (System Design.md §2.2).
func newUUIDv7() ([16]byte, error) {
	var u [16]byte
	ms := uint64(time.Now().UnixMilli())
	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)

	if _, err := rand.Read(u[6:]); err != nil {
		return u, fmt.Errorf("ids: reading random bytes: %w", err)
	}
	u[6] = (u[6] & 0x0F) | 0x70 // version 7
	u[8] = (u[8] & 0x3F) | 0x80 // RFC 9562 variant
	return u, nil
}

func formatUUID(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

func parseUUID(s string) ([16]byte, error) {
	var u [16]byte
	clean := make([]byte, 0, 32)
	for _, r := range s {
		if r == '-' {
			continue
		}
		clean = append(clean, byte(r))
	}
	if len(clean) != 32 {
		return u, fmt.Errorf("%w: uuid must be 32 hex chars excluding dashes, got %d", ErrInvalidID, len(clean))
	}
	if _, err := hex.Decode(u[:], clean); err != nil {
		return u, fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	return u, nil
}

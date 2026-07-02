// Package trajectory implements the Replay Engine's read-only replay mode
// (Architecture.md §7): reconstructing a trace's exact sequence of
// LLM/tool calls without re-executing anything, for pure debugging and
// inspection, and as the shared foundation execution-mode replay
// (internal/execution) builds on for recorded-response lookup.
package trajectory

import (
	"context"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// SpanReader is the ClickHouse read dependency this package needs,
// declared as an interface (matching ingest.SpanWriter's pattern in the
// Collector) so trajectory reconstruction stays testable against a fake
// without a live ClickHouse connection.
type SpanReader interface {
	GetTraceSpans(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]span.Span, error)
}

// BlobStore is the object-storage read dependency for resolving a
// payload's blob_ref into its actual bytes (Architecture.md §14).
type BlobStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// Step is one reconstructed unit of the trajectory: a span plus its
// resolved (never truncated, never a bare blob_ref) input/output text.
// This is the shape both the trajectory-mode API response and
// execution-mode's recorded-response index are built from.
type Step struct {
	Span           span.Span
	ResolvedInput  string
	ResolvedOutput string
}

// ErrEmptyTrace is returned when a trace_id has no spans — either it
// never existed or every span has already aged out under the project's
// retention policy (System Design.md §2.1's TTL).
var ErrEmptyTrace = &emptyTraceError{}

type emptyTraceError struct{}

func (e *emptyTraceError) Error() string { return "trajectory: trace has no spans" }

// Reconstruct fetches a trace's ordered span list and resolves every
// payload (inline or blob-backed) into plain text, per Architecture.md
// §7's "every tool.call and llm.call span stores its full input and
// output" contract. The determinism boundary this stops at — state the
// agent read without going through an instrumented call — is documented
// in System Design.md §4 and is not something this function can detect;
// it can only faithfully replay what the SDK recorded.
func Reconstruct(ctx context.Context, reader SpanReader, blobs BlobStore, projectID ids.ProjectID, traceID ids.TraceID) ([]Step, error) {
	spans, err := reader.GetTraceSpans(ctx, projectID, traceID)
	if err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return nil, ErrEmptyTrace
	}

	steps := make([]Step, 0, len(spans))
	for _, s := range spans {
		input, err := ResolvePayload(ctx, blobs, s.Input)
		if err != nil {
			return nil, err
		}
		output, err := ResolvePayload(ctx, blobs, s.Output)
		if err != nil {
			return nil, err
		}
		steps = append(steps, Step{Span: s, ResolvedInput: input, ResolvedOutput: output})
	}
	return steps, nil
}

// ResolvePayload returns a payload's string value regardless of whether it
// is stored inline or in blob storage — the one place every replay-mode
// consumer goes through, so a blob-fetch bug is fixed once, not twice.
func ResolvePayload(ctx context.Context, store BlobStore, p span.Payload) (string, error) {
	if p.IsInline() {
		return p.Inline, nil
	}
	data, err := store.Get(ctx, p.BlobRef)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

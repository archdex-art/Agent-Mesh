package ingest

import (
	"context"
	"fmt"

	"github.com/agentmesh/agentmesh/services/collector/internal/blobstore"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

// inlinePayloadLimitBytes mirrors docs/otlp-mapping.md's "Payload size
// threshold" (System Design.md §2.1's 4KB inline/blob-ref cutoff). Defined
// here, not in shared/span, because the threshold is specifically an
// ingestion-time decision the Collector makes — it is not a property of
// the Span type itself.
const inlinePayloadLimitBytes = 4096

// BlobStore is the minimal dependency Offloader needs to move an oversized
// payload to object storage — exactly blobstore.Client's Put method
// signature, declared as an interface (matching the SpanWriter/AuditEmitter
// pattern already used in this codebase) so the offload logic is testable
// against a fake without a live MinIO connection.
type BlobStore interface {
	Put(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID, spanID ids.SpanID, kind blobstore.PayloadKind, data []byte) (string, error)
}

// Offloader implements the Collector's side of docs/otlp-mapping.md's
// corrected "Payload size threshold" contract: it inspects each decoded
// span's Input/Output payloads and, for anything at or above
// inlinePayloadLimitBytes, uploads it to blob storage and rewrites the
// span to carry a BlobRef instead of the inline value — never both
// (shared/span.Span.Validate enforces that invariant downstream).
type Offloader struct {
	store BlobStore
}

// NewOffloader returns an Offloader backed by store.
func NewOffloader(store BlobStore) *Offloader {
	return &Offloader{store: store}
}

// Offload mutates s in place, moving any oversized inline payload to blob
// storage. It is a no-op for spans whose payloads are already within the
// inline threshold (the common case) — no network call is made unless a
// payload actually needs offloading.
func (o *Offloader) Offload(ctx context.Context, s *span.Span) error {
	if needsOffload(s.Input) {
		blobRef, err := o.store.Put(ctx, s.ProjectID, s.TraceID, s.SpanID, blobstore.PayloadInput, []byte(s.Input.Inline))
		if err != nil {
			return fmt.Errorf("ingest: offloading input payload for span %s: %w", s.SpanID, err)
		}
		s.Input = span.Payload{BlobRef: blobRef}
	}
	if needsOffload(s.Output) {
		blobRef, err := o.store.Put(ctx, s.ProjectID, s.TraceID, s.SpanID, blobstore.PayloadOutput, []byte(s.Output.Inline))
		if err != nil {
			return fmt.Errorf("ingest: offloading output payload for span %s: %w", s.SpanID, err)
		}
		s.Output = span.Payload{BlobRef: blobRef}
	}
	return nil
}

func needsOffload(p span.Payload) bool {
	return p.IsInline() && len(p.Inline) >= inlinePayloadLimitBytes
}

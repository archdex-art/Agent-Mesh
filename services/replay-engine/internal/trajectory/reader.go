package trajectory

import (
	"context"

	"github.com/agentmesh/agentmesh/shared/ids"
)

// Reader adapts the package-level Reconstruct function to an interface
// (internal/rest.TrajectoryReader's shape), the same "function wrapped as
// a single-method interface" pattern used for testability throughout this
// codebase (e.g. ingest.SpanWriter in the Collector).
type Reader struct {
	spanReader SpanReader
	blobs      BlobStore
}

// NewReader returns a Reader backed by the given dependencies.
func NewReader(spanReader SpanReader, blobs BlobStore) *Reader {
	return &Reader{spanReader: spanReader, blobs: blobs}
}

// Reconstruct implements internal/rest.TrajectoryReader.
func (r *Reader) Reconstruct(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]Step, error) {
	return Reconstruct(ctx, r.spanReader, r.blobs, projectID, traceID)
}

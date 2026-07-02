package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/agentmesh/agentmesh/services/collector/internal/blobstore"
	"github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/span"
)

func newTestSpan(t *testing.T) span.Span {
	t.Helper()
	projectID := mustProjectID(t)
	_, traceID, spanID := wellFormedSpan(t, projectID)
	return span.Span{
		ProjectID: projectID,
		TraceID:   traceID,
		SpanID:    spanID,
		Kind:      span.KindLLMCall,
		Name:      "gpt-4.1",
		Status:    span.StatusOK,
	}
}

func TestOffloadLeavesSmallPayloadInline(t *testing.T) {
	s := newTestSpan(t)
	s.Input = span.Payload{Inline: "small"}
	blobStore := &fakeBlobStore{}

	if err := NewOffloader(blobStore).Offload(context.Background(), &s); err != nil {
		t.Fatalf("Offload: %v", err)
	}
	if s.Input.Inline != "small" {
		t.Fatalf("Input.Inline = %q, want unchanged", s.Input.Inline)
	}
	if len(blobStore.puts) != 0 {
		t.Fatalf("blobStore received %d Put calls, want 0 for a small payload", len(blobStore.puts))
	}
}

func TestOffloadMovesOversizedInputAndOutput(t *testing.T) {
	s := newTestSpan(t)
	huge := strings.Repeat("x", inlinePayloadLimitBytes)
	s.Input = span.Payload{Inline: huge}
	s.Output = span.Payload{Inline: huge}
	blobStore := &fakeBlobStore{}

	if err := NewOffloader(blobStore).Offload(context.Background(), &s); err != nil {
		t.Fatalf("Offload: %v", err)
	}
	if s.Input.Inline != "" || s.Input.BlobRef == "" {
		t.Fatalf("Input = %+v, want cleared inline and a set BlobRef", s.Input)
	}
	if s.Output.Inline != "" || s.Output.BlobRef == "" {
		t.Fatalf("Output = %+v, want cleared inline and a set BlobRef", s.Output)
	}
	if len(blobStore.puts) != 2 {
		t.Fatalf("blobStore received %d Put calls, want 2 (input + output)", len(blobStore.puts))
	}
}

func TestOffloadIgnoresPayloadAlreadyUsingBlobRef(t *testing.T) {
	s := newTestSpan(t)
	s.Input = span.Payload{BlobRef: "s3://agentmesh-blobs/already/there.bin"}
	blobStore := &fakeBlobStore{}

	if err := NewOffloader(blobStore).Offload(context.Background(), &s); err != nil {
		t.Fatalf("Offload: %v", err)
	}
	if len(blobStore.puts) != 0 {
		t.Fatalf("blobStore received %d Put calls, want 0 for an already-blob-ref payload", len(blobStore.puts))
	}
}

func TestOffloadPropagatesBlobStoreError(t *testing.T) {
	s := newTestSpan(t)
	s.Input = span.Payload{Inline: strings.Repeat("x", inlinePayloadLimitBytes)}
	blobStore := &fakeBlobStore{err: errors.New(errors.CodeUnavailable, "minio down")}

	err := NewOffloader(blobStore).Offload(context.Background(), &s)
	if err == nil {
		t.Fatal("Offload: want error when the blob store Put fails, got nil")
	}
}

func TestNeedsOffloadThreshold(t *testing.T) {
	underLimit := span.Payload{Inline: strings.Repeat("x", inlinePayloadLimitBytes-1)}
	atLimit := span.Payload{Inline: strings.Repeat("x", inlinePayloadLimitBytes)}
	blobRef := span.Payload{BlobRef: "ref"}

	if needsOffload(underLimit) {
		t.Fatal("needsOffload(underLimit) = true, want false")
	}
	if !needsOffload(atLimit) {
		t.Fatal("needsOffload(atLimit) = false, want true (>= is the documented threshold)")
	}
	if needsOffload(blobRef) {
		t.Fatal("needsOffload(blobRef) = true, want false: already offloaded")
	}
}

// Compile-time assertion that *blobstore.Client actually satisfies
// BlobStore — the exact type mismatch this interface was designed to
// catch at build time rather than silently failing to wire in main.go.
var _ BlobStore = (*blobstore.Client)(nil)

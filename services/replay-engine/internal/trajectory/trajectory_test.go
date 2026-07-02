package trajectory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
)

type fakeSpanReader struct {
	spans []span.Span
	err   error
}

func (f *fakeSpanReader) GetTraceSpans(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID) ([]span.Span, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.spans, nil
}

type fakeBlobStore struct {
	objects map[string]string
}

func (f *fakeBlobStore) Get(ctx context.Context, key string) ([]byte, error) {
	data, ok := f.objects[key]
	if !ok {
		return nil, errors.New("object not found: " + key)
	}
	return []byte(data), nil
}

func mustProjectID(t *testing.T) ids.ProjectID {
	t.Helper()
	id, err := ids.NewProjectID()
	if err != nil {
		t.Fatalf("NewProjectID: %v", err)
	}
	return id
}

func mustTraceID(t *testing.T) ids.TraceID {
	t.Helper()
	id, err := ids.NewTraceID()
	if err != nil {
		t.Fatalf("NewTraceID: %v", err)
	}
	return id
}

func mustSpanID(t *testing.T) ids.SpanID {
	t.Helper()
	id, err := ids.NewSpanID()
	if err != nil {
		t.Fatalf("NewSpanID: %v", err)
	}
	return id
}

func TestResolvePayloadReturnsInlineDirectly(t *testing.T) {
	blobs := &fakeBlobStore{}
	got, err := ResolvePayload(context.Background(), blobs, span.Payload{Inline: "small value"})
	if err != nil {
		t.Fatalf("ResolvePayload: %v", err)
	}
	if got != "small value" {
		t.Fatalf("ResolvePayload = %q, want %q", got, "small value")
	}
}

func TestResolvePayloadFetchesFromBlobStore(t *testing.T) {
	blobs := &fakeBlobStore{objects: map[string]string{"proj/trace/span/output.bin": "huge value"}}
	got, err := ResolvePayload(context.Background(), blobs, span.Payload{BlobRef: "proj/trace/span/output.bin"})
	if err != nil {
		t.Fatalf("ResolvePayload: %v", err)
	}
	if got != "huge value" {
		t.Fatalf("ResolvePayload = %q, want %q", got, "huge value")
	}
}

func TestResolvePayloadReturnsEmptyForEmptyPayload(t *testing.T) {
	blobs := &fakeBlobStore{}
	got, err := ResolvePayload(context.Background(), blobs, span.Payload{})
	if err != nil {
		t.Fatalf("ResolvePayload: %v", err)
	}
	if got != "" {
		t.Fatalf("ResolvePayload = %q, want empty", got)
	}
}

func TestResolvePayloadPropagatesBlobStoreError(t *testing.T) {
	blobs := &fakeBlobStore{objects: map[string]string{}}
	_, err := ResolvePayload(context.Background(), blobs, span.Payload{BlobRef: "missing.bin"})
	if err == nil {
		t.Fatal("ResolvePayload: want error for a missing blob, got nil")
	}
}

func TestReconstructReturnsErrEmptyTraceForNoSpans(t *testing.T) {
	reader := &fakeSpanReader{spans: nil}
	blobs := &fakeBlobStore{}
	_, err := Reconstruct(context.Background(), reader, blobs, mustProjectID(t), mustTraceID(t))
	if err != ErrEmptyTrace {
		t.Fatalf("Reconstruct error = %v, want ErrEmptyTrace", err)
	}
}

func TestReconstructPropagatesReaderError(t *testing.T) {
	wantErr := errors.New("clickhouse down")
	reader := &fakeSpanReader{err: wantErr}
	blobs := &fakeBlobStore{}
	_, err := Reconstruct(context.Background(), reader, blobs, mustProjectID(t), mustTraceID(t))
	if err != wantErr {
		t.Fatalf("Reconstruct error = %v, want %v", err, wantErr)
	}
}

func TestReconstructResolvesInlineAndBlobPayloadsInOrder(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	span1 := span.Span{
		ProjectID: projectID, TraceID: traceID, SpanID: mustSpanID(t),
		Kind: span.KindToolCall, Name: "search", Status: span.StatusOK,
		StartTime: time.Now(),
		Input:     span.Payload{Inline: "query1"},
		Output:    span.Payload{BlobRef: "blob-key-1"},
	}
	span2 := span.Span{
		ProjectID: projectID, TraceID: traceID, SpanID: mustSpanID(t),
		Kind: span.KindLLMCall, Name: "gpt-4.1", Status: span.StatusOK,
		StartTime: time.Now().Add(time.Second),
		Input:     span.Payload{Inline: "prompt2"},
		Output:    span.Payload{Inline: "completion2"},
	}
	reader := &fakeSpanReader{spans: []span.Span{span1, span2}}
	blobs := &fakeBlobStore{objects: map[string]string{"blob-key-1": "large search result"}}

	steps, err := Reconstruct(context.Background(), reader, blobs, projectID, traceID)
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	if steps[0].ResolvedInput != "query1" || steps[0].ResolvedOutput != "large search result" {
		t.Fatalf("steps[0] = %+v, want resolved blob output", steps[0])
	}
	if steps[1].ResolvedInput != "prompt2" || steps[1].ResolvedOutput != "completion2" {
		t.Fatalf("steps[1] = %+v, want resolved inline payloads", steps[1])
	}
}

func TestReconstructPropagatesBlobResolutionError(t *testing.T) {
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	badSpan := span.Span{
		ProjectID: projectID, TraceID: traceID, SpanID: mustSpanID(t),
		Kind: span.KindToolCall, Name: "search", Status: span.StatusOK,
		StartTime: time.Now(),
		Output:    span.Payload{BlobRef: "missing-blob"},
	}
	reader := &fakeSpanReader{spans: []span.Span{badSpan}}
	blobs := &fakeBlobStore{objects: map[string]string{}}

	_, err := Reconstruct(context.Background(), reader, blobs, projectID, traceID)
	if err == nil {
		t.Fatal("Reconstruct: want error when a blob-backed payload cannot be fetched")
	}
}

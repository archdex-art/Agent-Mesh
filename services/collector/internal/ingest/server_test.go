package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/agentmesh/agentmesh/services/collector/internal/blobstore"
	"github.com/agentmesh/agentmesh/shared/authkeys"
	amerrors "github.com/agentmesh/agentmesh/shared/errors"
	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/agentmesh/agentmesh/shared/span"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeAuthStore struct {
	record authkeys.Record
	err    error
}

func (f *fakeAuthStore) LookupByHash(ctx context.Context, hashedKey string) (authkeys.Record, error) {
	if f.err != nil {
		return authkeys.Record{}, f.err
	}
	return f.record, nil
}

type fakeSpanWriter struct {
	written []span.Span
	err     error
}

func (f *fakeSpanWriter) WriteBatch(ctx context.Context, spans []span.Span) error {
	if f.err != nil {
		return f.err
	}
	f.written = append(f.written, spans...)
	return nil
}

// fakeBlobStore satisfies BlobStore without any real object storage,
// letting server tests exercise the offload wiring without live infra
// (Phase 3's "independently testable" standard).
type fakeBlobStore struct {
	puts []string // recorded "kind:name" for each Put call
	err  error
}

func (f *fakeBlobStore) Put(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID, spanID ids.SpanID, kind blobstore.PayloadKind, data []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	key := blobstore.Key(projectID, traceID, spanID, kind)
	f.puts = append(f.puts, key)
	return key, nil
}

func withAPIKey(ctx context.Context, key string) context.Context {
	md := metadata.New(map[string]string{apiKeyMetadataKey: key})
	return metadata.NewIncomingContext(ctx, md)
}

func TestExportRejectsMissingAPIKey(t *testing.T) {
	srv := NewServer(&fakeAuthStore{}, NewDecoder(), NewOffloader(&fakeBlobStore{}), &fakeSpanWriter{})
	_, err := srv.Export(context.Background(), &collectorpb.ExportTraceServiceRequest{})

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("Export error = %v, want Unauthenticated status", err)
	}
}

func TestExportRejectsInvalidAPIKey(t *testing.T) {
	authStore := &fakeAuthStore{err: authkeys.ErrMalformedKey}
	srv := NewServer(authStore, NewDecoder(), NewOffloader(&fakeBlobStore{}), &fakeSpanWriter{})

	ctx := withAPIKey(context.Background(), "am_live_wrongkey")
	_, err := srv.Export(ctx, &collectorpb.ExportTraceServiceRequest{})

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Fatalf("Export error = %v, want Unauthenticated status", err)
	}
}

func TestExportRejectsReadOnlyKey(t *testing.T) {
	projectID := mustProjectID(t)
	authStore := &fakeAuthStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleRead}}
	srv := NewServer(authStore, NewDecoder(), NewOffloader(&fakeBlobStore{}), &fakeSpanWriter{})

	ctx := withAPIKey(context.Background(), "am_read_somekey1234567")
	_, err := srv.Export(ctx, &collectorpb.ExportTraceServiceRequest{})

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		t.Fatalf("Export error = %v, want PermissionDenied status", err)
	}
}

func TestExportSucceedsAndWritesSpans(t *testing.T) {
	projectID := mustProjectID(t)
	authStore := &fakeAuthStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleIngest}}
	writer := &fakeSpanWriter{}
	srv := NewServer(authStore, NewDecoder(), NewOffloader(&fakeBlobStore{}), writer)

	otlpSpan, _, _ := wellFormedSpan(t, projectID)
	req := &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{otlpSpan}}}},
		},
	}

	ctx := withAPIKey(context.Background(), "am_live_validkey1234567")
	resp, err := srv.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if resp.PartialSuccess != nil {
		t.Fatalf("expected no partial success, got %+v", resp.PartialSuccess)
	}
	if len(writer.written) != 1 {
		t.Fatalf("writer received %d spans, want 1", len(writer.written))
	}
}

func TestExportReportsPartialSuccessForMixedBatch(t *testing.T) {
	projectID := mustProjectID(t)
	authStore := &fakeAuthStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleIngest}}
	writer := &fakeSpanWriter{}
	srv := NewServer(authStore, NewDecoder(), NewOffloader(&fakeBlobStore{}), writer)

	goodSpan, _, _ := wellFormedSpan(t, projectID)
	badSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.Attributes = append(s.Attributes, &commonpb.KeyValue{}) // malformed: no key
		s.TraceId = []byte{1}                                     // force a decode error
	})

	req := &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{goodSpan, badSpan}}}},
		},
	}

	ctx := withAPIKey(context.Background(), "am_live_validkey1234567")
	resp, err := srv.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if resp.PartialSuccess == nil {
		t.Fatal("expected PartialSuccess to be set for a mixed-validity batch")
	}
	if resp.PartialSuccess.RejectedSpans != 1 {
		t.Fatalf("RejectedSpans = %d, want 1", resp.PartialSuccess.RejectedSpans)
	}
	if len(writer.written) != 1 {
		t.Fatalf("writer received %d spans, want 1 (only the valid one)", len(writer.written))
	}
}

func TestExportPropagatesWriterUnavailableAsGRPCUnavailable(t *testing.T) {
	projectID := mustProjectID(t)
	authStore := &fakeAuthStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleIngest}}
	writer := &fakeSpanWriter{err: unavailableErr()}
	srv := NewServer(authStore, NewDecoder(), NewOffloader(&fakeBlobStore{}), writer)

	otlpSpan, _, _ := wellFormedSpan(t, projectID)
	req := &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{otlpSpan}}}},
		},
	}

	ctx := withAPIKey(context.Background(), "am_live_validkey1234567")
	_, err := srv.Export(ctx, req)

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unavailable {
		t.Fatalf("Export error = %v, want Unavailable status", err)
	}
}

func TestExportOffloadsOversizedPayloadBeforeWriting(t *testing.T) {
	// End-to-end proof of the corrected docs/otlp-mapping.md contract: the
	// Collector, not the exporter, performs the 4KB size-check and blob
	// offload. A span whose inline input exceeds the threshold must be
	// rewritten to carry a blob_ref before it reaches the writer.
	projectID := mustProjectID(t)
	authStore := &fakeAuthStore{record: authkeys.Record{ProjectID: projectID, Role: authkeys.RoleIngest}}
	writer := &fakeSpanWriter{}
	blobStore := &fakeBlobStore{}
	srv := NewServer(authStore, NewDecoder(), NewOffloader(blobStore), writer)

	huge := strings.Repeat("x", 10_000)
	otlpSpan, _, _ := wellFormedSpan(t, projectID, func(s *tracepb.Span) {
		s.Attributes = append(s.Attributes, strAttr("agentmesh.input.inline", huge))
	})
	req := &collectorpb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{otlpSpan}}}},
		},
	}

	ctx := withAPIKey(context.Background(), "am_live_validkey1234567")
	if _, err := srv.Export(ctx, req); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if len(blobStore.puts) != 1 {
		t.Fatalf("blobStore received %d Put calls, want 1", len(blobStore.puts))
	}
	if len(writer.written) != 1 {
		t.Fatalf("writer received %d spans, want 1", len(writer.written))
	}
	got := writer.written[0]
	if got.Input.Inline != "" {
		t.Fatal("written span still carries the inline payload, want it cleared after offload")
	}
	if got.Input.BlobRef == "" {
		t.Fatal("written span has no BlobRef after offload, want one set")
	}
}

func unavailableErr() error {
	return amerrors.New(amerrors.CodeUnavailable, "clickhouse down")
}

//go:build integration

// Run with: go test -tags integration ./internal/blobstore/... -v
// Requires a MinIO instance reachable at AGENTMESH_TEST_MINIO_ADDR (default
// localhost:9000) — matching the rigor established in Milestone 2's
// writer_integration_test.go: this proves Put/Get/Delete actually round-trip
// through a real S3-compatible store, not merely that the client library
// compiles against our usage.
package blobstore

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func testClient(t *testing.T) *Client {
	t.Helper()
	addr := os.Getenv("AGENTMESH_TEST_MINIO_ADDR")
	if addr == "" {
		addr = "localhost:9000"
	}
	mc, err := minio.New(addr, &minio.Options{
		Creds:  credentials.NewStaticV4("agentmesh", "agentmesh-dev-secret", ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("creating minio client: %v", err)
	}

	client := New(mc, "agentmesh-blobs-test")
	if err := client.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("EnsureBucket: %v", err)
	}
	return client
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

func TestPutAndGetRoundTripThroughRealMinIO(t *testing.T) {
	client := testClient(t)
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	spanID := mustSpanID(t)

	payload := []byte(`{"prompt": "this is a large simulated tool-call payload that would exceed the 4KB inline threshold in a real span"}`)

	key, err := client.Put(context.Background(), projectID, traceID, spanID, PayloadInput, payload)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	wantKey := Key(projectID, traceID, spanID, PayloadInput)
	if key != wantKey {
		t.Fatalf("Put returned key %q, want %q", key, wantKey)
	}

	got, err := client.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("Get returned %q, want %q", got, payload)
	}
}

func TestKeyLayoutMatchesSystemDesignSpec(t *testing.T) {
	// System Design.md §2.3: s3://agentmesh-blobs/{project_id}/{trace_id}/{span_id}/{input|output}.bin
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	spanID := mustSpanID(t)

	inputKey := Key(projectID, traceID, spanID, PayloadInput)
	wantInput := projectID.String() + "/" + traceID.String() + "/" + spanID.String() + "/input.bin"
	if inputKey != wantInput {
		t.Fatalf("input key = %q, want %q", inputKey, wantInput)
	}

	outputKey := Key(projectID, traceID, spanID, PayloadOutput)
	wantOutput := projectID.String() + "/" + traceID.String() + "/" + spanID.String() + "/output.bin"
	if outputKey != wantOutput {
		t.Fatalf("output key = %q, want %q", outputKey, wantOutput)
	}
}

func TestDeleteRemovesObject(t *testing.T) {
	client := testClient(t)
	projectID := mustProjectID(t)
	traceID := mustTraceID(t)
	spanID := mustSpanID(t)

	key, err := client.Put(context.Background(), projectID, traceID, spanID, PayloadOutput, []byte("data to delete"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := client.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = client.Get(context.Background(), key)
	if err == nil {
		t.Fatal("Get succeeded after Delete, want an error (object should no longer exist)")
	}
}

func TestGetNonexistentKeyReturnsError(t *testing.T) {
	client := testClient(t)
	_, err := client.Get(context.Background(), "does/not/exist/input.bin")
	if err == nil {
		t.Fatal("Get succeeded for a nonexistent key, want an error")
	}
}

func TestEnsureBucketIsIdempotent(t *testing.T) {
	client := testClient(t)
	// testClient already calls EnsureBucket once; calling it again must
	// not error (the "bucket already exists" case).
	if err := client.EnsureBucket(context.Background()); err != nil {
		t.Fatalf("second EnsureBucket call failed: %v", err)
	}
}

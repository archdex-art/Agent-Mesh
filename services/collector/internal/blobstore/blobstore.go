// Package blobstore implements the Collector's S3-compatible object
// storage client for large tool-I/O payloads, per System Design.md §2.3's
// key layout (`s3://agentmesh-blobs/{project_id}/{trace_id}/{span_id}/{input|output}.bin`)
// and Architecture.md §14 ("MinIO for self-hosted, S3 for cloud").
//
// docs/otlp-mapping.md's "Payload size threshold" section (corrected)
// makes clear that the 4KB inline/blob-ref decision is the *Collector's*
// job, not the exporter's: an SDK always sends the full payload inline,
// and the Collector — never the customer's agent process — holds the
// object-storage write credentials. This package therefore exposes two
// independent capabilities:
//   - Put: called by ingest.Offloader (internal/ingest/offload.go) on
//     ingestion, after decode and before persistence, for any payload at
//     or above the inline threshold.
//   - Get: called by the Replay Engine (System Design.md §4's sequence
//     diagram: "Replay->>Blob: fetch large input/output payloads") to
//     retrieve a payload referenced by a span's blob_ref for
//     reconstruction/replay.
package blobstore

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/agentmesh/agentmesh/shared/ids"
	"github.com/minio/minio-go/v7"
)

// PayloadKind distinguishes an input from an output payload, matching the
// two file names in System Design.md §2.3's key layout.
type PayloadKind string

const (
	PayloadInput  PayloadKind = "input"
	PayloadOutput PayloadKind = "output"
)

// Client is a thin wrapper around the MinIO/S3 SDK, scoped to the single
// bucket AgentMesh uses (System Design.md §2.3: "agentmesh-blobs").
type Client struct {
	minio  *minio.Client
	bucket string
}

// New returns a Client backed by mc, targeting bucket. The bucket's
// existence/creation is the caller's responsibility (handled once at
// deployment time, not per-request) — see EnsureBucket for a convenience
// helper callers can invoke during service startup.
func New(mc *minio.Client, bucket string) *Client {
	return &Client{minio: mc, bucket: bucket}
}

// EnsureBucket creates the configured bucket if it does not already exist.
// Idempotent — safe to call on every service startup.
func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.minio.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("blobstore: checking bucket %q exists: %w", c.bucket, err)
	}
	if exists {
		return nil
	}
	if err := c.minio.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("blobstore: creating bucket %q: %w", c.bucket, err)
	}
	return nil
}

// Key builds the object-store key for a given project/trace/span payload,
// per System Design.md §2.3's exact layout.
func Key(projectID ids.ProjectID, traceID ids.TraceID, spanID ids.SpanID, kind PayloadKind) string {
	return fmt.Sprintf("%s/%s/%s/%s.bin", projectID.String(), traceID.String(), spanID.String(), kind)
}

// Put uploads data under the key computed by Key, returning that key as
// the blob_ref to store on the span (docs/otlp-mapping.md's
// agentmesh.input.blob_ref / agentmesh.output.blob_ref attributes).
func (c *Client) Put(ctx context.Context, projectID ids.ProjectID, traceID ids.TraceID, spanID ids.SpanID, kind PayloadKind, data []byte) (string, error) {
	key := Key(projectID, traceID, spanID, kind)
	_, err := c.minio.PutObject(ctx, c.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return "", fmt.Errorf("blobstore: uploading %s: %w", key, err)
	}
	return key, nil
}

// Get retrieves the payload stored at key (a blob_ref value previously
// returned by Put or recorded by an SDK exporter).
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := c.minio.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("blobstore: fetching %s: %w", key, err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("blobstore: reading %s: %w", key, err)
	}
	return data, nil
}

// Delete removes the object at key. Used by the retention/compaction job
// (Architecture.md §8) when a project's retention window expires.
func (c *Client) Delete(ctx context.Context, key string) error {
	if err := c.minio.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("blobstore: deleting %s: %w", key, err)
	}
	return nil
}

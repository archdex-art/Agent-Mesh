package store

import (
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

// BlobStoreClient is the Replay Engine's read-only view of the
// S3-compatible object store the Collector writes large tool-I/O
// payloads to (Architecture.md §14). Unlike
// services/collector/internal/blobstore.Client, this type exposes only
// Get — the Replay Engine reads recorded payloads for reconstruction
// (System Design.md §4: "Replay->>Blob: fetch large input/output
// payloads") but never writes; only the Collector's ingestion path
// assigns blob_ref values.
type BlobStoreClient struct {
	minio  *minio.Client
	bucket string
}

// NewBlobStoreClient returns a BlobStoreClient backed by mc, targeting
// bucket. The bucket is expected to already exist (created by the
// Collector's EnsureBucket at its own startup) — the Replay Engine has no
// reason to create it.
func NewBlobStoreClient(mc *minio.Client, bucket string) *BlobStoreClient {
	return &BlobStoreClient{minio: mc, bucket: bucket}
}

// Get retrieves the payload stored at key (a blob_ref value read from a
// span row), satisfying trajectory.BlobStore.
func (c *BlobStoreClient) Get(ctx context.Context, key string) ([]byte, error) {
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

package asset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
)

// objectPrefix is the key prefix under which staged assets are written. Staging
// bucket lifecycle (retention/GC) is a non-goal (the bucket is provisioned
// externally); this prefix just keeps Mizan's uploads namespaced.
const objectPrefix = "mizan-staging/"

// uploader is the storage seam GCSStager writes through. It exists so unit
// tests can exercise Stage's control flow (validation, pass-through, key
// derivation) without a network or GCS credentials.
type uploader interface {
	// upload writes data to object within the configured bucket, tagging it
	// with contentType, and returns the resulting gs:// URI.
	upload(ctx context.Context, object, contentType string, data io.Reader) (string, error)
}

// GCSStager stages local assets to a GCS bucket and returns gs:// URIs for the
// native eval path. It is safe for concurrent use.
type GCSStager struct {
	bucket string
	up     uploader
}

// Ensure GCSStager satisfies the Stager contract.
var _ Stager = (*GCSStager)(nil)

// NewGCSStager constructs a GCSStager for the given bucket (a bare bucket name,
// e.g. the gs://-stripped config.StagingBucket). It returns ErrNoBucket when
// bucket is empty so callers can surface a clear message when a multimodal eval
// is attempted with no StagingBucket configured.
func NewGCSStager(ctx context.Context, bucket string) (*GCSStager, error) {
	bucket = strings.TrimPrefix(strings.TrimSpace(bucket), "gs://")
	bucket = strings.Trim(bucket, "/")
	if bucket == "" {
		return nil, ErrNoBucket
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("asset: create GCS client: %w", err)
	}
	return &GCSStager{
		bucket: bucket,
		up:     &gcsUploader{client: client, bucket: bucket},
	}, nil
}

// Stage implements Stager. A gs:// input is returned as-is (with MIME resolved
// from the override or the object extension); a LocalPath input is MIME-detected
// and uploaded, and the resulting gs:// URI is returned.
func (s *GCSStager) Stage(ctx context.Context, in StageInput) (StageResult, error) {
	if err := in.validate(); err != nil {
		return StageResult{}, err
	}

	// Already staged: pass the gs:// URI through untouched.
	if in.GCSUri != "" {
		return StageResult{
			GCSUri: in.GCSUri,
			MIME:   resolveMIME(in.MIME, gcsObjectName(in.GCSUri), nil),
		}, nil
	}

	// Local file: requires a bucket to upload into.
	if s.bucket == "" || s.up == nil {
		return StageResult{}, ErrNoBucket
	}

	data, err := os.ReadFile(in.LocalPath)
	if err != nil {
		return StageResult{}, fmt.Errorf("asset: read %q: %w", in.LocalPath, err)
	}

	mimeType := resolveMIME(in.MIME, in.LocalPath, data)
	object := objectKey(in.LocalPath, data)

	uri, err := s.up.upload(ctx, object, mimeType, bytes.NewReader(data))
	if err != nil {
		return StageResult{}, fmt.Errorf("asset: upload %q to gs://%s/%s: %w", in.LocalPath, s.bucket, object, err)
	}
	return StageResult{GCSUri: uri, MIME: mimeType}, nil
}

// objectKey returns a content-addressed object name that preserves the source
// extension. Content addressing gives idempotent dedup for free: re-staging the
// same bytes overwrites the same object.
func objectKey(localPath string, data []byte) string {
	sum := sha256.Sum256(data)
	ext := strings.ToLower(filepath.Ext(localPath))
	return objectPrefix + hex.EncodeToString(sum[:]) + ext
}

// gcsUploader is the production uploader backed by cloud.google.com/go/storage.
type gcsUploader struct {
	client *storage.Client
	bucket string
}

func (u *gcsUploader) upload(ctx context.Context, object, contentType string, data io.Reader) (string, error) {
	w := u.client.Bucket(u.bucket).Object(object).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := io.Copy(w, data); err != nil {
		_ = w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("gs://%s/%s", u.bucket, object), nil
}

// Close releases the underlying storage client. It is safe to call on a stager
// constructed without a real client (e.g. in tests).
func (s *GCSStager) Close() error {
	if u, ok := s.up.(*gcsUploader); ok && u.client != nil {
		return u.client.Close()
	}
	return nil
}

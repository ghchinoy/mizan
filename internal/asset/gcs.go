// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package asset

import (
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

// maxAssetBytes caps the size of a local asset Mizan will read and upload
// (512 MiB). It is defense-in-depth against a single oversized or crafted
// LocalPath (e.g. a multi-GB media file, or a sparse file) exhausting memory or
// hanging a run — it is NOT a sandbox (see Stage). Tune if legitimate media
// grows past this.
const maxAssetBytes int64 = 512 << 20

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
	return s.stageLocal(ctx, in)
}

// stageLocal reads, content-addresses, and streams a local regular file to the
// staging bucket.
//
// Trust boundary: in P1, LocalPath is user-supplied CLI input — a CLI user can
// legitimately reference any file they can read, so this is NOT a sandbox and
// does NOT confine paths to a base directory. The guards below are
// defense-in-depth / DoS protection for when WI-4 wires (potentially
// dataset-derived) rows into LocalPath: symlinks are resolved and the target
// must be a regular file (rejecting dirs, devices, FIFOs and sockets such as
// /dev/zero, which would otherwise read without bound), and the asset size is
// capped. The file is streamed straight into the GCS writer so a large asset
// never fully lands in memory.
func (s *GCSStager) stageLocal(ctx context.Context, in StageInput) (StageResult, error) {
	// Resolve symlinks and operate on the real path.
	resolved, err := filepath.EvalSymlinks(in.LocalPath)
	if err != nil {
		return StageResult{}, fmt.Errorf("asset: resolve %q: %w", in.LocalPath, err)
	}
	fi, err := os.Lstat(resolved)
	if err != nil {
		return StageResult{}, fmt.Errorf("asset: stat %q: %w", in.LocalPath, err)
	}
	if !fi.Mode().IsRegular() {
		return StageResult{}, fmt.Errorf("asset: %q is not a regular file (mode %s); refusing to read", in.LocalPath, fi.Mode().Type())
	}
	if fi.Size() > maxAssetBytes {
		return StageResult{}, fmt.Errorf("asset: %q is %d bytes, exceeds the %d-byte cap", in.LocalPath, fi.Size(), maxAssetBytes)
	}

	f, err := os.Open(resolved)
	if err != nil {
		return StageResult{}, fmt.Errorf("asset: open %q: %w", in.LocalPath, err)
	}
	defer f.Close()

	// Read a bounded header for MIME sniffing, then hash the full content in a
	// streaming pass (content addressing needs every byte, but they never all
	// reside in memory at once).
	header := make([]byte, sniffLen)
	hn, err := io.ReadFull(f, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return StageResult{}, fmt.Errorf("asset: read %q: %w", in.LocalPath, err)
	}
	header = header[:hn]

	h := sha256.New()
	h.Write(header)
	if _, err := io.Copy(h, io.LimitReader(f, maxAssetBytes+1)); err != nil {
		return StageResult{}, fmt.Errorf("asset: hash %q: %w", in.LocalPath, err)
	}
	sum := h.Sum(nil)

	// Use the caller-supplied name (not the symlink-resolved path) for the MIME
	// extension and the preserved object-key extension: that is the name the
	// user intends, and content sniffing (from header) still wins on mismatch.
	mimeType := resolveMIME(in.MIME, in.LocalPath, header)
	object := objectKey(in.LocalPath, sum)

	// Rewind and stream the upload from the file rather than buffering it.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return StageResult{}, fmt.Errorf("asset: seek %q: %w", in.LocalPath, err)
	}
	uri, err := s.up.upload(ctx, object, mimeType, io.LimitReader(f, maxAssetBytes))
	if err != nil {
		return StageResult{}, fmt.Errorf("asset: upload %q to gs://%s/%s: %w", in.LocalPath, s.bucket, object, err)
	}
	return StageResult{GCSUri: uri, MIME: mimeType}, nil
}

// objectKey returns a content-addressed object name (from the sha256 of the
// asset bytes) that preserves a sanitized source extension. Content addressing
// gives idempotent dedup for free: re-staging the same bytes overwrites the same
// object.
func objectKey(localPath string, sum []byte) string {
	return objectPrefix + hex.EncodeToString(sum) + safeExt(localPath)
}

// safeExt returns the source file extension only when it is a recognized media
// extension (a key of extMIME); otherwise it returns "". The key core is
// already content-addressed (sha256, hex [0-9a-f]) and filepath.Ext cannot
// contain a path separator, so this is not a traversal fix — it keeps arbitrary
// non-separator bytes (spaces, control chars, newlines, unicode) out of the
// object name. A dropped extension is cosmetic.
func safeExt(localPath string) string {
	ext := strings.ToLower(filepath.Ext(localPath))
	if _, ok := extMIME[ext]; ok {
		return ext
	}
	return ""
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

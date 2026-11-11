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

// Package gcs provides the Cloud Storage-backed asset.Stager implementation
// used by the CLI/MCP composition root for Vertex native EvaluateInstances runs.
package gcs

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

	"github.com/ghchinoy/mizan/internal/asset"
)

// objectPrefix is the key prefix under which staged assets are written.
const objectPrefix = "mizan-staging/"

// maxAssetBytes caps the size of a local asset Mizan will read and upload
// (512 MiB).
const maxAssetBytes int64 = 512 << 20

type uploader interface {
	upload(ctx context.Context, object, contentType string, data io.Reader) (string, error)
}

// GCSStager stages local assets to a GCS bucket and returns gs:// URIs for the
// native eval path. It is safe for concurrent use.
type GCSStager struct {
	bucket string
	up     uploader
}

var _ asset.Stager = (*GCSStager)(nil)

// NewGCSStager constructs a GCSStager for the given bucket (a bare bucket name,
// e.g. the gs://-stripped config.StagingBucket). It returns asset.ErrNoBucket
// when bucket is empty.
func NewGCSStager(ctx context.Context, bucket string) (*GCSStager, error) {
	bucket = strings.TrimPrefix(strings.TrimSpace(bucket), "gs://")
	bucket = strings.Trim(bucket, "/")
	if bucket == "" {
		return nil, asset.ErrNoBucket
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("asset/gcs: create GCS client: %w", err)
	}
	return &GCSStager{
		bucket: bucket,
		up:     &gcsUploader{client: client, bucket: bucket},
	}, nil
}

// Stage implements asset.Stager. A gs:// input is returned as-is (with MIME
// resolved from the override or the object extension); a LocalPath input is
// MIME-detected and uploaded, and the resulting gs:// URI is returned.
func (s *GCSStager) Stage(ctx context.Context, in asset.StageInput) (asset.StageResult, error) {
	if err := in.Validate(); err != nil {
		return asset.StageResult{}, err
	}

	if in.GCSUri != "" {
		return asset.StageResult{
			GCSUri: in.GCSUri,
			MIME:   asset.ResolveMIME(in.MIME, asset.GCSObjectName(in.GCSUri), nil),
		}, nil
	}

	if s.bucket == "" || s.up == nil {
		return asset.StageResult{}, asset.ErrNoBucket
	}
	return s.stageLocal(ctx, in)
}

func (s *GCSStager) stageLocal(ctx context.Context, in asset.StageInput) (asset.StageResult, error) {
	resolved, err := filepath.EvalSymlinks(in.LocalPath)
	if err != nil {
		return asset.StageResult{}, fmt.Errorf("asset: resolve %q: %w", in.LocalPath, err)
	}
	fi, err := os.Lstat(resolved)
	if err != nil {
		return asset.StageResult{}, fmt.Errorf("asset: stat %q: %w", in.LocalPath, err)
	}
	if !fi.Mode().IsRegular() {
		return asset.StageResult{}, fmt.Errorf("asset: %q is not a regular file (mode %s); refusing to read", in.LocalPath, fi.Mode().Type())
	}
	if fi.Size() > maxAssetBytes {
		return asset.StageResult{}, fmt.Errorf("asset: %q is %d bytes, exceeds the %d-byte cap", in.LocalPath, fi.Size(), maxAssetBytes)
	}

	f, err := os.Open(resolved)
	if err != nil {
		return asset.StageResult{}, fmt.Errorf("asset: open %q: %w", in.LocalPath, err)
	}
	defer f.Close()

	header := make([]byte, asset.SniffLen)
	hn, err := io.ReadFull(f, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return asset.StageResult{}, fmt.Errorf("asset: read %q: %w", in.LocalPath, err)
	}
	header = header[:hn]

	h := sha256.New()
	h.Write(header)
	if _, err := io.Copy(h, io.LimitReader(f, maxAssetBytes+1)); err != nil {
		return asset.StageResult{}, fmt.Errorf("asset: hash %q: %w", in.LocalPath, err)
	}
	sum := h.Sum(nil)

	mimeType := asset.ResolveMIME(in.MIME, in.LocalPath, header)
	object := objectKey(in.LocalPath, sum)

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return asset.StageResult{}, fmt.Errorf("asset: seek %q: %w", in.LocalPath, err)
	}
	uri, err := s.up.upload(ctx, object, mimeType, io.LimitReader(f, maxAssetBytes))
	if err != nil {
		return asset.StageResult{}, fmt.Errorf("asset: upload %q to gs://%s/%s: %w", in.LocalPath, s.bucket, object, err)
	}
	return asset.StageResult{GCSUri: uri, MIME: mimeType}, nil
}

func objectKey(localPath string, sum []byte) string {
	return objectPrefix + hex.EncodeToString(sum) + asset.SafeExt(localPath)
}

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

// Close releases the underlying storage client.
func (s *GCSStager) Close() error {
	if u, ok := s.up.(*gcsUploader); ok && u.client != nil {
		return u.client.Close()
	}
	return nil
}

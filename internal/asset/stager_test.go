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
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeUploader records uploads in memory so Stage's control flow can be tested
// without a network or GCS credentials.
type fakeUploader struct {
	mu      sync.Mutex
	bucket  string
	objects map[string]uploadRecord
}

type uploadRecord struct {
	contentType string
	data        []byte
}

func newFakeUploader(bucket string) *fakeUploader {
	return &fakeUploader{bucket: bucket, objects: map[string]uploadRecord{}}
}

func (f *fakeUploader) upload(_ context.Context, object, contentType string, data io.Reader) (string, error) {
	b, err := io.ReadAll(data)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[object] = uploadRecord{contentType: contentType, data: b}
	return "gs://" + f.bucket + "/" + object, nil
}

func newTestStager(bucket string) (*GCSStager, *fakeUploader) {
	up := newFakeUploader(bucket)
	return &GCSStager{bucket: bucket, up: up}, up
}

func TestStageInputValidation(t *testing.T) {
	s, _ := newTestStager("bucket")
	ctx := context.Background()

	tests := []struct {
		name string
		in   StageInput
	}{
		{"neither", StageInput{}},
		{"both", StageInput{LocalPath: "/tmp/x.png", GCSUri: "gs://b/o.png"}},
		{"bad-gcs-scheme", StageInput{GCSUri: "http://example.com/o.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Stage(ctx, tt.in); err == nil {
				t.Fatalf("Stage(%+v) = nil error, want error", tt.in)
			}
		})
	}
}

func TestStageGCSPassthrough(t *testing.T) {
	s, up := newTestStager("bucket")
	ctx := context.Background()

	res, err := s.Stage(ctx, StageInput{GCSUri: "gs://other-bucket/path/clip.mp4"})
	if err != nil {
		t.Fatalf("Stage(gs) error: %v", err)
	}
	if res.GCSUri != "gs://other-bucket/path/clip.mp4" {
		t.Fatalf("GCSUri = %q, want pass-through", res.GCSUri)
	}
	if res.MIME != "video/mp4" {
		t.Fatalf("MIME = %q, want video/mp4 (from extension)", res.MIME)
	}
	if len(up.objects) != 0 {
		t.Fatalf("pass-through uploaded %d objects, want 0", len(up.objects))
	}
}

func TestStageGCSPassthroughMIMEOverride(t *testing.T) {
	s, _ := newTestStager("bucket")
	res, err := s.Stage(context.Background(), StageInput{
		GCSUri: "gs://b/o", // no extension
		MIME:   "audio/mp3",
	})
	if err != nil {
		t.Fatalf("Stage error: %v", err)
	}
	if res.MIME != "audio/mpeg" {
		t.Fatalf("MIME = %q, want normalized audio/mpeg", res.MIME)
	}
}

func TestStageLocalUpload(t *testing.T) {
	s, up := newTestStager("bucket")
	ctx := context.Background()

	dir := t.TempDir()
	path := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(path, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := s.Stage(ctx, StageInput{LocalPath: path})
	if err != nil {
		t.Fatalf("Stage(local) error: %v", err)
	}
	if !strings.HasPrefix(res.GCSUri, "gs://bucket/"+objectPrefix) {
		t.Fatalf("GCSUri = %q, want gs://bucket/%s...", res.GCSUri, objectPrefix)
	}
	if !strings.HasSuffix(res.GCSUri, ".png") {
		t.Fatalf("GCSUri = %q, want preserved .png extension", res.GCSUri)
	}
	if res.MIME != "image/png" {
		t.Fatalf("MIME = %q, want image/png", res.MIME)
	}
	if len(up.objects) != 1 {
		t.Fatalf("uploaded %d objects, want 1", len(up.objects))
	}
	for obj, rec := range up.objects {
		if rec.contentType != "image/png" {
			t.Fatalf("object %q contentType = %q, want image/png", obj, rec.contentType)
		}
	}
}

// TestStageLocalUploadContentAddressed verifies the object key is derived from
// content (idempotent dedup): the same bytes map to the same object.
func TestStageLocalUploadContentAddressed(t *testing.T) {
	s, up := newTestStager("bucket")
	ctx := context.Background()
	dir := t.TempDir()

	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := os.WriteFile(a, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}

	r1, err := s.Stage(ctx, StageInput{LocalPath: a})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.Stage(ctx, StageInput{LocalPath: b})
	if err != nil {
		t.Fatal(err)
	}
	if r1.GCSUri != r2.GCSUri {
		t.Fatalf("same content produced different URIs: %q vs %q", r1.GCSUri, r2.GCSUri)
	}
	if len(up.objects) != 1 {
		t.Fatalf("content-addressed dedup failed: %d objects, want 1", len(up.objects))
	}
}

// TestStageLocalMismatchExtension: a JPEG-with-.png-name uploads with the
// correct, content-sniffed MIME so the native path won't drop it.
func TestStageLocalMismatchExtension(t *testing.T) {
	s, _ := newTestStager("bucket")
	dir := t.TempDir()
	path := filepath.Join(dir, "actually_jpeg.png")
	if err := os.WriteFile(path, jpegHead, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(context.Background(), StageInput{LocalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if res.MIME != "image/jpeg" {
		t.Fatalf("MIME = %q, want image/jpeg (content wins over extension)", res.MIME)
	}
}

func TestStageLocalMIMEOverride(t *testing.T) {
	s, _ := newTestStager("bucket")
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(path, []byte("arbitrary bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(context.Background(), StageInput{LocalPath: path, MIME: "image/webp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MIME != "image/webp" {
		t.Fatalf("MIME = %q, want image/webp (explicit override)", res.MIME)
	}
}

func TestStageLocalMissingFile(t *testing.T) {
	s, _ := newTestStager("bucket")
	if _, err := s.Stage(context.Background(), StageInput{LocalPath: "/no/such/file.png"}); err == nil {
		t.Fatal("Stage(missing file) = nil error, want error")
	}
}

func TestNewGCSStagerEmptyBucket(t *testing.T) {
	_, err := NewGCSStager(context.Background(), "")
	if !errors.Is(err, ErrNoBucket) {
		t.Fatalf("NewGCSStager(\"\") err = %v, want ErrNoBucket", err)
	}
	// Whitespace / gs:// prefix that reduces to empty is also rejected.
	if _, err := NewGCSStager(context.Background(), "gs://"); !errors.Is(err, ErrNoBucket) {
		t.Fatalf("NewGCSStager(\"gs://\") err = %v, want ErrNoBucket", err)
	}
}

// TestStageLocalNoBucket: a stager with an empty bucket rejects an upload with
// ErrNoBucket (defense-in-depth alongside the constructor check).
func TestStageLocalNoBucket(t *testing.T) {
	s := &GCSStager{bucket: "", up: nil}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.png")
	if err := os.WriteFile(path, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Stage(context.Background(), StageInput{LocalPath: path})
	if !errors.Is(err, ErrNoBucket) {
		t.Fatalf("Stage(local, no bucket) err = %v, want ErrNoBucket", err)
	}
}

func TestGCSObjectName(t *testing.T) {
	tests := map[string]string{
		"gs://bucket/path/to/file.mp4": "path/to/file.mp4",
		"gs://bucket/file.png":         "file.png",
		"gs://bucket":                  "bucket",
	}
	for in, want := range tests {
		if got := gcsObjectName(in); got != want {
			t.Fatalf("gcsObjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

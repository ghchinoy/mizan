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

package gcs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

var (
	jpegHead = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	pngHead  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0, 0, 0, 0}
	mp4Head  = []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00")
)

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
	ctx := t.Context()

	tests := []struct {
		name string
		in   asset.StageInput
	}{
		{"neither", asset.StageInput{}},
		{"both", asset.StageInput{LocalPath: "/tmp/x.png", GCSUri: "gs://b/o.png"}},
		{"bad-gcs-scheme", asset.StageInput{GCSUri: "http://example.com/o.png"}},
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
	res, err := s.Stage(t.Context(), asset.StageInput{GCSUri: "gs://other-bucket/path/clip.mp4"})
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
	res, err := s.Stage(t.Context(), asset.StageInput{
		GCSUri: "gs://b/o",
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
	ctx := t.Context()

	dir := t.TempDir()
	path := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(path, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := s.Stage(ctx, asset.StageInput{LocalPath: path})
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

func TestStageLocalUploadContentAddressed(t *testing.T) {
	s, up := newTestStager("bucket")
	ctx := t.Context()
	dir := t.TempDir()

	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := os.WriteFile(a, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}

	r1, err := s.Stage(ctx, asset.StageInput{LocalPath: a})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.Stage(ctx, asset.StageInput{LocalPath: b})
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

func TestStageLocalMismatchExtension(t *testing.T) {
	s, _ := newTestStager("bucket")
	dir := t.TempDir()
	path := filepath.Join(dir, "actually_jpeg.png")
	if err := os.WriteFile(path, jpegHead, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(t.Context(), asset.StageInput{LocalPath: path})
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
	res, err := s.Stage(t.Context(), asset.StageInput{LocalPath: path, MIME: "image/webp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MIME != "image/webp" {
		t.Fatalf("MIME = %q, want image/webp (explicit override)", res.MIME)
	}
}

func TestStageLocalMissingFile(t *testing.T) {
	s, _ := newTestStager("bucket")
	if _, err := s.Stage(t.Context(), asset.StageInput{LocalPath: "/no/such/file.png"}); err == nil {
		t.Fatal("Stage(missing file) = nil error, want error")
	}
}

func TestNewGCSStagerEmptyBucket(t *testing.T) {
	_, err := NewGCSStager(t.Context(), "")
	if !errors.Is(err, asset.ErrNoBucket) {
		t.Fatalf("NewGCSStager(\"\") err = %v, want ErrNoBucket", err)
	}
	if _, err := NewGCSStager(t.Context(), "gs://"); !errors.Is(err, asset.ErrNoBucket) {
		t.Fatalf("NewGCSStager(\"gs://\") err = %v, want ErrNoBucket", err)
	}
}

func TestStageLocalNoBucket(t *testing.T) {
	s := &GCSStager{bucket: "", up: nil}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.png")
	if err := os.WriteFile(path, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := s.Stage(t.Context(), asset.StageInput{LocalPath: path})
	if !errors.Is(err, asset.ErrNoBucket) {
		t.Fatalf("Stage(local, no bucket) err = %v, want ErrNoBucket", err)
	}
}

func TestStageLocalUnknownDefaultsOctetStream(t *testing.T) {
	s, up := newTestStager("bucket")
	path := filepath.Join(t.TempDir(), "blob.zzz")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0x03}, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(t.Context(), asset.StageInput{LocalPath: path})
	if err != nil {
		t.Fatalf("Stage(unknown) error: %v", err)
	}
	if res.MIME != "application/octet-stream" {
		t.Fatalf("MIME = %q, want application/octet-stream", res.MIME)
	}
	if got := asset.ModalityForMIME(res.MIME); got != registry.ModalityText {
		t.Fatalf("ModalityForMIME(%q) = %q, want ModalityText", res.MIME, got)
	}
	if len(up.objects) != 1 {
		t.Fatalf("uploaded %d objects, want 1", len(up.objects))
	}
}

func TestStageConcurrent(t *testing.T) {
	s, up := newTestStager("bucket")
	dir := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	uris := make([]string, n)
	for i := range n {
		path := filepath.Join(dir, "f"+string(rune('a'+i))+".png")
		body := append([]byte("\x89PNG\r\n\x1a\n"), byte(i))
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			res, err := s.Stage(t.Context(), asset.StageInput{LocalPath: p})
			errs[idx] = err
			uris[idx] = res.GCSUri
		}(i, path)
	}
	wg.Wait()
	seen := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("goroutine %d Stage error: %v", i, errs[i])
		}
		if seen[uris[i]] {
			t.Fatalf("duplicate URI %q from distinct content", uris[i])
		}
		seen[uris[i]] = true
	}
	if len(up.objects) != n {
		t.Fatalf("uploaded %d objects, want %d", len(up.objects), n)
	}
}

func TestStageLocalMIMEOverrideConflict(t *testing.T) {
	s, _ := newTestStager("bucket")
	dir := t.TempDir()
	conflict := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(conflict, jpegHead, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(t.Context(), asset.StageInput{LocalPath: conflict, MIME: "audio/mpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MIME != "image/jpeg" {
		t.Fatalf("MIME = %q, want image/jpeg", res.MIME)
	}
}

func TestStageLocalRejectsNonRegularFile(t *testing.T) {
	s, up := newTestStager("bucket")
	fifo := filepath.Join(t.TempDir(), "pipe.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	_, err := s.Stage(t.Context(), asset.StageInput{LocalPath: fifo})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Stage(FIFO) err = %v, want 'not a regular file'", err)
	}
	if len(up.objects) != 0 {
		t.Fatalf("uploaded %d objects for rejected FIFO, want 0", len(up.objects))
	}
}

func TestStageLocalRejectsOversize(t *testing.T) {
	s, up := newTestStager("bucket")
	big := filepath.Join(t.TempDir(), "big.mp4")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxAssetBytes + 1); err != nil {
		f.Close()
		t.Skipf("cannot create sparse oversize file: %v", err)
	}
	f.Close()
	_, err = s.Stage(t.Context(), asset.StageInput{LocalPath: big})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Stage(oversize) err = %v, want size-cap error", err)
	}
	if len(up.objects) != 0 {
		t.Fatalf("uploaded %d objects for oversize file, want 0", len(up.objects))
	}
}

func TestStageLocalResolvesSymlink(t *testing.T) {
	s, up := newTestStager("bucket")
	dir := t.TempDir()
	target := filepath.Join(dir, "target.png")
	if err := os.WriteFile(target, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.png")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	res, err := s.Stage(t.Context(), asset.StageInput{LocalPath: link})
	if err != nil {
		t.Fatalf("Stage(symlink) error: %v", err)
	}
	if res.MIME != "image/png" || len(up.objects) != 1 {
		t.Fatalf("MIME = %q, uploaded = %d", res.MIME, len(up.objects))
	}
}

func TestObjectKeyExtensionSanitized(t *testing.T) {
	s, _ := newTestStager("bucket")
	dir := t.TempDir()
	weird := filepath.Join(dir, "payload.exe")
	if err := os.WriteFile(weird, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(t.Context(), asset.StageInput{LocalPath: weird})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.GCSUri, ".exe") {
		t.Fatalf("GCSUri = %q, want unrecognized .exe extension dropped", res.GCSUri)
	}
	name := strings.TrimPrefix(res.GCSUri, "gs://bucket/"+objectPrefix)
	if len(name) != 64 {
		t.Fatalf("object name = %q (len %d), want bare 64-char sha256", name, len(name))
	}
	good := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(good, mp4Head, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = s.Stage(t.Context(), asset.StageInput{LocalPath: good})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.GCSUri, ".mp4") {
		t.Fatalf("GCSUri = %q, want preserved .mp4 extension", res.GCSUri)
	}
}

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

//go:build integration

package asset

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

// liveBucket is the provisioned Mizan staging bucket (bare name). It already
// exists; lifecycle management is a non-goal.
const liveBucket = "ghchinoy-genai-sa-mizan-staging"

// TestGCSStager_LiveUpload uploads a small temp file to the real staging bucket,
// asserts a gs:// URI comes back and the object exists, then cleans up.
//
// Gated on ADC being present (PROJECT_ID or GOOGLE_APPLICATION_CREDENTIALS /
// metadata). Run with:
//
//	PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./internal/asset/ -run Live -v
func TestGCSStager_LiveUpload(t *testing.T) {
	if os.Getenv("PROJECT_ID") == "" &&
		os.Getenv("GOOGLE_CLOUD_PROJECT") == "" &&
		os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		t.Skip("no ADC hint (PROJECT_ID / GOOGLE_CLOUD_PROJECT / GOOGLE_APPLICATION_CREDENTIALS); skipping live upload")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bucket := os.Getenv("MIZAN_STAGING_BUCKET")
	bucket = strings.TrimPrefix(bucket, "gs://")
	if bucket == "" {
		bucket = liveBucket
	}

	s, err := NewGCSStager(ctx, bucket)
	if err != nil {
		t.Fatalf("NewGCSStager: %v", err)
	}
	defer s.Close()

	// Small PNG-magic temp file.
	f, err := os.CreateTemp(t.TempDir(), "mizan-live-*.png")
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("\x89PNG\r\n\x1a\n"), []byte("mizan integration test payload")...)
	if _, err := f.Write(payload); err != nil {
		t.Fatal(err)
	}
	f.Close()

	res, err := s.Stage(ctx, StageInput{LocalPath: f.Name()})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	t.Logf("staged: GCSUri=%s MIME=%s", res.GCSUri, res.MIME)

	if !strings.HasPrefix(res.GCSUri, "gs://"+bucket+"/") {
		t.Fatalf("GCSUri = %q, want gs://%s/...", res.GCSUri, bucket)
	}
	if res.MIME != "image/png" {
		t.Fatalf("MIME = %q, want image/png", res.MIME)
	}

	// Assert the object exists, then clean up.
	object := strings.TrimPrefix(res.GCSUri, "gs://"+bucket+"/")
	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage.NewClient: %v", err)
	}
	defer client.Close()

	obj := client.Bucket(bucket).Object(object)
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		t.Fatalf("object attrs (existence check) for %s: %v", res.GCSUri, err)
	}
	t.Logf("object exists: size=%d contentType=%s", attrs.Size, attrs.ContentType)
	if attrs.ContentType != "image/png" {
		t.Errorf("uploaded contentType = %q, want image/png", attrs.ContentType)
	}

	if err := obj.Delete(ctx); err != nil {
		t.Fatalf("cleanup delete %s: %v", res.GCSUri, err)
	}
	t.Logf("cleaned up: deleted %s", res.GCSUri)
}

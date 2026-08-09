package asset

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestDetectMIME_CuratedExtensionMap pins the remaining entries of the curated
// extension->MIME map that the primary tables don't cover. The map is
// load-bearing: a wrong top-level type silently drops the asset on the native
// path (spike-core), so each pinned extension's resolved type is a spec.
// Notably .mpeg/.mpg resolve to VIDEO (not audio) — an easy-to-get-wrong case.
func TestDetectMIME_CuratedExtensionMap(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"a.m4a", "audio/mp4"},
		{"a.aac", "audio/aac"},
		{"a.mpga", "audio/mpeg"},
		{"a.oga", "audio/ogg"},
		{"a.m4v", "video/mp4"},
		{"a.mpeg", "video/mpeg"},
		{"a.mpg", "video/mpeg"},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			// nil head -> extension path only.
			if got := DetectMIME(tt.filename, nil); got != tt.want {
				t.Fatalf("DetectMIME(%q, nil) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

// TestStageLocalUnknownDefaultsOctetStream documents the fallback for an asset
// whose content and extension are both inconclusive and with no MIME override:
// Stage resolves application/octet-stream, which ModalityForMIME classifies as
// text — i.e. the native multimodal path treats it as non-media rather than
// guessing a media type.
func TestStageLocalUnknownDefaultsOctetStream(t *testing.T) {
	s, up := newTestStager("bucket")
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.zzz")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0x03}, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := s.Stage(context.Background(), StageInput{LocalPath: path})
	if err != nil {
		t.Fatalf("Stage(unknown) error: %v", err)
	}
	if res.MIME != "application/octet-stream" {
		t.Fatalf("MIME = %q, want application/octet-stream", res.MIME)
	}
	if got := ModalityForMIME(res.MIME); got != registry.ModalityText {
		t.Fatalf("ModalityForMIME(%q) = %q, want ModalityText", res.MIME, got)
	}
	// The bytes are still uploaded with the octet-stream content type.
	if len(up.objects) != 1 {
		t.Fatalf("uploaded %d objects, want 1", len(up.objects))
	}
	for obj, rec := range up.objects {
		if rec.contentType != "application/octet-stream" {
			t.Fatalf("object %q contentType = %q, want application/octet-stream", obj, rec.contentType)
		}
	}
}

// TestNormalizeMIME_M4VAlias pins the video/x-m4v -> video/mp4 canonicalization,
// the one normalizeMIME alias the primary table omits.
func TestNormalizeMIME_M4VAlias(t *testing.T) {
	if got := normalizeMIME("video/x-m4v"); got != "video/mp4" {
		t.Fatalf("normalizeMIME(video/x-m4v) = %q, want video/mp4", got)
	}
}

// TestStageConcurrent exercises the "safe for concurrent use" contract from the
// Stager doc comment: many goroutines staging distinct local files through one
// GCSStager must not race and must each get a distinct content-addressed URI.
// Run under -race to catch data races.
func TestStageConcurrent(t *testing.T) {
	s, up := newTestStager("bucket")
	dir := t.TempDir()

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	uris := make([]string, n)
	for i := 0; i < n; i++ {
		i := i
		// Distinct content per goroutine -> distinct object key.
		path := filepath.Join(dir, "f"+string(rune('a'+i))+".png")
		body := append([]byte("\x89PNG\r\n\x1a\n"), byte(i))
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.Stage(context.Background(), StageInput{LocalPath: path})
			errs[i] = err
			uris[i] = res.GCSUri
		}()
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := 0; i < n; i++ {
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

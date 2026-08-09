package asset

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// ftyp fixtures for the brand-disambiguation cases. mp4Head (brand "mp42") and
// m4aHead (brand "M4A ") live in mime_test.go.
var (
	m4bHead = []byte("\x00\x00\x00\x18ftypM4B \x00\x00\x00\x00") // audiobook (audio)
)

// TestDetectMIME_FtypAudioBrandDisambiguation covers the load-bearing fix: an
// audio-only MP4 must not be mislabeled video/mp4 (which would silently drop it
// on the native path). Unambiguous audio brands (M4A/M4B) map to audio/mp4, and
// a generic brand (mp42) is disambiguated by an MP4-family audio extension.
func TestDetectMIME_FtypAudioBrandDisambiguation(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		head     []byte
		want     string
	}{
		{"m4a-brand", "x.m4a", m4aHead, "audio/mp4"},
		{"m4b-brand", "x.m4b", m4bHead, "audio/mp4"},
		{"generic-brand-audio-ext", "x.m4a", mp4Head, "audio/mp4"}, // mp42 + .m4a -> audio
		{"generic-brand-aac-ext", "x.aac", mp4Head, "audio/mp4"},   // mp42 + .aac -> audio
		{"generic-brand-video-ext", "x.mp4", mp4Head, "video/mp4"}, // mp42 + .mp4 -> video
		{"generic-brand-no-ext", "clip", mp4Head, "video/mp4"},     // default video
		// Cross-type mismatch still lets content win: an mp42 container named
		// .jpg is video/mp4, not image/jpeg.
		{"generic-brand-image-ext", "photo.jpg", mp4Head, "video/mp4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectMIME(tt.filename, tt.head); got != tt.want {
				t.Fatalf("DetectMIME(%q, %s) = %q, want %q", tt.filename, tt.name, got, tt.want)
			}
		})
	}
}

// TestDetectMIME_OggVideo covers the .ogv fix: an OggS container defaults to
// audio/ogg, but a .ogv extension names Ogg video and must resolve to video/ogg
// (both from bytes and extension-only) so a Theora video is not silently dropped.
func TestDetectMIME_OggVideo(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		head     []byte
		want     string
	}{
		{"ogv-with-oggs-bytes", "movie.ogv", oggHead, "video/ogg"},
		{"ogv-extension-only", "movie.ogv", nil, "video/ogg"},
		{"ogg-defaults-audio", "song.ogg", oggHead, "audio/ogg"},
		// Cross-type mismatch: OggS bytes under a .png name still sniff audio/ogg
		// (content wins; only .ogv opts into video).
		{"oggs-bytes-png-ext", "pic.png", oggHead, "audio/ogg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectMIME(tt.filename, tt.head); got != tt.want {
				t.Fatalf("DetectMIME(%q, %s) = %q, want %q", tt.filename, tt.name, got, tt.want)
			}
		})
	}
}

// TestResolveMIME_OverrideConflictPrefersSniff covers the untrusted-override LOW:
// when an override's top-level class conflicts with an unambiguous content sniff,
// the sniffed type wins so an override cannot force a droppable top-level type.
// An inconclusive sniff still honors the override.
func TestStageLocalMIMEOverrideConflict(t *testing.T) {
	s, _ := newTestStager("bucket")
	dir := t.TempDir()

	// jpeg bytes but caller claims audio/mpeg -> sniff (image/jpeg) wins.
	conflict := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(conflict, jpegHead, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(context.Background(), StageInput{LocalPath: conflict, MIME: "audio/mpeg"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MIME != "image/jpeg" {
		t.Fatalf("MIME = %q, want image/jpeg (sniff wins over conflicting override)", res.MIME)
	}

	// Same top-level (image/jpeg bytes, image/png override): override honored.
	same := filepath.Join(dir, "blob2.bin")
	if err := os.WriteFile(same, jpegHead, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = s.Stage(context.Background(), StageInput{LocalPath: same, MIME: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if res.MIME != "image/png" {
		t.Fatalf("MIME = %q, want image/png (override honored, same top-level)", res.MIME)
	}
}

// TestStageLocalRejectsNonRegularFile covers the DoS guard: a non-regular
// LocalPath (here a FIFO — /dev/zero is the real-world case) is rejected before
// any read, so it cannot hang or OOM the process.
func TestStageLocalRejectsNonRegularFile(t *testing.T) {
	s, up := newTestStager("bucket")
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}

	_, err := s.Stage(context.Background(), StageInput{LocalPath: fifo})
	if err == nil {
		t.Fatal("Stage(FIFO) = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Stage(FIFO) err = %v, want 'not a regular file'", err)
	}
	if len(up.objects) != 0 {
		t.Fatalf("uploaded %d objects for rejected FIFO, want 0", len(up.objects))
	}
}

// TestStageLocalRejectsOversize covers the size cap: a regular file larger than
// maxAssetBytes is rejected. A sparse file (Truncate) keeps the test cheap.
func TestStageLocalRejectsOversize(t *testing.T) {
	s, up := newTestStager("bucket")
	dir := t.TempDir()
	big := filepath.Join(dir, "big.mp4")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxAssetBytes + 1); err != nil {
		f.Close()
		t.Skipf("cannot create sparse oversize file: %v", err)
	}
	f.Close()

	_, err = s.Stage(context.Background(), StageInput{LocalPath: big})
	if err == nil {
		t.Fatal("Stage(oversize) = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Stage(oversize) err = %v, want size-cap error", err)
	}
	if len(up.objects) != 0 {
		t.Fatalf("uploaded %d objects for oversize file, want 0", len(up.objects))
	}
}

// TestStageLocalResolvesSymlink covers the EvalSymlinks path: staging a symlink
// to a regular file succeeds and detects the target's content type.
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

	res, err := s.Stage(context.Background(), StageInput{LocalPath: link})
	if err != nil {
		t.Fatalf("Stage(symlink) error: %v", err)
	}
	if res.MIME != "image/png" {
		t.Fatalf("MIME = %q, want image/png", res.MIME)
	}
	if len(up.objects) != 1 {
		t.Fatalf("uploaded %d objects, want 1", len(up.objects))
	}
}

// TestObjectKeyExtensionSanitized covers the object-key LOW: an unrecognized
// source extension is dropped from the object name (the key core stays the
// content-addressed sha256), keeping arbitrary bytes out of the key.
func TestObjectKeyExtensionSanitized(t *testing.T) {
	s, _ := newTestStager("bucket")
	dir := t.TempDir()
	// .exe is not a recognized media extension -> dropped.
	weird := filepath.Join(dir, "payload.exe")
	if err := os.WriteFile(weird, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := s.Stage(context.Background(), StageInput{LocalPath: weird})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.GCSUri, ".exe") {
		t.Fatalf("GCSUri = %q, want unrecognized .exe extension dropped", res.GCSUri)
	}
	// Object name after the prefix must be the bare 64-char hex digest.
	name := strings.TrimPrefix(res.GCSUri, "gs://bucket/"+objectPrefix)
	if len(name) != 64 {
		t.Fatalf("object name = %q (len %d), want bare 64-char sha256", name, len(name))
	}

	// Recognized extension is preserved.
	good := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(good, mp4Head, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = s.Stage(context.Background(), StageInput{LocalPath: good})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(res.GCSUri, ".mp4") {
		t.Fatalf("GCSUri = %q, want preserved .mp4 extension", res.GCSUri)
	}
}

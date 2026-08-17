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

package eval

// defect_a_test.go covers the Defect A fix: the genai structured-output path
// (custom_schema + rubric --rubric-detail) must not silently drop a CLI-supplied
// --gcs/--file media asset. A CLI ref carries an EMPTY Modality by design
// (buildInstance leaves MIME/modality resolution to the engine); before the fix
// renderGenaiPrompt keyed on Modality and collapsed such refs to empty text.
//
// The acceptance criteria (design/defect-a-scope.md §"Acceptance Criteria",
// unit items 1-5) are:
//  1. a --gcs media ref with empty Modality produces a URI/FileData Part with the
//     extension-resolved MIME (audio/wav, image/jpeg, video/mp4), not empty text;
//  2. a --file media ref with empty Modality produces an inline-bytes Part with
//     the sniffed MIME;
//  3. a media ref whose MIME cannot be resolved returns a HARD error and
//     GenerateContent is never called;
//  4. a genuine text field still substitutes inline (regression);
//  5. a ref with Modality explicitly pre-set to a media type still attaches
//     (regression / future import path).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// jpegMagic is a minimal JPEG header (SOI + APP0/JFIF marker) so asset.DetectMIME
// sniffs image/jpeg from the bytes, exercising the content-sniff path (not just
// the extension).
var jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}

// Criterion 1: a --gcs media ref with empty Modality attaches as a URI Part with
// the MIME resolved from the object extension — NOT an empty text Part. The two
// live-repro assets (coffee_order.wav, cat_on_snow.jpg) plus a video case are all
// covered by extension.
func TestRenderGenaiPromptGCSMediaAttaches(t *testing.T) {
	cases := []struct {
		name     string
		uri      string
		wantMIME string
	}{
		{"audio_wav_cell10", "gs://cloud-samples-data/generative-ai/audio/coffee_order.wav", "audio/wav"},
		{"image_jpeg_cell7a", "gs://cloud-samples-data/generative-ai/image/320px-Felis_catus-cat_on_snow.jpg", "image/jpeg"},
		{"video_mp4", "gs://bkt/clip.mp4", "video/mp4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rendered, parts, err := renderGenaiPrompt(
				"Analyze {{media}} carefully.",
				Instance{Fields: map[string]AssetRef{
					// Empty Modality — exactly what the CLI --gcs flag produces.
					"media": {GCSUri: c.uri},
				}},
			)
			if err != nil {
				t.Fatalf("renderGenaiPrompt: unexpected error: %v", err)
			}
			if len(parts) != 1 {
				t.Fatalf("mediaParts = %d, want 1", len(parts))
			}
			fd := parts[0].FileData
			if fd == nil {
				t.Fatalf("part is not a FileData/URI part: %+v", parts[0])
			}
			if fd.FileURI != c.uri {
				t.Errorf("FileURI = %q, want %q", fd.FileURI, c.uri)
			}
			if fd.MIMEType != c.wantMIME {
				t.Errorf("MIMEType = %q, want %q", fd.MIMEType, c.wantMIME)
			}
			// The placeholder must be substituted (not left raw), and the media
			// must NOT have collapsed into an empty inline text part.
			if strings.Contains(rendered, "{{media}}") {
				t.Errorf("placeholder not substituted: %q", rendered)
			}
			if parts[0].Text != "" {
				t.Errorf("media part carries text %q; must be a URI part", parts[0].Text)
			}
		})
	}
}

// Criterion 2: a --file media ref with empty Modality produces an inline-bytes
// Part with the sniffed MIME.
func TestRenderGenaiPromptFileMediaInlineBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cat.jpg")
	if err := os.WriteFile(path, jpegMagic, 0o644); err != nil {
		t.Fatal(err)
	}

	_, parts, err := renderGenaiPrompt(
		"Describe {{img}}.",
		Instance{Fields: map[string]AssetRef{
			// Empty Modality — exactly what the CLI --file flag produces.
			"img": {FilePath: path},
		}},
	)
	if err != nil {
		t.Fatalf("renderGenaiPrompt: unexpected error: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("mediaParts = %d, want 1", len(parts))
	}
	blob := parts[0].InlineData
	if blob == nil {
		t.Fatalf("part is not an inline-bytes part: %+v", parts[0])
	}
	if blob.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q, want image/jpeg (sniffed)", blob.MIMEType)
	}
	if !bytes.Equal(blob.Data, jpegMagic) {
		t.Errorf("inline data = %v, want the file bytes", blob.Data)
	}
}

// Criterion 3 (render level): a media ref whose MIME cannot be resolved (unknown
// extension, no bytes) returns a HARD error from renderGenaiPrompt — never a
// silent drop.
func TestRenderGenaiPromptUnresolvableMIMEErrors(t *testing.T) {
	_, _, err := renderGenaiPrompt(
		"Judge {{x}}.",
		Instance{Fields: map[string]AssetRef{
			"x": {GCSUri: "gs://bkt/object-without-extension"},
		}},
	)
	if err == nil {
		t.Fatal("expected a hard error for an unresolvable media MIME, got nil")
	}
	if !strings.Contains(err.Error(), "MIME") {
		t.Errorf("error = %v, want it to name the MIME resolution failure", err)
	}
}

// Criterion 3 (engine level): the unresolvable-MIME error propagates out of
// runGenaiStructured BEFORE any API call — the fake GenerateContent must never be
// invoked (no confabulated result can be produced).
func TestRunCustomSchemaUnresolvableMIMENoAPICall(t *testing.T) {
	fg := &fakeGenai{respText: "{}"}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	tmpl := customSchemaTemplate()
	tmpl.MetricPromptTemplate = "Judge {{creative}}"
	_, err := eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{
			"creative": {GCSUri: "gs://bkt/object-without-extension"},
		},
	})
	if err == nil {
		t.Fatal("expected an error when the media MIME cannot be resolved")
	}
	if !strings.Contains(err.Error(), "MIME") {
		t.Errorf("error = %v, want it to name the MIME failure", err)
	}
	if fg.calls != 0 {
		t.Errorf("GenerateContent was called %d times; must be 0 when render errors", fg.calls)
	}
}

// Criterion 4: a genuine text field (no path/URI, empty or text Modality) still
// substitutes inline and produces no media Parts — regression guard that the
// predicate flip did not disturb the text path.
func TestRenderGenaiPromptTextStillInline(t *testing.T) {
	// Empty Modality text (the common CLI text case).
	rendered, parts, err := renderGenaiPrompt(
		"Hello {{name}}!",
		Instance{Fields: map[string]AssetRef{"name": {Text: "world"}}},
	)
	if err != nil {
		t.Fatalf("renderGenaiPrompt: %v", err)
	}
	if len(parts) != 0 {
		t.Errorf("mediaParts = %d, want 0 for a text field", len(parts))
	}
	if rendered != "Hello world!" {
		t.Errorf("rendered = %q, want %q", rendered, "Hello world!")
	}

	// Explicit text Modality behaves identically.
	rendered2, parts2, err := renderGenaiPrompt(
		"Hi {{n}}.",
		Instance{Fields: map[string]AssetRef{"n": {Modality: registry.ModalityText, Text: "bob"}}},
	)
	if err != nil {
		t.Fatalf("renderGenaiPrompt (explicit text): %v", err)
	}
	if len(parts2) != 0 {
		t.Errorf("mediaParts = %d, want 0", len(parts2))
	}
	if rendered2 != "Hi bob." {
		t.Errorf("rendered = %q, want %q", rendered2, "Hi bob.")
	}
}

// Criterion 5: a ref with Modality explicitly pre-set to a media type (a future
// import path that DOES set Modality) still attaches — the flip must not regress
// the already-typed case.
func TestRenderGenaiPromptPresetModalityAttaches(t *testing.T) {
	rendered, parts, err := renderGenaiPrompt(
		"Rate {{img}}.",
		Instance{Fields: map[string]AssetRef{
			"img": {Modality: registry.ModalityImage, GCSUri: "gs://bkt/x.jpg", MimeType: "image/jpeg"},
		}},
	)
	if err != nil {
		t.Fatalf("renderGenaiPrompt: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("mediaParts = %d, want 1", len(parts))
	}
	if parts[0].FileData == nil || parts[0].FileData.MIMEType != "image/jpeg" {
		t.Errorf("part = %+v, want a FileData image/jpeg part", parts[0])
	}
	// A pre-set modality is reflected in the human-facing attachment marker.
	if !strings.Contains(rendered, "attached image") {
		t.Errorf("rendered = %q, want an 'attached image' marker", rendered)
	}
}

// TestReadInlineAssetMIMEParity is a focused unit test for Fix C: readInlineAsset
// uses asset.DetectMIME (sniff + extension) instead of http.DetectContentType,
// and rejects an unresolvable/octet-stream type instead of forwarding a droppable
// MIME. A WAV file is the canonical case http.DetectContentType mis-types.
func TestReadInlineAssetMIMEParity(t *testing.T) {
	dir := t.TempDir()

	// A RIFF/WAVE header — http.DetectContentType would return
	// application/octet-stream (a silent-drop hazard); asset.DetectMIME sniffs
	// audio/wav.
	wav := filepath.Join(dir, "clip.wav")
	wavData := append([]byte("RIFF\x24\x00\x00\x00WAVE"), make([]byte, 8)...)
	if err := os.WriteFile(wav, wavData, 0o644); err != nil {
		t.Fatal(err)
	}
	data, mime, err := readInlineAsset(wav, "")
	if err != nil {
		t.Fatalf("readInlineAsset(wav): %v", err)
	}
	if mime != "audio/wav" {
		t.Errorf("mime = %q, want audio/wav (asset.DetectMIME, not http.DetectContentType)", mime)
	}
	if !bytes.Equal(data, wavData) {
		t.Errorf("data mismatch")
	}

	// An explicit override still wins.
	_, mime, err = readInlineAsset(wav, "audio/x-custom")
	if err != nil {
		t.Fatalf("readInlineAsset(override): %v", err)
	}
	if mime != "audio/x-custom" {
		t.Errorf("mime = %q, want the override audio/x-custom", mime)
	}

	// Unresolvable type (no extension, non-media bytes) is a hard error, not a
	// forwarded octet-stream.
	blob := filepath.Join(dir, "blob")
	if err := os.WriteFile(blob, []byte{0x00, 0x01, 0x02, 0x03, 0x04}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readInlineAsset(blob, ""); err == nil {
		t.Error("expected a hard error for an unresolvable MIME, got nil")
	} else if !strings.Contains(err.Error(), "MIME") {
		t.Errorf("error = %v, want it to name the MIME failure", err)
	}
}

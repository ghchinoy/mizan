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
	"os"
	"path/filepath"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// magic byte fixtures for each media container we sniff.
var (
	jpegHead = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	pngHead  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0, 0, 0, 0}
	gifHead  = []byte("GIF89a\x01\x00\x01\x00\x00\x00")
	webpHead = []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
	wavHead  = []byte("RIFF\x24\x00\x00\x00WAVEfmt ")
	oggHead  = []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00")
	flacHead = []byte("fLaC\x00\x00\x00\x22\x00\x00\x00\x00")
	mp3ID3   = []byte("ID3\x03\x00\x00\x00\x00\x00\x00")
	mp3Frame = []byte{0xFF, 0xFB, 0x90, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}
	mp4Head  = []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00")
	movHead  = []byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00")
	m4aHead  = []byte("\x00\x00\x00\x18ftypM4A \x00\x00\x00\x00")
	webmHead = []byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x00, 0x00, 0x00, 0, 0, 0, 0}
)

func TestDetectMIME_ContentSniff(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		head     []byte
		want     string
	}{
		{"jpeg", "x.jpg", jpegHead, "image/jpeg"},
		{"png", "x.png", pngHead, "image/png"},
		{"gif", "x.gif", gifHead, "image/gif"},
		{"webp", "x.webp", webpHead, "image/webp"},
		{"wav", "x.wav", wavHead, "audio/wav"},
		{"ogg", "x.ogg", oggHead, "audio/ogg"},
		{"flac", "x.flac", flacHead, "audio/flac"},
		{"mp3-id3", "x.mp3", mp3ID3, "audio/mpeg"},
		{"mp3-frame", "x.mp3", mp3Frame, "audio/mpeg"},
		{"mp4", "x.mp4", mp4Head, "video/mp4"},
		{"mov", "x.mov", movHead, "video/quicktime"},
		{"m4a", "x.m4a", m4aHead, "audio/mp4"},
		{"webm", "x.webm", webmHead, "video/webm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectMIME(tt.filename, tt.head); got != tt.want {
				t.Fatalf("DetectMIME(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestDetectMIME_ExtensionFallback(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"a.jpg", "image/jpeg"},
		{"a.jpeg", "image/jpeg"},
		{"a.png", "image/png"},
		{"a.gif", "image/gif"},
		{"a.webp", "image/webp"},
		{"a.mp3", "audio/mpeg"},
		{"a.wav", "audio/wav"},
		{"a.ogg", "audio/ogg"},
		{"a.flac", "audio/flac"},
		{"a.mp4", "video/mp4"},
		{"a.webm", "video/webm"},
		{"a.mov", "video/quicktime"},
		{"a.JPG", "image/jpeg"}, // case-insensitive extension
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			// No bytes -> extension path only.
			if got := DetectMIME(tt.filename, nil); got != tt.want {
				t.Fatalf("DetectMIME(%q, nil) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

// TestDetectMIME_MismatchDanger covers the load-bearing case from spike-core:
// when the extension lies about the content, the sniffed content type wins so
// the native path does not silently drop the asset on a top-level mismatch.
func TestDetectMIME_MismatchDanger(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		head     []byte
		want     string
	}{
		{"jpeg-bytes-txt-ext", "note.txt", jpegHead, "image/jpeg"},
		{"png-bytes-mp3-ext", "song.mp3", pngHead, "image/png"},
		{"wav-bytes-png-ext", "pic.png", wavHead, "audio/wav"},
		{"mp4-bytes-jpg-ext", "photo.jpg", mp4Head, "video/mp4"},
		{"webm-bytes-wav-ext", "clip.wav", webmHead, "video/webm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectMIME(tt.filename, tt.head)
			if got != tt.want {
				t.Fatalf("DetectMIME(%q, %s bytes) = %q, want %q", tt.filename, tt.name, got, tt.want)
			}
		})
	}
}

func TestDetectMIME_Unknown(t *testing.T) {
	if got := DetectMIME("mystery.xyz", []byte{0x00, 0x01, 0x02, 0x03}); got != "application/octet-stream" {
		t.Fatalf("DetectMIME(unknown) = %q, want application/octet-stream", got)
	}
	if got := DetectMIME("", nil); got != "application/octet-stream" {
		t.Fatalf("DetectMIME(empty) = %q, want application/octet-stream", got)
	}
}

func TestDetectMIMEFile(t *testing.T) {
	dir := t.TempDir()

	// Correctly named file.
	good := filepath.Join(dir, "img.png")
	if err := os.WriteFile(good, pngHead, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := DetectMIMEFile(good); err != nil || got != "image/png" {
		t.Fatalf("DetectMIMEFile(png) = %q, %v; want image/png, nil", got, err)
	}

	// Misleadingly named file: content wins.
	lie := filepath.Join(dir, "actually_jpeg.png")
	if err := os.WriteFile(lie, jpegHead, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := DetectMIMEFile(lie); err != nil || got != "image/jpeg" {
		t.Fatalf("DetectMIMEFile(lie) = %q, %v; want image/jpeg, nil", got, err)
	}

	// Empty file: extension fallback.
	empty := filepath.Join(dir, "empty.wav")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := DetectMIMEFile(empty); err != nil || got != "audio/wav" {
		t.Fatalf("DetectMIMEFile(empty) = %q, %v; want audio/wav, nil", got, err)
	}

	// Missing file: error.
	if _, err := DetectMIMEFile(filepath.Join(dir, "nope.png")); err == nil {
		t.Fatal("DetectMIMEFile(missing) = nil error, want error")
	}
}

func TestModalityForMIME(t *testing.T) {
	tests := []struct {
		mime string
		want registry.Modality
	}{
		{"image/jpeg", registry.ModalityImage},
		{"image/png", registry.ModalityImage},
		{"audio/mpeg", registry.ModalityAudio},
		{"audio/wav", registry.ModalityAudio},
		{"video/mp4", registry.ModalityVideo},
		{"video/webm", registry.ModalityVideo},
		{"text/plain", registry.ModalityText},
		{"application/octet-stream", registry.ModalityText},
		{"", registry.ModalityText},
	}
	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			if got := ModalityForMIME(tt.mime); got != tt.want {
				t.Fatalf("ModalityForMIME(%q) = %q, want %q", tt.mime, got, tt.want)
			}
		})
	}
}

func TestNormalizeMIME(t *testing.T) {
	tests := map[string]string{
		"audio/wave":                "audio/wav",
		"audio/x-wav":               "audio/wav",
		"audio/x-flac":              "audio/flac",
		"audio/mp3":                 "audio/mpeg",
		"text/plain; charset=utf-8": "text/plain",
		"image/png":                 "image/png",
	}
	for in, want := range tests {
		if got := normalizeMIME(in); got != want {
			t.Fatalf("normalizeMIME(%q) = %q, want %q", in, got, want)
		}
	}
}

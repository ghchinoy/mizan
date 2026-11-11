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

import "testing"

var (
	m4bHead = []byte("\x00\x00\x00\x18ftypM4B \x00\x00\x00\x00")
)

func TestDetectMIME_FtypAudioBrandDisambiguation(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		head     []byte
		want     string
	}{
		{"m4a-brand", "x.m4a", m4aHead, "audio/mp4"},
		{"m4b-brand", "x.m4b", m4bHead, "audio/mp4"},
		{"generic-brand-audio-ext", "x.m4a", mp4Head, "audio/mp4"},
		{"generic-brand-aac-ext", "x.aac", mp4Head, "audio/mp4"},
		{"generic-brand-video-ext", "x.mp4", mp4Head, "video/mp4"},
		{"generic-brand-no-ext", "clip", mp4Head, "video/mp4"},
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

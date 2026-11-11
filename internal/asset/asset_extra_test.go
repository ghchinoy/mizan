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
			if got := DetectMIME(tt.filename, nil); got != tt.want {
				t.Fatalf("DetectMIME(%q, nil) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestNormalizeMIME_M4VAlias(t *testing.T) {
	if got := normalizeMIME("video/x-m4v"); got != "video/mp4" {
		t.Fatalf("normalizeMIME(video/x-m4v) = %q, want video/mp4", got)
	}
}

func TestResolveMIME_OverrideConflictPrefersSniff(t *testing.T) {
	if got := ResolveMIME("audio/mpeg", "blob.bin", jpegHead); got != "image/jpeg" {
		t.Fatalf("ResolveMIME(conflict) = %q, want image/jpeg", got)
	}
	if got := ResolveMIME("image/png", "blob2.bin", jpegHead); got != "image/png" {
		t.Fatalf("ResolveMIME(same top-level) = %q, want image/png", got)
	}
}

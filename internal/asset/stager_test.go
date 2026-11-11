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

func TestStageInputValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      StageInput
		wantErr bool
	}{
		{"neither", StageInput{}, true},
		{"both", StageInput{LocalPath: "/tmp/x.png", GCSUri: "gs://b/o.png"}, true},
		{"bad-gcs-scheme", StageInput{GCSUri: "http://example.com/o.png"}, true},
		{"valid-local", StageInput{LocalPath: "/tmp/x.png"}, false},
		{"valid-gcs", StageInput{GCSUri: "gs://b/o.png"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%+v) err = %v, wantErr %t", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestGCSObjectName(t *testing.T) {
	tests := map[string]string{
		"gs://bucket/path/to/file.mp4": "path/to/file.mp4",
		"gs://bucket/file.png":         "file.png",
		"gs://bucket":                  "bucket",
	}
	for in, want := range tests {
		if got := GCSObjectName(in); got != want {
			t.Fatalf("GCSObjectName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeExt(t *testing.T) {
	if got := SafeExt("/tmp/clip.MP4"); got != ".mp4" {
		t.Fatalf("SafeExt(.MP4) = %q, want .mp4", got)
	}
	if got := SafeExt("/tmp/unknown.xyz"); got != "" {
		t.Fatalf("SafeExt(.xyz) = %q, want empty", got)
	}
}

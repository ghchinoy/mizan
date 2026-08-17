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

package registry

import "testing"

func TestValidateSemver(t *testing.T) {
	valid := []string{"0.1.0", "1.2.3", "v2.0.0", "1.0.0-rc.1", "1.2.3+build.5"}
	for _, s := range valid {
		if err := ValidateSemver(s); err != nil {
			t.Errorf("ValidateSemver(%q) = %v, want nil", s, err)
		}
	}
	invalid := []string{"", "not-a-version", "1.2.3.4", "abc.def.ghi"}
	for _, s := range invalid {
		if err := ValidateSemver(s); err == nil {
			t.Errorf("ValidateSemver(%q) = nil, want error", s)
		}
	}
}

func TestParseModality(t *testing.T) {
	for _, s := range []string{"text", "image", "audio", "video", "music"} {
		m, err := ParseModality(s)
		if err != nil {
			t.Errorf("ParseModality(%q) = %v, want nil", s, err)
		}
		if string(m) != s {
			t.Errorf("ParseModality(%q) = %q, want %q", s, m, s)
		}
	}
	for _, s := range []string{"", "txt", "Text", "picture"} {
		if _, err := ParseModality(s); err == nil {
			t.Errorf("ParseModality(%q) = nil error, want error", s)
		}
	}
}

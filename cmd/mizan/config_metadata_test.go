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

package main

import (
	"strings"
	"testing"
)

// TestConfigShowSurfacesAuthorAndLicense proves `config show` surfaces the two
// authoring metadata keys and reflects their exported env values verbatim,
// driven by the same config.Fields single source of truth as every other row.
func TestConfigShowSurfacesAuthorAndLicense(t *testing.T) {
	cleanConfigEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_AUTHOR_NAME", "Jane Doe")
	t.Setenv("MIZAN_DEFAULT_LICENSE", "Apache-2.0")

	out, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v (out=%q)", err, out)
	}
	for _, want := range []string{"author-name", "Jane Doe", "default-license", "Apache-2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show missing %q; got:\n%s", want, out)
		}
	}
}

// TestConfigSetAcceptsAuthorAndLicense proves the two new keys round-trip through
// `config set` (they are accepted keys, derived from Fields).
func TestConfigSetAcceptsAuthorAndLicense(t *testing.T) {
	cleanConfigEnv(t)
	for _, key := range []string{"author-name", "default-license"} {
		if _, err := executeRoot(t, "config", "set", key, "x"); err != nil {
			t.Errorf("config set %q rejected: %v", key, err)
		}
	}
}

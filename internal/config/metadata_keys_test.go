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

package config

import "testing"

// TestAuthorLicenseFieldsResolveFromEnv proves the two authoring metadata fields
// (author-name, default-license) resolve from their env vars through the SAME
// single source of truth (Fields) that backs `config show`/`config set`.
func TestAuthorLicenseFieldsResolveFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123") // avoid ErrMissingProjectID
	t.Setenv("MIZAN_AUTHOR_NAME", "Jane Doe")
	t.Setenv("MIZAN_DEFAULT_LICENSE", "Apache-2.0")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.AuthorName != "Jane Doe" {
		t.Errorf("AuthorName = %q, want Jane Doe", c.AuthorName)
	}
	if c.DefaultLicense != "Apache-2.0" {
		t.Errorf("DefaultLicense = %q, want Apache-2.0", c.DefaultLicense)
	}
	// Both must be attributed to the exported env var (POLA #1).
	if got := c.SourceOf("author-name"); got != SourceEnv {
		t.Errorf("SourceOf(author-name) = %v, want env", got)
	}
	if got := c.SourceOf("default-license"); got != SourceEnv {
		t.Errorf("SourceOf(default-license) = %v, want env", got)
	}
}

// TestAuthorLicenseFieldsPresent is a drift guard: both new keys must appear in
// Fields() with their canonical env var, so `config show`/`config set` expose
// them automatically.
func TestAuthorLicenseFieldsPresent(t *testing.T) {
	want := map[string]string{
		"author-name":     "MIZAN_AUTHOR_NAME",
		"default-license": "MIZAN_DEFAULT_LICENSE",
	}
	got := map[string]string{}
	for _, f := range Fields() {
		if _, ok := want[f.Key]; ok {
			got[f.Key] = f.EnvVars[0]
		}
	}
	for k, env := range want {
		if got[k] != env {
			t.Errorf("Fields() missing %q -> %q (got %q)", k, env, got[k])
		}
	}
}

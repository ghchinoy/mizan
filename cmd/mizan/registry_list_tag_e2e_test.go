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

// registry_list_tag_e2e_test.go drives the B1 `registry list --tag` filter
// through the REAL cobra commands against a throwaway SQLite registry (no project
// or network needed). It covers: AND-narrowing across repeated --tag,
// case-sensitive exact match, and that omitting --tag leaves the existing table
// and `-o json` output shape unchanged. Hermetic and cgo-free.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// listTagIDs runs `registry list <args> --output json` and returns the matched
// template IDs, newest-first by the store's ID ordering.
func listTagIDs(t *testing.T, args ...string) []string {
	t.Helper()
	full := append([]string{"--output", "json", "registry", "list"}, args...)
	out, err := executeRoot(t, full...)
	if err != nil {
		t.Fatalf("registry list %v: %v (out=%q)", args, err, out)
	}
	var got []registry.MetricTemplate
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode list output %q: %v", out, err)
	}
	ids := make([]string, 0, len(got))
	for _, tmpl := range got {
		ids = append(ids, tmpl.ID)
	}
	return ids
}

// seedTagFixtures creates three templates with distinct tag sets.
func seedTagFixtures(t *testing.T) {
	t.Helper()
	useIsolatedRegistry(t)
	seeds := []struct {
		id   string
		tags []string
	}{
		{"test/a", []string{"quality", "safety"}},
		{"test/b", []string{"quality"}},
		{"test/c", nil},
	}
	for _, s := range seeds {
		args := []string{"registry", "create", "--id", s.id, "--kind", "pointwise", "--prompt", "Judge {{response}}"}
		for _, tag := range s.tags {
			args = append(args, "--tag", tag)
		}
		if out, err := executeRoot(t, args...); err != nil {
			t.Fatalf("seed %s: %v (out=%q)", s.id, err, out)
		}
	}
}

// TestRegistryListTagFilters proves the --tag filter's AND-narrowing and
// case-sensitive semantics through the real CLI.
func TestRegistryListTagFilters(t *testing.T) {
	seedTagFixtures(t)

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"single tag", []string{"--tag", "quality"}, []string{"test/a", "test/b"}},
		{"and narrowing", []string{"--tag", "quality", "--tag", "safety"}, []string{"test/a"}},
		{"absent tag", []string{"--tag", "nope"}, []string{}},
		{"case sensitive", []string{"--tag", "Quality"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := listTagIDs(t, tc.args...)
			if !equalStrings(got, tc.want) {
				t.Errorf("registry list %v = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

// TestRegistryListNoTagUnchanged proves omitting --tag returns every template
// (the pre-B1 default behavior) and that the JSON output shape is unaffected by
// the new flag.
func TestRegistryListNoTagUnchanged(t *testing.T) {
	seedTagFixtures(t)

	// JSON: all three templates, ordered by ID.
	gotJSON := listTagIDs(t)
	wantJSON := []string{"test/a", "test/b", "test/c"}
	if !equalStrings(gotJSON, wantJSON) {
		t.Errorf("registry list (no --tag) json = %v, want %v", gotJSON, wantJSON)
	}

	// Table output must still render and contain every seeded ID (default format,
	// unchanged by adding the flag).
	out, err := executeRoot(t, "registry", "list")
	if err != nil {
		t.Fatalf("registry list (table): %v (out=%q)", err, out)
	}
	for _, id := range wantJSON {
		if !strings.Contains(out, id) {
			t.Errorf("registry list table output missing %q\n%s", id, out)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

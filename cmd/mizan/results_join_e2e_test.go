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
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/results"
)

// TestResultsListTagJoinE2E drives the full registry->results tag join (design
// §4.A step 4, §9 B1 join) end to end over temp SQLite registry + results DBs:
//
//   - Two templates carry tag "brand"; one carries "other".
//   - Each template has a seeded result.
//   - `results list --tag brand` returns results for BOTH "brand" templates and
//     NONE of the "other" template's — proving the join resolves the tag against
//     the registry's current tags and merges results across all matches, rather
//     than filtering the (tag-less) results store directly.
func TestResultsListTagJoinE2E(t *testing.T) {
	cleanConfigEnv(t)
	dir := t.TempDir()
	registryDB := filepath.Join(dir, "registry.db")
	resultsDB := filepath.Join(dir, "results.db")
	t.Setenv("MIZAN_REGISTRY_DB", registryDB)
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", resultsDB)

	// Seed the registry with three templates: two tagged "brand", one "other".
	create := func(id, tag string) {
		t.Helper()
		out, err := executeRoot(t, "registry", "create",
			"--id", id, "--kind", "single", "--prompt", "Judge {{response}}", "--tag", tag)
		if err != nil {
			t.Fatalf("registry create %s: %v (out=%q)", id, err, out)
		}
	}
	create("brand-a/quality", "brand")
	create("brand-b/tone", "brand")
	create("other-c/style", "other")

	// Seed one result per template into the results store (real write hook, no API).
	seedResult(t, resultsDB, "brand-a/quality", "1.0.0")
	seedResult(t, resultsDB, "brand-b/tone", "1.0.0")
	seedResult(t, resultsDB, "other-c/style", "1.0.0")

	out, err := executeRoot(t, "--output", "json", "results", "list", "--tag", "brand")
	if err != nil {
		t.Fatalf("results list --tag brand: %v (out=%q)", err, out)
	}
	var got []results.Result
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode results list output %q: %v", out, err)
	}

	ids := map[string]bool{}
	for _, r := range got {
		ids[r.Template.ID] = true
	}
	if !ids["brand-a/quality"] || !ids["brand-b/tone"] {
		t.Errorf("join dropped a tagged template's results; got ids %v", ids)
	}
	if ids["other-c/style"] {
		t.Errorf("join leaked a non-'brand' template's results; got ids %v", ids)
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 joined results, got %d: %s", len(got), out)
	}

	// AND-narrowing / empty resolution: a tag no template carries yields no rows
	// and the friendly "no results found" note (case-sensitive: "Brand" != "brand").
	empty, err := executeRoot(t, "results", "list", "--tag", "Brand")
	if err != nil {
		t.Fatalf("results list --tag Brand: %v (out=%q)", err, empty)
	}
	if !strings.Contains(empty, "no results found") {
		t.Errorf("empty (case-sensitive miss) resolution should print the no-results note, got: %q", empty)
	}
}

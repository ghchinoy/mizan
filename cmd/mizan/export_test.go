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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/staxexport"
)

// exportTempDB points the registry at a fresh SQLite file for the test. The
// export path is credential-free: it opens only the local registry (no
// Vertex/genai client, no ADC), so these tests need no PROJECT_ID and touch no
// network — asserting the credential-free invariant by construction.
func exportTempDB(t *testing.T) {
	t.Helper()
	t.Setenv("MIZAN_REGISTRY_DB", filepath.Join(t.TempDir(), "registry.db"))
}

// createRubricBrand seeds a rubric template mirroring the golden fixture's
// group/criterion shape (2 groups, 3 criterion-pairs). RatingRubric bands are not
// settable via `registry create`, so category-description derivation is covered
// by the staxexport unit tests; here we assert CLI wiring, cardinality, and the
// name convention.
func createRubricBrand(t *testing.T) {
	t.Helper()
	out, err := executeRoot(t,
		"registry", "create",
		"--id", "acme/rubric-brand",
		"--name", "Rubric Brand",
		"--kind", "rubric",
		"--rubric-group", "clarity=clear;concise",
		"--rubric-group", "tone=on-brand",
	)
	if err != nil {
		t.Fatalf("registry create rubric: %v (out=%q)", err, out)
	}
}

func TestExportStaxRubricFanoutOptionB(t *testing.T) {
	exportTempDB(t)
	createRubricBrand(t)

	out, err := executeRoot(t, "export", "stax", "--metric", "acme/rubric-brand")
	if err != nil {
		t.Fatalf("export stax: %v (out=%q)", err, out)
	}

	var evs []staxexport.Evaluator
	if err := json.Unmarshal([]byte(out), &evs); err != nil {
		t.Fatalf("output is not a JSON array of evaluators: %v\n%s", err, out)
	}
	if len(evs) != 3 {
		t.Fatalf("got %d evaluators, want 3 (fan-out)", len(evs))
	}
	names := []string{evs[0].Name, evs[1].Name, evs[2].Name}
	want := []string{
		"acme/rubric-brand::clarity::clear",
		"acme/rubric-brand::clarity::concise",
		"acme/rubric-brand::tone::on-brand",
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("evaluator[%d] name = %q, want %q", i, names[i], want[i])
		}
	}
	// Each fan-out evaluator carries a full 1-5 category set.
	if n := len(evs[0].OutputCategories); n != 5 {
		t.Errorf("clarity::clear has %d categories, want 5", n)
	}
}

func TestExportStaxFlattenWarns(t *testing.T) {
	exportTempDB(t)
	createRubricBrand(t)

	out, err := executeRoot(t, "export", "stax", "--metric", "acme/rubric-brand", "--flatten")
	if err != nil {
		t.Fatalf("export stax --flatten: %v (out=%q)", err, out)
	}
	// Combined stdout+stderr: the warning (stderr) and one JSON object (stdout).
	if !strings.Contains(out, "dropped per-criterion granularity") {
		t.Errorf("flatten did not emit the granularity-loss warning; out=%q", out)
	}
	// The JSON portion must be a single object (one flattened evaluator), not an
	// array. Find the object that starts after the warning line.
	brace := strings.Index(out, "{")
	if brace < 0 {
		t.Fatalf("no JSON object in output: %q", out)
	}
	var ev staxexport.Evaluator
	if err := json.Unmarshal([]byte(out[brace:]), &ev); err != nil {
		t.Fatalf("flatten output is not a single JSON object: %v\n%s", err, out)
	}
	if !strings.HasSuffix(ev.Name, "(flattened)") {
		t.Errorf("flatten evaluator name = %q, want it to end with (flattened)", ev.Name)
	}
}

func TestExportStaxPointwise(t *testing.T) {
	exportTempDB(t)
	if out, err := executeRoot(t,
		"registry", "create",
		"--id", "acme/helpfulness",
		"--kind", "pointwise",
		"--prompt", "Rate {{response}}.",
	); err != nil {
		t.Fatalf("registry create pointwise: %v (out=%q)", err, out)
	}

	out, err := executeRoot(t, "export", "stax", "--metric", "acme/helpfulness")
	if err != nil {
		t.Fatalf("export stax: %v (out=%q)", err, out)
	}
	var ev staxexport.Evaluator
	if err := json.Unmarshal([]byte(out), &ev); err != nil {
		t.Fatalf("pointwise output is not a single JSON object: %v\n%s", err, out)
	}
	if ev.Name != "acme/helpfulness" {
		t.Errorf("name = %q, want acme/helpfulness", ev.Name)
	}
	if ev.Prompt.Content != "Rate {{output}}." {
		t.Errorf("prompt = %q, want %q (response -> output)", ev.Prompt.Content, "Rate {{output}}.")
	}
}

func TestExportStaxFailClosed(t *testing.T) {
	exportTempDB(t)

	// pairwise
	if out, err := executeRoot(t,
		"registry", "create", "--id", "acme/pw", "--kind", "pairwise",
		"--candidate-field", "a", "--baseline-field", "b",
	); err != nil {
		t.Fatalf("create pairwise: %v (out=%q)", err, out)
	}
	// custom_schema
	if out, err := executeRoot(t,
		"registry", "create", "--id", "acme/cs", "--kind", "custom_schema",
		"--response-schema", `{"type":"object"}`,
	); err != nil {
		t.Fatalf("create custom_schema: %v (out=%q)", err, out)
	}

	for _, id := range []string{"acme/pw", "acme/cs"} {
		out, err := executeRoot(t, "export", "stax", "--metric", id)
		if err == nil {
			t.Fatalf("export %s: expected unsupported error, got nil (out=%q)", id, out)
		}
		if !strings.Contains(err.Error(), "unsupported in v1") {
			t.Errorf("export %s error = %v, want 'unsupported in v1'", id, err)
		}
	}
}

func TestExportStaxFailClosedWritesNothing(t *testing.T) {
	exportTempDB(t)
	if out, err := executeRoot(t,
		"registry", "create", "--id", "acme/pw", "--kind", "pairwise",
		"--candidate-field", "a", "--baseline-field", "b",
	); err != nil {
		t.Fatalf("create pairwise: %v (out=%q)", err, out)
	}
	outFile := filepath.Join(t.TempDir(), "evaluator.json")
	if _, err := executeRoot(t, "export", "stax", "--metric", "acme/pw", "--out", outFile); err == nil {
		t.Fatal("expected unsupported error, got nil")
	}
	if _, err := os.Stat(outFile); !os.IsNotExist(err) {
		t.Errorf("fail-closed export must write nothing, but %q exists (stat err=%v)", outFile, err)
	}
}

func TestExportStaxUnmappedPlaceholder(t *testing.T) {
	exportTempDB(t)
	if out, err := executeRoot(t,
		"registry", "create", "--id", "acme/weird", "--kind", "pointwise",
		"--prompt", "Rate {{response}} via {{frobnicate}}.",
	); err != nil {
		t.Fatalf("create: %v (out=%q)", err, out)
	}

	out, err := executeRoot(t, "export", "stax", "--metric", "acme/weird")
	if err == nil {
		t.Fatalf("expected hard error for unmapped placeholder, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error = %v, want it to name the unmapped field", err)
	}

	// The override lets it export.
	if _, err := executeRoot(t, "export", "stax", "--metric", "acme/weird",
		"--placeholder-map", "frobnicate=history"); err != nil {
		t.Errorf("override should allow export: %v", err)
	}
}

func TestExportStaxToFile(t *testing.T) {
	exportTempDB(t)
	createRubricBrand(t)
	outFile := filepath.Join(t.TempDir(), "evaluator.json")

	if _, err := executeRoot(t, "export", "stax", "--metric", "acme/rubric-brand", "--out", outFile); err != nil {
		t.Fatalf("export --out: %v", err)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read out file: %v", err)
	}
	var evs []staxexport.Evaluator
	if err := json.Unmarshal(data, &evs); err != nil {
		t.Fatalf("file is not evaluator JSON: %v\n%s", err, data)
	}
	if len(evs) != 3 {
		t.Errorf("file has %d evaluators, want 3", len(evs))
	}
}

func TestExportStaxRequiresMetric(t *testing.T) {
	out, err := executeRoot(t, "export", "stax")
	if err == nil {
		t.Fatalf("expected error for missing --metric, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "--metric is required") {
		t.Errorf("error = %v, want '--metric is required'", err)
	}
}

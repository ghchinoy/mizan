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

	"github.com/ghchinoy/mizan/internal/eval"
)

// setupHeuristicCLIEnv points the registry + results stores at throwaway SQLite
// files so a CLI heuristic run touches no developer DB and — critically — needs
// NO project, NO ADC, and NO network (a heuristic is credential-free, §4.B).
// PROJECT_ID is deliberately NOT set: the heuristic path must work without one.
func setupHeuristicCLIEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MIZAN_REGISTRY_DB", filepath.Join(dir, "registry.db"))
	t.Setenv("MIZAN_RESULTS_BACKEND", "sqlite")
	t.Setenv("MIZAN_RESULTS_DB", filepath.Join(dir, "results.db"))
}

// TestCLIHeuristicRunCredentialFree drives the real cobra commands end-to-end for
// EACH supported check type with NO ADC and NO network (design §9 B2): create a
// kind:heuristic template via `registry create` (using the new --heuristic-*
// flags), run it via `eval run`, and assert the score is exactly the expected
// pass/fail value. Because no PROJECT_ID / ADC is configured, any attempt to
// reach Vertex/genai would fail — so a green run proves the path is offline.
func TestCLIHeuristicRunCredentialFree(t *testing.T) {
	schema := `{"type":"object","required":["a"],"properties":{"a":{"type":"integer"}}}`
	cases := []struct {
		name       string
		createArgs []string
		field      string
		wantScore  float32
	}{
		{
			name: "contains-pass",
			createArgs: []string{
				"--heuristic-type", "contains", "--heuristic-target", "response", "--heuristic-value", "OK",
			},
			field:     "response=all OK here",
			wantScore: 1,
		},
		{
			name: "contains-fail",
			createArgs: []string{
				"--heuristic-type", "contains", "--heuristic-target", "response", "--heuristic-value", "OK",
			},
			field:     "response=nothing to see",
			wantScore: 0,
		},
		{
			name: "regex-pass",
			createArgs: []string{
				"--heuristic-type", "regex", "--heuristic-target", "response", "--heuristic-value", `^\d{3}-\d{4}$`,
			},
			field:     "response=123-4567",
			wantScore: 1,
		},
		{
			name: "equals-pass",
			createArgs: []string{
				"--heuristic-type", "equals", "--heuristic-target", "response", "--heuristic-value", "yes",
			},
			field:     "response=yes",
			wantScore: 1,
		},
		{
			name: "json-valid-pass",
			createArgs: []string{
				"--heuristic-type", "json-valid", "--heuristic-target", "response",
			},
			field:     `response={"a":1}`,
			wantScore: 1,
		},
		{
			name: "json-schema-valid-fail",
			createArgs: []string{
				"--heuristic-type", "json-schema-valid", "--heuristic-target", "response", "--heuristic-schema", schema,
			},
			field:     `response={"a":"not-an-integer"}`,
			wantScore: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupHeuristicCLIEnv(t)
			id := "checks/" + tc.name

			createArgs := append([]string{
				"registry", "create",
				"--id", id,
				"--name", "Heuristic " + tc.name,
				"--kind", "heuristic",
				"--modality", "text",
				"--input", "response:text:true",
			}, tc.createArgs...)
			if out, err := executeRoot(t, createArgs...); err != nil {
				t.Fatalf("registry create: %v (out=%q)", err, out)
			}

			runOut, err := executeRoot(t,
				"--output", "json",
				"eval", "run",
				"--metric", id,
				"--field", tc.field,
			)
			if err != nil {
				t.Fatalf("eval run: %v (out=%q)", err, runOut)
			}

			// The JSON result is on stdout; the "heuristic: no autorater" pre-flight
			// is on stderr (both captured in the same buffer), so decode only the JSON
			// object.
			res := decodeResultJSON(t, runOut)
			if res.Score == nil {
				t.Fatalf("heuristic result had no score (out=%q)", runOut)
			}
			if *res.Score != tc.wantScore {
				t.Errorf("Score = %v, want %v", *res.Score, tc.wantScore)
			}
			if strings.TrimSpace(res.Explanation) == "" {
				t.Error("heuristic result had no explanation")
			}
			// A heuristic resolves no autorater: the pre-flight must say so.
			if !strings.Contains(runOut, "heuristic: no autorater") {
				t.Errorf("expected a 'heuristic: no autorater' pre-flight, got:\n%s", runOut)
			}
		})
	}
}

// TestCLIHeuristicRunPersistsAndShows proves a heuristic run persists with
// kind=heuristic and an empty applied autorater, and that `results show` renders
// it (design §9 B2).
func TestCLIHeuristicRunPersistsAndShows(t *testing.T) {
	setupHeuristicCLIEnv(t)
	id := "checks/persist"

	if out, err := executeRoot(t,
		"registry", "create",
		"--id", id,
		"--name", "Persisted heuristic",
		"--kind", "heuristic",
		"--modality", "text",
		"--input", "response:text:true",
		"--heuristic-type", "contains",
		"--heuristic-target", "response",
		"--heuristic-value", "OK",
	); err != nil {
		t.Fatalf("registry create: %v (out=%q)", err, out)
	}

	runOut, err := executeRoot(t,
		"--output", "json",
		"eval", "run",
		"--metric", id,
		"--field", "response=all OK here",
	)
	if err != nil {
		t.Fatalf("eval run: %v (out=%q)", err, runOut)
	}

	// Find the persisted run via `results list --output json` and show it.
	listOut, err := executeRoot(t, "--output", "json", "results", "list")
	if err != nil {
		t.Fatalf("results list: %v (out=%q)", err, listOut)
	}
	runID := firstRunID(t, listOut)

	showOut, err := executeRoot(t, "results", "show", runID)
	if err != nil {
		t.Fatalf("results show: %v (out=%q)", err, showOut)
	}
	if !strings.Contains(showOut, "heuristic") {
		t.Errorf("results show did not render kind heuristic:\n%s", showOut)
	}
}

// TestCLIHeuristicRunWarnsOnModelFlag proves the friendliness fold-in: passing
// --model to a kind:heuristic run (which ignores it via the early return) emits a
// one-line warning that the model flag is ignored, and the run still succeeds.
func TestCLIHeuristicRunWarnsOnModelFlag(t *testing.T) {
	setupHeuristicCLIEnv(t)
	id := "checks/warn-model"

	if out, err := executeRoot(t,
		"registry", "create",
		"--id", id,
		"--name", "Warn model heuristic",
		"--kind", "heuristic",
		"--modality", "text",
		"--input", "response:text:true",
		"--heuristic-type", "contains",
		"--heuristic-target", "response",
		"--heuristic-value", "OK",
	); err != nil {
		t.Fatalf("registry create: %v (out=%q)", err, out)
	}

	runOut, err := executeRoot(t,
		"--output", "json",
		"eval", "run",
		"--metric", id,
		"--field", "response=all OK here",
		"--model", "gemini-2.5-pro",
	)
	if err != nil {
		t.Fatalf("eval run: %v (out=%q)", err, runOut)
	}
	if !strings.Contains(runOut, "--model is ignored for kind:heuristic") {
		t.Errorf("expected a --model-ignored warning, got:\n%s", runOut)
	}
}

// decodeResultJSON extracts and decodes the eval.Result JSON object from combined
// stdout+stderr output (the pre-flight line is plain text on stderr).
func decodeResultJSON(t *testing.T, out string) eval.Result {
	t.Helper()
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("no JSON object in output:\n%s", out)
	}
	var res eval.Result
	if err := json.Unmarshal([]byte(out[start:end+1]), &res); err != nil {
		t.Fatalf("decode result JSON: %v (raw=%q)", err, out[start:end+1])
	}
	return res
}

// firstRunID pulls the first run id out of a `results list --output json` array.
func firstRunID(t *testing.T, out string) string {
	t.Helper()
	start := strings.Index(out, "[")
	end := strings.LastIndex(out, "]")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("no JSON array in results list output:\n%s", out)
	}
	var rows []struct {
		RunID string `json:"RunID"`
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &rows); err != nil {
		t.Fatalf("decode results list: %v (raw=%q)", err, out[start:end+1])
	}
	if len(rows) == 0 || rows[0].RunID == "" {
		t.Fatalf("no run id in results list:\n%s", out)
	}
	return rows[0].RunID
}

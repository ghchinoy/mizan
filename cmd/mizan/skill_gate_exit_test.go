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

// This is the gate-exit-code coverage gate for the run-eval-set skill: it proves
// the exit-code truth table DOCUMENTED in run-eval-set's SKILL.md matches what the
// CLI actually does. It is HERMETIC — it does NOT run a live eval-set (no network,
// no ADC): it drives the real evalSetGateError (the exact function
// cmd/mizan/evalset.go turns a set result into a process exit code with) across
// the full gate × verdict truth table, parses the SKILL.md's documented
// gate-exit-code block, and asserts they agree cell-for-cell. If the skill claims
// an exit code the CLI does not produce (or the CLI's rule changes), this fails.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/evalset"
)

// gateExitMarker precedes the fenced json block documenting the gate exit-code
// truth table in run-eval-set's SKILL.md.
const gateExitMarker = "<!-- gate-exit-code -->"

// gateExitFenceRe matches a fenced ```json block (indent-tolerant), mirroring the
// skilldocs drift gate's fence matcher.
var gateExitFenceRe = regexp.MustCompile("(?ms)^[ \t]*```json[ \t]*\r?\n(.*?)^[ \t]*```")

// runEvalSetSkillPath resolves the run-eval-set SKILL.md from this test file's own
// location so the gate is independent of the working directory `go test` runs
// from. cmd/mizan lives at <repo>/cmd/mizan, so "../.." is the repo root.
func runEvalSetSkillPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate SKILL.md")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..",
		"plugins", "mizan-eval", "skills", "run-eval-set", "SKILL.md")
}

// documentedGateExitTable extracts the gate-exit-code json block (a map of
// "gate=<bool>, verdict=<VERDICT>" -> exit code) documented in the SKILL.md.
func documentedGateExitTable(t *testing.T) map[string]float64 {
	t.Helper()
	raw, err := os.ReadFile(runEvalSetSkillPath(t))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	text := string(raw)
	mi := strings.Index(text, gateExitMarker)
	if mi < 0 {
		t.Fatalf("SKILL.md is missing the %q marker before the gate exit-code json block", gateExitMarker)
	}
	m := gateExitFenceRe.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```json block found after the %q marker in SKILL.md", gateExitMarker)
	}
	var table map[string]float64
	if err := json.Unmarshal([]byte(m[1]), &table); err != nil {
		t.Fatalf("documented gate exit-code block is not valid JSON: %v\nblock:\n%s", err, m[1])
	}
	return table
}

// TestRunEvalSetGateExitMatchesCLI proves the SKILL.md's documented gate exit-code
// truth table matches the CLI's real evalSetGateError across every gate × verdict
// combination. evalSetGateError returns a non-nil error exactly when the process
// exits non-zero (cobra prints it and main() exits 1), so nil -> exit 0, non-nil
// -> exit 1.
func TestRunEvalSetGateExitMatchesCLI(t *testing.T) {
	documented := documentedGateExitTable(t)

	cases := []struct {
		key     string
		gate    bool
		verdict evalset.SetVerdict
	}{
		{"gate=false, verdict=PASSED", false, evalset.Passed},
		{"gate=false, verdict=FAILED", false, evalset.Failed},
		{"gate=true, verdict=PASSED", true, evalset.Passed},
		{"gate=true, verdict=FAILED", true, evalset.Failed},
	}

	if len(documented) != len(cases) {
		t.Errorf("documented gate exit-code table has %d entries; want %d (one per gate×verdict cell): %v",
			len(documented), len(cases), documented)
	}

	for _, tc := range cases {
		wantExit, ok := documented[tc.key]
		if !ok {
			t.Errorf("SKILL.md gate exit-code table is missing the %q cell", tc.key)
			continue
		}

		// The CLI's real rule: non-nil error => non-zero exit (1), nil => exit 0.
		err := evalSetGateError(evalset.EvalSetResult{SetID: "quickstart/x", Gate: tc.gate, Verdict: tc.verdict})
		cliExit := 0.0
		if err != nil {
			cliExit = 1.0
		}

		if wantExit != cliExit {
			t.Errorf("gate exit-code drift for %q: SKILL.md documents %v, CLI evalSetGateError yields %v",
				tc.key, wantExit, cliExit)
		}
	}
}

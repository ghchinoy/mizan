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

// registry_kind_alias_e2e_test.go drives the vernacular kind aliases through the
// REAL cobra commands end-to-end against a throwaway SQLite registry (no project
// or network needed — registry create/list only touch the local DB). It closes
// the two boundaries the in-process apply()/NormalizeKind unit tests don't reach
// on their own: `registry create --kind <alias>` persists the canonical kind,
// and the `registry list --kind <alias>` FILTER folds the alias before querying.
// These are cgo-free (CGO_ENABLED=0 SQLite driver) and hermetic.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// useIsolatedRegistry blanks the config environment (so no real .env leaks in)
// and points the registry at a fresh SQLite file for the test.
func useIsolatedRegistry(t *testing.T) {
	t.Helper()
	cleanConfigEnv(t)
	t.Setenv("MIZAN_REGISTRY_DB", filepath.Join(t.TempDir(), "registry.db"))
}

// listKinds runs `registry list --kind <spelling> --output json` and returns the
// stored kinds of the matched templates.
func listKinds(t *testing.T, spelling string) []registry.MetricKind {
	t.Helper()
	out, err := executeRoot(t, "--output", "json", "registry", "list", "--kind", spelling)
	if err != nil {
		t.Fatalf("registry list --kind %s: %v (out=%q)", spelling, err, out)
	}
	var got []registry.MetricTemplate
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode list output %q: %v", out, err)
	}
	kinds := make([]registry.MetricKind, 0, len(got))
	for _, tmpl := range got {
		kinds = append(kinds, tmpl.Kind)
	}
	return kinds
}

// TestRegistryCreateWithKindAliasPersistsCanonical proves `registry create
// --kind single|compare` stores the canonical kind in the registry, so anything
// reading the template later (including the eval engine) sees only the canonical
// spelling.
func TestRegistryCreateWithKindAliasPersistsCanonical(t *testing.T) {
	cases := []struct {
		id       string
		spelling string
		want     registry.MetricKind
		extra    []string
	}{
		{"test/single-metric", "single", registry.KindPointwise, nil},
		{"test/compare-metric", "compare", registry.KindPairwise, nil},
		{"test/bool-metric", "bool", registry.KindBoul, nil},
		{"test/boolean-metric", "boolean", registry.KindBoul, nil},
		{"test/boul-metric", "boul", registry.KindBoul, nil},
		{"test/grade-metric", "grade", registry.KindScore, nil},
		{"test/score-metric", "score", registry.KindScore, nil},
		{"test/choice-metric", "choice", registry.KindChoice, []string{"--choices", "option_a,option_b"}},
		{"test/classify-metric", "classify", registry.KindChoice, []string{"--choices", "option_a,option_b"}},
	}
	for _, tc := range cases {
		t.Run(tc.spelling, func(t *testing.T) {
			useIsolatedRegistry(t)
			args := []string{
				"--output", "json",
				"registry", "create",
				"--id", tc.id,
				"--kind", tc.spelling,
				"--prompt", "Judge {{response}}",
			}
			args = append(args, tc.extra...)
			out, err := executeRoot(t, args...)
			if err != nil {
				t.Fatalf("registry create --kind %s: %v (out=%q)", tc.spelling, err, out)
			}
			var created registry.MetricTemplate
			if err := json.Unmarshal([]byte(out), &created); err != nil {
				t.Fatalf("decode create output %q: %v", out, err)
			}
			if created.Kind != tc.want {
				t.Errorf("created template Kind = %q, want canonical %q", created.Kind, tc.want)
			}
		})
	}
}

// TestRegistryListKindAliasFiltersSameAsCanonical proves the list --kind filter
// accepts the aliases and folds them to the canonical kind: `--kind single`
// returns exactly the pointwise templates (same as `--kind pointwise`) and
// `--kind compare` returns exactly the pairwise templates (same as `--kind
// pairwise`).
func TestRegistryListKindAliasFiltersSameAsCanonical(t *testing.T) {
	useIsolatedRegistry(t)

	// Seed one pointwise and one pairwise template (authored via canonical
	// spellings so the filter — not the create path — is what's under test).
	if out, err := executeRoot(t, "registry", "create",
		"--id", "test/pw", "--kind", "pointwise", "--prompt", "Judge {{response}}"); err != nil {
		t.Fatalf("seed pointwise: %v (out=%q)", err, out)
	}
	if out, err := executeRoot(t, "registry", "create",
		"--id", "test/pair", "--kind", "pairwise", "--prompt", "Compare {{baseline}} {{candidate}}"); err != nil {
		t.Fatalf("seed pairwise: %v (out=%q)", err, out)
	}
	if out, err := executeRoot(t, "registry", "create",
		"--id", "test/boul", "--kind", "boul", "--prompt", "Is safe? {{response}}"); err != nil {
		t.Fatalf("seed boul: %v (out=%q)", err, out)
	}
	if out, err := executeRoot(t, "registry", "create",
		"--id", "test/choice", "--kind", "choice", "--choices", "a,b", "--prompt", "Choose {{response}}"); err != nil {
		t.Fatalf("seed choice: %v (out=%q)", err, out)
	}
	if out, err := executeRoot(t, "registry", "create",
		"--id", "test/score", "--kind", "score", "--prompt", "Rate {{response}}"); err != nil {
		t.Fatalf("seed score: %v (out=%q)", err, out)
	}

	single := listKinds(t, "single")
	pointwise := listKinds(t, "pointwise")
	if len(single) != 1 || single[0] != registry.KindPointwise {
		t.Errorf("list --kind single = %v, want exactly [pointwise]", single)
	}
	if !equalKinds(single, pointwise) {
		t.Errorf("list --kind single (%v) != list --kind pointwise (%v)", single, pointwise)
	}

	compare := listKinds(t, "compare")
	pairwise := listKinds(t, "pairwise")
	if len(compare) != 1 || compare[0] != registry.KindPairwise {
		t.Errorf("list --kind compare = %v, want exactly [pairwise]", compare)
	}
	if !equalKinds(compare, pairwise) {
		t.Errorf("list --kind compare (%v) != list --kind pairwise (%v)", compare, pairwise)
	}

	boolList := listKinds(t, "bool")
	boulList := listKinds(t, "boul")
	if len(boolList) != 1 || boolList[0] != registry.KindBoul {
		t.Errorf("list --kind bool = %v, want exactly [boul]", boolList)
	}
	if !equalKinds(boolList, boulList) {
		t.Errorf("list --kind bool (%v) != list --kind boul (%v)", boolList, boulList)
	}

	classifyList := listKinds(t, "classify")
	choiceList := listKinds(t, "choice")
	if len(classifyList) != 1 || classifyList[0] != registry.KindChoice {
		t.Errorf("list --kind classify = %v, want exactly [choice]", classifyList)
	}
	if !equalKinds(classifyList, choiceList) {
		t.Errorf("list --kind classify (%v) != list --kind choice (%v)", classifyList, choiceList)
	}

	gradeList := listKinds(t, "grade")
	scoreList := listKinds(t, "score")
	if len(gradeList) != 1 || gradeList[0] != registry.KindScore {
		t.Errorf("list --kind grade = %v, want exactly [score]", gradeList)
	}
	if !equalKinds(gradeList, scoreList) {
		t.Errorf("list --kind grade (%v) != list --kind score (%v)", gradeList, scoreList)
	}
}

// TestRegistryListKindUnknownErrors proves an unknown --kind filter value is
// rejected with the enumerated message (the same NormalizeKind guard as create).
func TestRegistryListKindUnknownErrors(t *testing.T) {
	useIsolatedRegistry(t)
	out, err := executeRoot(t, "registry", "list", "--kind", "bogus")
	if err == nil {
		t.Fatalf("registry list --kind bogus: nil error, want an error (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "unknown metric kind") {
		t.Errorf("error = %v, want it to mention unknown metric kind", err)
	}
}

// TestRegistryListKindInvalidFailsFastBeforeDBOpen proves the --kind filter is
// normalized/validated BEFORE the SQLite DB is opened: an invalid filter value
// surfaces the enumerated "unknown metric kind" error even when the registry DB
// path can never be opened, so no DB work is attempted (ITEM C fast-fail).
func TestRegistryListKindInvalidFailsFastBeforeDBOpen(t *testing.T) {
	cleanConfigEnv(t)
	// Point the registry at an unopenable path: a regular file stands where the
	// DB's parent directory would be, so OpenService's MkdirAll fails. If the
	// --kind were validated after the open, we'd see this DB error instead.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}
	t.Setenv("MIZAN_REGISTRY_DB", filepath.Join(blocker, "registry.db"))

	out, err := executeRoot(t, "registry", "list", "--kind", "bogus")
	if err == nil {
		t.Fatalf("registry list --kind bogus: nil error, want an error (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "unknown metric kind") {
		t.Errorf("error = %v, want the enumerated kind error (proving fast-fail before DB open)", err)
	}
}

func equalKinds(a, b []registry.MetricKind) bool {
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

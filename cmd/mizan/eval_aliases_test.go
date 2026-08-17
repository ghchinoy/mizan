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

// eval_aliases_test.go covers the plain-vernacular subcommand aliases (ITEM C):
// `mizan eval single` resolves to `eval run` (score one response, a.k.a.
// pointwise) and `mizan eval compare` resolves to `eval pairwise` (compare two
// responses, a.k.a. pairwise). The canonical spellings keep working unchanged.

import (
	"strings"
	"testing"
)

// TestEvalSubcommandAliasesResolve proves the aliases route to the same cobra
// command as their canonical spelling via cobra's own Find resolution.
func TestEvalSubcommandAliasesResolve(t *testing.T) {
	cases := []struct {
		alias     string
		canonical string
	}{
		{"single", "run"},
		{"compare", "pairwise"},
	}
	for _, tc := range cases {
		root := newRootCmd()
		aliasCmd, _, err := root.Find([]string{"eval", tc.alias})
		if err != nil {
			t.Fatalf("Find([eval %s]): %v", tc.alias, err)
		}
		root2 := newRootCmd()
		canonCmd, _, err := root2.Find([]string{"eval", tc.canonical})
		if err != nil {
			t.Fatalf("Find([eval %s]): %v", tc.canonical, err)
		}
		if aliasCmd.Name() != canonCmd.Name() {
			t.Errorf("alias %q resolved to %q, want %q", tc.alias, aliasCmd.Name(), canonCmd.Name())
		}
	}
}

// TestEvalSingleHitsRunCommand confirms invoking via the alias reaches the same
// RunE (it fails on run's mutual-exclusivity guard, before any config/DB/API).
func TestEvalSingleHitsRunCommand(t *testing.T) {
	out, err := executeRoot(t, "eval", "single")
	if err == nil {
		t.Fatalf("expected error for missing --metric/--set via `eval single`, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "exactly one of --metric or --set is required") {
		t.Errorf("error = %v, want exactly one of --metric or --set is required (proves it hit `eval run`)", err)
	}
}

// TestEvalCompareHitsPairwiseCommand confirms the compare alias reaches the
// pairwise RunE (it fails on pairwise's --metric guard).
func TestEvalCompareHitsPairwiseCommand(t *testing.T) {
	out, err := executeRoot(t, "eval", "compare")
	if err == nil {
		t.Fatalf("expected error for missing --metric via `eval compare`, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "--metric is required") {
		t.Errorf("error = %v, want --metric is required (proves it hit `eval pairwise`)", err)
	}
}

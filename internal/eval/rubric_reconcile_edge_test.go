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

package eval

import (
	"context"
	"strings"
	"testing"
)

// These tests close the R-R2 reconciliation edge-case gaps the primary
// rubric_reconcile_test.go leaves thin, and — crucially — assert the EXACT,
// deterministic text of the error and warning payloads so the suite catches any
// future reordering or reformatting regression (the existing tests only use
// substring/Contains checks, which would not detect a scrambled pair list).
//
// The authored fixture is rubricTemplate():
//
//	clarity -> "The message is unambiguous", "No jargon"   (declared order)
//	tone    -> "Matches a professional brand voice"
//
// Deterministic ordering contract exercised here:
//   - MISSING / DUPLICATE pairs are reported in AUTHORED order: group names
//     sorted (clarity < tone), criteria in declared order within a group.
//   - When both categories fire, the message is "missing ...; duplicated ...".
//   - EXTRA warnings are emitted in the order the judge RETURNED them.

// TestReconcileEmptyPerCriterionAllMissing proves an empty per_criterion array
// (the degenerate "judge returned nothing" case) reports EVERY authored pair as
// missing, in the exact authored order, as a single hard error.
func TestReconcileEmptyPerCriterionAllMissing(t *testing.T) {
	resp := `{"per_criterion": [], "overall_score": 3, "explanation": "empty"}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err == nil {
		t.Fatal("expected error when per_criterion is empty (all authored criteria missing), got nil")
	}
	want := `eval: rubric reconciliation failed: missing authored criterion(s): ` +
		`[group="clarity" criterion="The message is unambiguous", ` +
		`group="clarity" criterion="No jargon", ` +
		`group="tone" criterion="Matches a professional brand voice"]`
	if err.Error() != want {
		t.Errorf("error mismatch\n got: %s\nwant: %s", err.Error(), want)
	}
}

// TestReconcileMatchingIsWhitespaceAndCaseExact documents that pair identity is
// matched by EXACT string equality on both group and criterion: any difference
// in case or surrounding whitespace makes the authored pair MISSING (a hard
// error) — the judge's near-miss is NOT silently accepted. Authored fixture is a
// single pair so each variant isolates one kind of near-miss.
func TestReconcileMatchingIsWhitespaceAndCaseExact(t *testing.T) {
	cases := []struct {
		name          string
		returnedGroup string
		returnedCrit  string
	}{
		{"criterion trailing whitespace", "clarity", "No jargon "},
		{"criterion leading whitespace", "clarity", " No jargon"},
		{"criterion case differs", "clarity", "no jargon"},
		{"group case differs", "Clarity", "No jargon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := rubricTemplate()
			tmpl.RubricGroups = map[string][]string{"clarity": {"No jargon"}}
			resp := `{"per_criterion":[{"group":"` + tc.returnedGroup +
				`","criterion":"` + tc.returnedCrit +
				`","score":4,"rationale":"near miss"}],"overall_score":4,"explanation":"x"}`
			fg := &fakeGenai{respText: resp}
			eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

			_, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetail(1, 5))
			if err == nil {
				t.Fatalf("expected missing-criterion error for near-miss %q/%q (matching is exact), got nil",
					tc.returnedGroup, tc.returnedCrit)
			}
			// The authored (exact) pair must be reported missing.
			want := `eval: rubric reconciliation failed: missing authored criterion(s): ` +
				`[group="clarity" criterion="No jargon"]`
			if err.Error() != want {
				t.Errorf("error mismatch\n got: %s\nwant: %s", err.Error(), want)
			}
		})
	}
}

// TestReconcileCombinedMissingAndExtraFailsClosed proves that when a response has
// BOTH a missing authored pair and an extra (unauthored) pair, the run FAILS on
// the missing pair (scorecard-corrupting) and the extra is NOT surfaced — no
// warning leaks out of the error path, and the extra's name never appears in the
// error. Reconciliation fails closed: a corrupt scorecard is never returned.
func TestReconcileCombinedMissingAndExtraFailsClosed(t *testing.T) {
	// "No jargon" omitted (missing); "Uses active voice" added (extra).
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"clear"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"ok"},
			{"group":"style","criterion":"Uses active voice","score":5,"rationale":"bonus"}
		],
		"overall_score": 4,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err == nil {
		t.Fatal("expected error (missing authored criterion) even alongside an extra, got nil")
	}
	want := `eval: rubric reconciliation failed: missing authored criterion(s): ` +
		`[group="clarity" criterion="No jargon"]`
	if err.Error() != want {
		t.Errorf("error mismatch\n got: %s\nwant: %s", err.Error(), want)
	}
	// The extra must NOT be reported in the error (it is only ever a warning, and
	// warnings are suppressed once the run errors).
	if strings.Contains(err.Error(), "Uses active voice") {
		t.Errorf("extra criterion leaked into error message: %s", err.Error())
	}
	// Fail-closed: no partial/warning-bearing Result is returned on error.
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none on the error path", res.Warnings)
	}
	if res.CustomOutput != nil {
		t.Errorf("CustomOutput = %v, want nil on the error path", res.CustomOutput)
	}
}

// TestReconcileMissingAndDuplicateReportedTogether proves both scorecard-
// corrupting categories are reported in ONE error (so a single run surfaces every
// problem), joined deterministically as "missing ...; duplicated ...". The exact
// string is asserted so a reordering regression is caught.
func TestReconcileMissingAndDuplicateReportedTogether(t *testing.T) {
	// "The message is unambiguous" omitted (missing); "No jargon" returned twice
	// (duplicate); tone present once.
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"No jargon","score":5,"rationale":"a"},
			{"group":"clarity","criterion":"No jargon","score":2,"rationale":"b"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"ok"}
		],
		"overall_score": 4,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err == nil {
		t.Fatal("expected combined missing+duplicate error, got nil")
	}
	want := `eval: rubric reconciliation failed: ` +
		`missing authored criterion(s): [group="clarity" criterion="The message is unambiguous"]; ` +
		`duplicated authored criterion(s): [group="clarity" criterion="No jargon"]`
	if err.Error() != want {
		t.Errorf("error mismatch\n got: %s\nwant: %s", err.Error(), want)
	}
}

// TestReconcileMultipleExtrasWarnInReturnedOrder proves that with several extras,
// one warning is emitted PER extra in the order the judge returned them (not
// sorted, not deduped), and the exact warning text is stable. This locks the
// warning ordering so downstream assertions and CLI output stay deterministic.
func TestReconcileMultipleExtrasWarnInReturnedOrder(t *testing.T) {
	// All three authored pairs present (no error), plus two extras returned in the
	// order zeta-first, alpha-second — deliberately NOT alphabetical.
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"clear"},
			{"group":"clarity","criterion":"No jargon","score":5,"rationale":"plain"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"ok"},
			{"group":"zeta","criterion":"Extra Z","score":5,"rationale":"z"},
			{"group":"alpha","criterion":"Extra A","score":5,"rationale":"a"}
		],
		"overall_score": 4,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err != nil {
		t.Fatalf("Run: %v (extras must not error)", err)
	}
	// Both extras kept in output alongside the three authored entries.
	if pc := res.CustomOutput["per_criterion"].([]any); len(pc) != 5 {
		t.Fatalf("per_criterion len = %d, want 5 (both extras kept)", len(pc))
	}
	wantWarnings := []string{
		`mizan: rubric reconciliation warning: judge returned unauthored criterion (group="zeta", criterion="Extra Z"); kept in output`,
		`mizan: rubric reconciliation warning: judge returned unauthored criterion (group="alpha", criterion="Extra A"); kept in output`,
	}
	if len(res.Warnings) != len(wantWarnings) {
		t.Fatalf("Warnings = %v, want %d in returned order", res.Warnings, len(wantWarnings))
	}
	for i, want := range wantWarnings {
		if res.Warnings[i] != want {
			t.Errorf("warning[%d] mismatch\n got: %s\nwant: %s", i, res.Warnings[i], want)
		}
	}
}

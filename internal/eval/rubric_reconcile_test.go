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

// The R-R2 reconciliation tests exercise runRubricStructured's strict
// per-criterion reconciliation against the AUTHORED (group, criterion) set from
// rubricTemplate():
//
//	clarity -> "The message is unambiguous", "No jargon"
//	tone    -> "Matches a professional brand voice"
//
// Identity is an exact string match on the (group, criterion) pair, using the
// same strings sent to the judge.

// TestReconcileHappyPath: exact 1:1 authored<->returned -> no error, no warnings.
func TestReconcileHappyPath(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"clear"},
			{"group":"clarity","criterion":"No jargon","score":5,"rationale":"plain"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"ok"}
		],
		"overall_score": 4,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", res.Warnings)
	}
	pc := res.CustomOutput["per_criterion"].([]any)
	if len(pc) != 3 {
		t.Errorf("per_criterion len = %d, want 3", len(pc))
	}
}

// TestReconcileMissingErrors: an authored criterion the judge did not return is a
// hard error whose message names the missing pair.
func TestReconcileMissingErrors(t *testing.T) {
	// "No jargon" omitted.
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"clear"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"ok"}
		],
		"overall_score": 4,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err == nil {
		t.Fatal("expected error for a missing authored criterion, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"eval:", "missing", "No jargon", "clarity"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

// TestReconcileDuplicateErrors: the same authored pair returned twice is a hard
// error whose message names the duplicated pair.
func TestReconcileDuplicateErrors(t *testing.T) {
	// "The message is unambiguous" returned twice; "No jargon" present once.
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"a"},
			{"group":"clarity","criterion":"The message is unambiguous","score":2,"rationale":"b"},
			{"group":"clarity","criterion":"No jargon","score":5,"rationale":"plain"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"ok"}
		],
		"overall_score": 4,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err == nil {
		t.Fatal("expected error for a duplicated authored criterion, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"eval:", "duplicat", "The message is unambiguous"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

// TestReconcileExtraWarnsAndPassesThrough: an unauthored criterion is kept in the
// output and recorded as a warning, without failing the run.
func TestReconcileExtraWarnsAndPassesThrough(t *testing.T) {
	// All three authored + one extra "Uses active voice".
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"clear"},
			{"group":"clarity","criterion":"No jargon","score":5,"rationale":"plain"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"ok"},
			{"group":"style","criterion":"Uses active voice","score":5,"rationale":"bonus"}
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
	// Extra kept in output: 4 entries survive.
	pc := res.CustomOutput["per_criterion"].([]any)
	if len(pc) != 4 {
		t.Fatalf("per_criterion len = %d, want 4 (extra kept)", len(pc))
	}
	// Exactly one warning, naming the extra pair.
	if len(res.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly 1", res.Warnings)
	}
	for _, want := range []string{"Uses active voice", "style"} {
		if !strings.Contains(res.Warnings[0], want) {
			t.Errorf("warning %q missing %q", res.Warnings[0], want)
		}
	}
}

// TestReconcileMultiGroupDistinct proves identical criterion strings in different
// groups are DISTINCT identities (matched by the full (group, criterion) pair):
// returning one entry per (group, criterion) is the happy path, with no error and
// no warnings, even though the criterion string repeats across groups.
func TestReconcileMultiGroupDistinct(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricGroups = map[string][]string{
		"accuracy":  {"Factually correct"},
		"relevance": {"Factually correct"}, // same string, different group
	}
	resp := `{
		"per_criterion": [
			{"group":"accuracy","criterion":"Factually correct","score":4,"rationale":"a"},
			{"group":"relevance","criterion":"Factually correct","score":5,"rationale":"b"}
		],
		"overall_score": 5,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetail(1, 5))
	if err != nil {
		t.Fatalf("Run: %v (per-(group,criterion) identities must be distinct)", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none (both pairs authored)", res.Warnings)
	}
	pc := res.CustomOutput["per_criterion"].([]any)
	if len(pc) != 2 {
		t.Errorf("per_criterion len = %d, want 2", len(pc))
	}
}

// TestReconcileMultiGroupWrongGroupIsMissingPlusExtra proves the pair identity is
// strict on BOTH fields: returning the right criterion string under the WRONG
// group leaves the authored (correct-group) pair MISSING (hard error) rather than
// silently matching on the criterion string alone.
func TestReconcileMultiGroupWrongGroupIsMissingPlusExtra(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricGroups = map[string][]string{
		"accuracy":  {"Factually correct"},
		"relevance": {"On topic"},
	}
	// "Factually correct" returned under "relevance" (wrong group): authored
	// (accuracy, Factually correct) is missing; (relevance, Factually correct) is
	// extra. Missing wins as a hard error.
	resp := `{
		"per_criterion": [
			{"group":"relevance","criterion":"Factually correct","score":4,"rationale":"a"},
			{"group":"relevance","criterion":"On topic","score":5,"rationale":"b"}
		],
		"overall_score": 5,
		"explanation": "fine"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetail(1, 5))
	if err == nil {
		t.Fatal("expected missing-criterion error (wrong group must not match), got nil")
	}
	if !strings.Contains(err.Error(), "accuracy") {
		t.Errorf("error %q should name the missing (accuracy, Factually correct) pair", err.Error())
	}
}

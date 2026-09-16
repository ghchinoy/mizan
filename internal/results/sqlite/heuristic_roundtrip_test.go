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

package sqlite

import (
	"context"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
)

// TestHeuristicRunRoundTrip is the B2 results-store acceptance check (design §9):
// a heuristic run persists through the SAME results.Service.Record path the CLI
// store hook uses, with a NIL eval.Result.Applied (a heuristic has no autorater).
// It proves the write path tolerates nil Applied, records kind=heuristic and
// Outcome.Score ∈ {0,1}, and stores an EMPTY AppliedAutorater — then reads it
// back through Get/List unchanged.
func TestHeuristicRunRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc := results.NewService(newStore(t))

	tmpl := registry.MetricTemplate{
		ID:          "checks/contains-ok",
		Version:     "1.0.0",
		Kind:        registry.KindHeuristic,
		ContentHash: "sha256:deadbeef",
		Heuristic:   &registry.HeuristicSpec{Type: registry.HeuristicContains, Target: "response", Value: "OK"},
	}
	score := float32(1.0)
	// A heuristic eval.Result: Score set, Applied deliberately NIL.
	out := eval.Result{Score: &score, Explanation: `matched: text contains "OK"`}
	if out.Applied != nil {
		t.Fatalf("precondition: expected nil eval.Result.Applied for a heuristic run")
	}

	rec, err := svc.Record(ctx, results.RecordInput{
		Command:  "eval run",
		Template: tmpl,
		Instance: eval.Instance{Fields: map[string]eval.AssetRef{
			"response": {Modality: registry.ModalityText, Text: "all OK here"},
		}},
		// Applied left as the zero value — the CLI store hook maps nil eval Applied
		// to this empty results.AppliedAutorater.
		Outcome: out,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := svc.Get(ctx, rec.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Template.Kind != registry.KindHeuristic {
		t.Errorf("Template.Kind = %q, want %q", got.Template.Kind, registry.KindHeuristic)
	}
	if got.Outcome.Score == nil || (*got.Outcome.Score != 0 && *got.Outcome.Score != 1) {
		t.Errorf("Outcome.Score = %v, want a value in {0,1}", got.Outcome.Score)
	}
	if *got.Outcome.Score != 1 {
		t.Errorf("Outcome.Score = %v, want 1 (pass)", *got.Outcome.Score)
	}
	// EMPTY applied autorater: a heuristic has no resolved model / host.
	if (got.Autorater != results.AppliedAutorater{}) {
		t.Errorf("expected an empty AppliedAutorater for a heuristic run, got %#v", got.Autorater)
	}
	// A heuristic is not a rubric, so no RubricRef is attached.
	if got.Rubric != nil {
		t.Errorf("expected nil Rubric for a heuristic run, got %#v", *got.Rubric)
	}

	// It is also discoverable by a kind filter.
	list, err := svc.List(ctx, results.ResultFilter{Kind: registry.KindHeuristic})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].RunID != rec.RunID {
		t.Errorf("List(kind=heuristic) = %d results, want exactly the recorded one", len(list))
	}
}

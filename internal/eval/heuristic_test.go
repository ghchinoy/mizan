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
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// heuristicTemplate builds a minimal KindHeuristic template with the given spec.
// It declares the target as a text input so the template is well-formed.
func heuristicTemplate(spec registry.HeuristicSpec) registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:         "test/heuristic",
		Name:       "Heuristic",
		Kind:       registry.KindHeuristic,
		Modalities: []registry.Modality{registry.ModalityText},
		Inputs:     []registry.InputSpec{{Name: spec.Target, Modality: registry.ModalityText, Required: true}},
		Heuristic:  &spec,
	}
}

func textInstance(field, text string) Instance {
	return Instance{Fields: map[string]AssetRef{
		field: {Modality: registry.ModalityText, Text: text},
	}}
}

// TestRunHeuristic_CredentialFree_AllSeamsNil is the load-bearing B2 test: a
// heuristic template must evaluate with ALL client seams nil (no Vertex client,
// no genai client, no global client) — i.e. with NO ADC and NO network — for
// EACH supported check type (design §9 B2). The engine is constructed WITHOUT
// WithGenaiClient/WithGlobalClient, so e.client, e.globalClient and e.genai are
// all nil; a run that reached any of them would panic.
func TestRunHeuristic_CredentialFree_AllSeamsNil(t *testing.T) {
	// A deliberately client-free engine: no native client, no genai, no global.
	eng := NewEngine(nil, "", "")
	if eng.client != nil || eng.genai != nil || eng.globalClient != nil {
		t.Fatalf("precondition: expected all client seams nil, got client=%v genai=%v global=%v", eng.client, eng.genai, eng.globalClient)
	}

	cases := []struct {
		name     string
		spec     registry.HeuristicSpec
		text     string
		wantPass bool
	}{
		{"contains-pass", registry.HeuristicSpec{Type: registry.HeuristicContains, Target: "response", Value: "OK"}, "all OK here", true},
		{"contains-fail", registry.HeuristicSpec{Type: registry.HeuristicContains, Target: "response", Value: "OK"}, "nothing here", false},
		{"contains-ci", registry.HeuristicSpec{Type: registry.HeuristicContains, Target: "response", Value: "ok", CaseInsensitive: true}, "all OK here", true},
		{"regex-pass", registry.HeuristicSpec{Type: registry.HeuristicRegex, Target: "response", Value: `^\d{3}-\d{4}$`}, "123-4567", true},
		{"regex-fail", registry.HeuristicSpec{Type: registry.HeuristicRegex, Target: "response", Value: `^\d{3}-\d{4}$`}, "not a phone", false},
		{"equals-pass", registry.HeuristicSpec{Type: registry.HeuristicEquals, Target: "response", Value: "yes"}, "yes", true},
		{"equals-fail", registry.HeuristicSpec{Type: registry.HeuristicEquals, Target: "response", Value: "yes"}, "no", false},
		{"json-valid-pass", registry.HeuristicSpec{Type: registry.HeuristicJSONValid, Target: "response"}, `{"a":1}`, true},
		{"json-valid-fail", registry.HeuristicSpec{Type: registry.HeuristicJSONValid, Target: "response"}, `{not json`, false},
		{"json-schema-valid-pass", registry.HeuristicSpec{Type: registry.HeuristicJSONSchemaValid, Target: "response", Schema: `{"type":"object","required":["a"],"properties":{"a":{"type":"integer"}}}`}, `{"a":1}`, true},
		{"json-schema-valid-fail", registry.HeuristicSpec{Type: registry.HeuristicJSONSchemaValid, Target: "response", Schema: `{"type":"object","required":["a"],"properties":{"a":{"type":"integer"}}}`}, `{"a":"x"}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := heuristicTemplate(tc.spec)
			res, err := eng.Run(context.Background(), tmpl, textInstance("response", tc.text))
			if err != nil {
				t.Fatalf("Run: unexpected error: %v", err)
			}
			if res.Score == nil {
				t.Fatalf("Run: expected a non-nil Score, got nil")
			}
			want := float32(0.0)
			if tc.wantPass {
				want = 1.0
			}
			if *res.Score != want {
				t.Errorf("Score = %v, want %v", *res.Score, want)
			}
			if res.Explanation == "" {
				t.Errorf("expected a deterministic Explanation, got empty")
			}
			// MODEL-BYPASS: a heuristic run stamps NO applied autorater.
			if res.Applied != nil {
				t.Errorf("expected nil Applied for a heuristic run, got %#v", *res.Applied)
			}
			// Duration is still recorded (cheap, useful for B3).
			if res.Stats.Duration < 0 {
				t.Errorf("expected a recorded Duration, got %v", res.Stats.Duration)
			}
		})
	}
}

// TestRunHeuristic_Deterministic proves the same input yields byte-identical
// score + explanation on repeated runs (no randomness, no clock in the verdict).
func TestRunHeuristic_Deterministic(t *testing.T) {
	eng := NewEngine(nil, "", "")
	tmpl := heuristicTemplate(registry.HeuristicSpec{Type: registry.HeuristicContains, Target: "response", Value: "OK"})
	inst := textInstance("response", "all OK here")

	r1, err := eng.Run(context.Background(), tmpl, inst)
	if err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	r2, err := eng.Run(context.Background(), tmpl, inst)
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if *r1.Score != *r2.Score || r1.Explanation != r2.Explanation {
		t.Errorf("non-deterministic: (%v,%q) != (%v,%q)", *r1.Score, r1.Explanation, *r2.Score, r2.Explanation)
	}
}

// TestRunHeuristic_MissingTargetField fails LOUD (not a silent 0.0) when the
// target field is not supplied on the instance.
func TestRunHeuristic_MissingTargetField(t *testing.T) {
	eng := NewEngine(nil, "", "")
	tmpl := heuristicTemplate(registry.HeuristicSpec{Type: registry.HeuristicContains, Target: "response", Value: "OK"})
	_, err := eng.Run(context.Background(), tmpl, textInstance("other", "x"))
	if err == nil {
		t.Fatal("expected an error for a missing target field, got nil")
	}
}

// TestRunHeuristic_BadRegexAtRun returns an error (never a silent fail) when a
// regex that slipped past validation cannot compile.
func TestRunHeuristic_BadRegexAtRun(t *testing.T) {
	eng := NewEngine(nil, "", "")
	tmpl := heuristicTemplate(registry.HeuristicSpec{Type: registry.HeuristicRegex, Target: "response", Value: "("})
	_, err := eng.Run(context.Background(), tmpl, textInstance("response", "x"))
	if err == nil {
		t.Fatal("expected an error for an invalid regex, got nil")
	}
}

// TestRunHeuristic_NonTextTargetRejected enforces the v1 text-only invariant.
func TestRunHeuristic_NonTextTargetRejected(t *testing.T) {
	eng := NewEngine(nil, "", "")
	tmpl := heuristicTemplate(registry.HeuristicSpec{Type: registry.HeuristicContains, Target: "response", Value: "OK"})
	inst := Instance{Fields: map[string]AssetRef{
		"response": {Modality: registry.ModalityImage, GCSUri: "gs://bucket/x.png"},
	}}
	if _, err := eng.Run(context.Background(), tmpl, inst); err == nil {
		t.Fatal("expected an error for a non-text target field, got nil")
	}
}

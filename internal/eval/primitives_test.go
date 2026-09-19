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

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

func TestRunBoul(t *testing.T) {
	cases := []struct {
		name       string
		judgeJSON  string
		wantPassed bool
		wantScore  float32
		wantConf   float32
		wantExpl   string
	}{
		{
			name:       "passed-true",
			judgeJSON:  `{"passed": true, "confidence": 0.95, "explanation": "The text adheres to safety guidelines."}`,
			wantPassed: true,
			wantScore:  1.0,
			wantConf:   0.95,
			wantExpl:   "The text adheres to safety guidelines.",
		},
		{
			name:       "passed-false",
			judgeJSON:  `{"passed": false, "confidence": 0.88, "explanation": "Contains unsafe elements."}`,
			wantPassed: false,
			wantScore:  0.0,
			wantConf:   0.88,
			wantExpl:   "Contains unsafe elements.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fg := &evaltest.FakeGenaiClient{}
			fg.PushJSON(tc.judgeJSON, nil)
			eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithGenaiClient(fg))

			tmpl := registry.MetricTemplate{
				ID:                   "safety/profanity-check",
				Kind:                 registry.KindBoul,
				MetricPromptTemplate: "Is this text safe? {{text}}",
			}

			inst := Instance{
				Fields: map[string]AssetRef{
					"text": {Modality: registry.ModalityText, Text: "Hello, world!"},
				},
			}

			res, err := eng.Run(context.Background(), tmpl, inst)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if res.Passed == nil || *res.Passed != tc.wantPassed {
				t.Errorf("Passed = %v, want %v", res.Passed, tc.wantPassed)
			}
			if res.Score == nil || *res.Score != tc.wantScore {
				t.Errorf("Score = %v, want %v", res.Score, tc.wantScore)
			}
			if res.Confidence == nil || *res.Confidence != tc.wantConf {
				t.Errorf("Confidence = %v, want %v", res.Confidence, tc.wantConf)
			}
			if res.Explanation != tc.wantExpl {
				t.Errorf("Explanation = %q, want %q", res.Explanation, tc.wantExpl)
			}
			if fg.Calls() != 1 {
				t.Errorf("expected 1 call to genai, got %d", fg.Calls())
			}
		})
	}
}

func TestRunChoice(t *testing.T) {
	fg := &evaltest.FakeGenaiClient{}
	fg.PushJSON(`{"selection": "technical", "explanation": "The user is asking about an API failure."}`, nil)
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithGenaiClient(fg))

	tmpl := registry.MetricTemplate{
		ID:                   "support/intent-classifier",
		Kind:                 registry.KindChoice,
		Choices:              []string{"billing", "technical", "sales", "general"},
		MetricPromptTemplate: "Classify this ticket: {{message}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"message": {Modality: registry.ModalityText, Text: "My database connection timed out with code 500."},
		},
	}

	res, err := eng.Run(context.Background(), tmpl, inst)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.ChoiceSelection != "technical" {
		t.Errorf("ChoiceSelection = %q, want %q", res.ChoiceSelection, "technical")
	}
	if res.Explanation != "The user is asking about an API failure." {
		t.Errorf("Explanation = %q, want %q", res.Explanation, "The user is asking about an API failure.")
	}
	if res.CustomOutput["selection"] != "technical" {
		t.Errorf("CustomOutput[selection] = %v, want %q", res.CustomOutput["selection"], "technical")
	}
}

func TestRunChoiceTooFewChoices(t *testing.T) {
	fg := &evaltest.FakeGenaiClient{}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithGenaiClient(fg))

	tmpl := registry.MetricTemplate{
		ID:                   "support/intent-classifier",
		Kind:                 registry.KindChoice,
		Choices:              []string{"only-one"},
		MetricPromptTemplate: "Classify this ticket: {{message}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"message": {Modality: registry.ModalityText, Text: "Hello"},
		},
	}

	if _, err := eng.Run(context.Background(), tmpl, inst); err == nil {
		t.Fatal("Run expected error for <2 choices, got nil")
	}
}

func TestRunScore(t *testing.T) {
	fg := &evaltest.FakeGenaiClient{}
	fg.PushJSON(`{"score": 8.5, "explanation": "Strong content with minimal flaws."}`, nil)
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithGenaiClient(fg))

	tmpl := registry.MetricTemplate{
		ID:                   "content/clarity-score",
		Kind:                 registry.KindScore,
		MetricPromptTemplate: "Score clarity from 1 to 10: {{essay}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"essay": {Modality: registry.ModalityText, Text: "A well-structured summary of the events."},
		},
	}

	res, err := eng.Run(context.Background(), tmpl, inst)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Score == nil || *res.Score != 8.5 {
		t.Errorf("Score = %v, want 8.5", res.Score)
	}
	if res.Explanation != "Strong content with minimal flaws." {
		t.Errorf("Explanation = %q, want %q", res.Explanation, "Strong content with minimal flaws.")
	}
}

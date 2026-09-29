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
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval/diffusion"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

type fakeDiffusionClient struct {
	resp      *diffusion.StructuredDecisionResponse
	stats     *diffusion.RequestStats
	err       error
	gotSchema string
	gotState  string
	gotImages []string
}

func (f *fakeDiffusionClient) Complete(_ context.Context, _ diffusion.ChatCompletionRequest) (*diffusion.ChatCompletionResponse, *diffusion.RequestStats, error) {
	return nil, nil, nil
}

func (f *fakeDiffusionClient) Decide(_ context.Context, schema, state string, images ...string) (*diffusion.StructuredDecisionResponse, *diffusion.RequestStats, error) {
	f.gotSchema = schema
	f.gotState = state
	f.gotImages = images
	if f.resp != nil && f.resp.ReadoutMode == "" {
		f.resp.ReadoutMode = diffusion.ReadoutEnvelope
	}
	return f.resp, f.stats, f.err
}

func TestRunDiffusionBoul(t *testing.T) {
	fd := &fakeDiffusionClient{
		resp: &diffusion.StructuredDecisionResponse{
			Answers: map[string]diffusion.QuestionAnswer{
				"verdict": {
					Type:       "boolean",
					Label:      "yes",
					Confidence: 0.982,
					Stderr:     0.0035,
					Agreement:  1.0,
				},
			},
		},
		stats: &diffusion.RequestStats{
			WallTime:  880 * time.Millisecond,
			DenoiseMs: 850.5,
			PrefillMs: 29.5,
		},
	}

	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))

	tmpl := registry.MetricTemplate{
		ID:                   "safety/brand-safe",
		Kind:                 registry.KindBoul,
		MetricPromptTemplate: "Is this text brand safe? {{text}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"text": {Modality: registry.ModalityText, Text: "Join us for our summer event."},
		},
	}

	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Passed == nil || !*res.Passed {
		t.Errorf("Passed = %v, want true", res.Passed)
	}
	if res.Score == nil || *res.Score != 1.0 {
		t.Errorf("Score = %v, want 1.0", res.Score)
	}
	if res.Confidence == nil || *res.Confidence != 0.982 {
		t.Errorf("Confidence = %v, want 0.982", res.Confidence)
	}
	if stderr, ok := res.CustomOutput["stderr"].(float64); !ok || stderr != 0.0035 {
		t.Errorf("CustomOutput[stderr] = %v, want 0.0035", res.CustomOutput["stderr"])
	}
	if denoise, ok := res.CustomOutput["denoise_ms"].(float64); !ok || denoise != 850.5 {
		t.Errorf("CustomOutput[denoise_ms] = %v, want 850.5", res.CustomOutput["denoise_ms"])
	}
}

func TestRunDiffusionChoice(t *testing.T) {
	fd := &fakeDiffusionClient{
		resp: &diffusion.StructuredDecisionResponse{
			Answers: map[string]diffusion.QuestionAnswer{
				"selection": {
					Type:       "choice",
					Choice:     "billing",
					Label:      "billing",
					Confidence: 0.995,
				},
			},
		},
		stats: &diffusion.RequestStats{
			WallTime: 870 * time.Millisecond,
		},
	}

	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))

	tmpl := registry.MetricTemplate{
		ID:                   "support/triage",
		Kind:                 registry.KindChoice,
		Choices:              []string{"billing", "technical", "sales"},
		MetricPromptTemplate: "Classify: {{ticket}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"ticket": {Modality: registry.ModalityText, Text: "I need an invoice copy."},
		},
	}

	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.ChoiceSelection != "billing" {
		t.Errorf("ChoiceSelection = %q, want 'billing'", res.ChoiceSelection)
	}
	if res.Confidence == nil || *res.Confidence != 0.995 {
		t.Errorf("Confidence = %v, want 0.995", res.Confidence)
	}
}

func TestRunDiffusionScore(t *testing.T) {
	fd := &fakeDiffusionClient{
		resp: &diffusion.StructuredDecisionResponse{
			Answers: map[string]diffusion.QuestionAnswer{
				"score": {
					Type:       "score",
					Score:      4.0,
					Confidence: 0.91,
				},
			},
		},
		stats: &diffusion.RequestStats{
			WallTime: 890 * time.Millisecond,
		},
	}

	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))

	tmpl := registry.MetricTemplate{
		ID:                   "quality/rating",
		Kind:                 registry.KindScore,
		MetricPromptTemplate: "Rate quality: {{response}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"response": {Modality: registry.ModalityText, Text: "Good reply."},
		},
	}

	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Score == nil || *res.Score != 4.0 {
		t.Errorf("Score = %v, want 4.0", res.Score)
	}
}

func TestRunDiffusionMultimodalImage(t *testing.T) {
	fd := &fakeDiffusionClient{
		resp: &diffusion.StructuredDecisionResponse{
			Answers: map[string]diffusion.QuestionAnswer{
				"verdict": {
					Type:       "boolean",
					Label:      "yes",
					Confidence: 0.97,
				},
			},
		},
	}

	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))

	tmpl := registry.MetricTemplate{
		ID:                   "brand/logo-check",
		Kind:                 registry.KindBoul,
		MetricPromptTemplate: "Is logo visible in image? {{ad_image}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"ad_image": {Modality: registry.ModalityImage, FilePath: "/tmp/sample_logo.png"},
		},
	}

	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fd.gotImages) != 1 || fd.gotImages[0] != "/tmp/sample_logo.png" {
		t.Errorf("gotImages = %v, want [/tmp/sample_logo.png]", fd.gotImages)
	}
	if res.Passed == nil || !*res.Passed {
		t.Errorf("Passed = %v, want true", res.Passed)
	}
}

func TestRunDiffusionRubric(t *testing.T) {
	fd := &fakeDiffusionClient{
		resp: &diffusion.StructuredDecisionResponse{
			Answers: map[string]diffusion.QuestionAnswer{
				"brand_1": {
					Type:       "boolean",
					Label:      "yes",
					Confidence: 0.99,
				},
				"brand_2": {
					Type:       "boolean",
					Label:      "no",
					Confidence: 0.85,
				},
			},
		},
	}

	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "proj-123", "us-central1", WithDiffusionClient(fd))

	tmpl := registry.MetricTemplate{
		ID:   "brand/scorecard",
		Kind: registry.KindRubric,
		RubricGroups: map[string][]string{
			"brand": {"Logo visible", "Brand colors"},
		},
		MetricPromptTemplate: "Evaluate ad: {{copy}}",
	}

	inst := Instance{
		Fields: map[string]AssetRef{
			"copy": {Modality: registry.ModalityText, Text: "Sample ad copy"},
		},
	}

	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.RubricDetail {
		t.Error("RubricDetail = false, want true")
	}
	// 1 of 2 criteria passed -> midpoint of the default 1-5 scale.
	if res.Score == nil || *res.Score != 3.0 {
		t.Errorf("Score = %v, want 3.0", res.Score)
	}
	pc, ok := res.CustomOutput["per_criterion"].([]any)
	if !ok || len(pc) != 2 {
		t.Fatalf("per_criterion = %v, want 2 entries", res.CustomOutput["per_criterion"])
	}
}

func TestRunDiffusionRejectsFallbackByDefault(t *testing.T) {
	fd := &fakeDiffusionClient{resp: &diffusion.StructuredDecisionResponse{
		ReadoutMode: diffusion.ReadoutFallback,
		Answers:     map[string]diffusion.QuestionAnswer{"verdict": {Label: "yes", Confidence: 1}},
	}}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(fd))
	tmpl := registry.MetricTemplate{ID: "a/b", Kind: registry.KindBoul, MetricPromptTemplate: "Toxic? {{t}}"}
	inst := Instance{Fields: map[string]AssetRef{"t": {Modality: registry.ModalityText, Text: "hi"}}}

	if _, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion")); !errors.Is(err, ErrDiffusionFallback) {
		t.Fatalf("err = %v, want ErrDiffusionFallback", err)
	}
	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"), WithDiffusionFallback(true))
	if err != nil {
		t.Fatalf("with fallback allowed: %v", err)
	}
	if res.CustomOutput["readout_mode"] != diffusion.ReadoutFallback {
		t.Errorf("readout_mode = %v, want fallback", res.CustomOutput["readout_mode"])
	}
}

func TestRunDiffusionScoreUsesTemplateScale(t *testing.T) {
	fd := &fakeDiffusionClient{resp: &diffusion.StructuredDecisionResponse{
		Answers: map[string]diffusion.QuestionAnswer{"score": {Score: 7.5, Level: "7", Confidence: 0.6}},
	}}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(fd))
	tmpl := registry.MetricTemplate{
		ID: "q/clarity", Kind: registry.KindScore, MetricPromptTemplate: "Rate clarity 0-9: {{r}}",
		RubricDetail: &registry.RubricDetail{Scale: &registry.RubricScale{Min: 0, Max: 9}},
	}
	inst := Instance{Fields: map[string]AssetRef{"r": {Modality: registry.ModalityText, Text: "x"}}}
	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"), WithDiffusionSamples(4))
	if err != nil {
		t.Fatal(err)
	}
	var schema diffusion.DecisionSchemaPayload
	if err := json.Unmarshal([]byte(fd.gotSchema), &schema); err != nil {
		t.Fatal(err)
	}
	if got := schema.Questions[0].Levels; len(got) != 10 || got[0] != "0" || got[9] != "9" {
		t.Errorf("levels = %v, want 0..9", got)
	}
	if schema.Samples != 4 {
		t.Errorf("samples = %d, want 4", schema.Samples)
	}
	// Server score 7.5 is a 1-based level index; on a 0-9 scale that is 6.5.
	if res.Score == nil || *res.Score != 6.5 {
		t.Errorf("Score = %v, want 6.5", res.Score)
	}
}

type seqDiffusionClient struct {
	resps  []*diffusion.StructuredDecisionResponse
	states []string
}

func (s *seqDiffusionClient) Complete(context.Context, diffusion.ChatCompletionRequest) (*diffusion.ChatCompletionResponse, *diffusion.RequestStats, error) {
	return nil, nil, nil
}

func (s *seqDiffusionClient) Decide(_ context.Context, _, state string, _ ...string) (*diffusion.StructuredDecisionResponse, *diffusion.RequestStats, error) {
	r := s.resps[len(s.states)]
	s.states = append(s.states, state)
	r.ReadoutMode = diffusion.ReadoutEnvelope
	return r, &diffusion.RequestStats{}, nil
}

func TestRunDiffusionPairwiseMirror(t *testing.T) {
	// Forward read prefers position 1 (BASELINE); the swapped read also prefers
	// position 1, which is now the original candidate: a pure position bias.
	fwd := map[string]float64{"BASELINE": 0.7, "CANDIDATE": 0.2, "TIE": 0.1}
	rev := map[string]float64{"BASELINE": 0.6, "CANDIDATE": 0.3, "TIE": 0.1}
	sc := &seqDiffusionClient{resps: []*diffusion.StructuredDecisionResponse{
		{Answers: map[string]diffusion.QuestionAnswer{"preference": {Probabilities: fwd}}},
		{Answers: map[string]diffusion.QuestionAnswer{"preference": {Probabilities: rev}}},
	}}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(sc))
	tmpl := registry.MetricTemplate{
		ID: "pw/helpful", Kind: registry.KindPairwise, BaselineFieldName: "baseline", CandidateFieldName: "response",
		MetricPromptTemplate: "Which is more helpful? A: {{baseline}} B: {{response}}",
	}
	inst := Instance{Fields: map[string]AssetRef{
		"baseline": {Modality: registry.ModalityText, Text: "AAA"},
		"response": {Modality: registry.ModalityText, Text: "BBB"},
	}}
	res, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion"), WithDiffusionMirror(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.states) != 2 || !strings.Contains(sc.states[1], `"baseline":"BBB"`) {
		t.Fatalf("mirror state not swapped: %v", sc.states)
	}
	if res.CustomOutput["position_consistent"] != false {
		t.Errorf("position_consistent = %v, want false", res.CustomOutput["position_consistent"])
	}
	// Averaged: BASELINE (0.7+0.3)/2 = 0.5, CANDIDATE (0.2+0.6)/2 = 0.4.
	if res.PairwiseChoice != "BASELINE" {
		t.Errorf("PairwiseChoice = %q, want BASELINE", res.PairwiseChoice)
	}
	if res.Confidence == nil || math.Abs(float64(*res.Confidence)-0.5) > 1e-6 {
		t.Errorf("Confidence = %v, want 0.5", res.Confidence)
	}
}

func TestDiffusionStateSendsFieldsOnceInTemplateOrder(t *testing.T) {
	fd := &fakeDiffusionClient{resp: &diffusion.StructuredDecisionResponse{
		Answers: map[string]diffusion.QuestionAnswer{"verdict": {Label: "no", Confidence: 0.9}},
	}}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(fd))
	tmpl := registry.MetricTemplate{ID: "a/b", Kind: registry.KindBoul, MetricPromptTemplate: "Is {{response}} harmful given {{prompt}}? Again: {{response}}"}
	inst := Instance{Fields: map[string]AssetRef{
		"prompt":   {Modality: registry.ModalityText, Text: "P-TEXT"},
		"response": {Modality: registry.ModalityText, Text: "R-TEXT"},
	}}
	if _, err := eng.Run(context.Background(), tmpl, inst, WithEngine("diffusion")); err != nil {
		t.Fatal(err)
	}
	if fd.gotState != `{"response":"R-TEXT","prompt":"P-TEXT"}` {
		t.Errorf("state = %s", fd.gotState)
	}
	if strings.Contains(fd.gotSchema, "R-TEXT") || !strings.Contains(fd.gotSchema, "Is [response] harmful given [prompt]?") {
		t.Errorf("schema should reference fields, not embed values: %s", fd.gotSchema)
	}
}

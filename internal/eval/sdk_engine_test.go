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
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

type stubGenai struct {
	resp        *genai.GenerateContentResponse
	err         error
	gotModel    string
	gotContents []*genai.Content
	gotCfg      *genai.GenerateContentConfig
}

func (s *stubGenai) GenerateContent(_ context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	s.gotModel = model
	s.gotContents = contents
	s.gotCfg = cfg
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func TestBuildGenaiParts_MediaPreambleFirstAndDedup(t *testing.T) {
	sg := &stubGenai{
		resp: &genai.GenerateContentResponse{
			ResponseID:   "resp-1",
			ModelVersion: "gemini-4-argon-001",
			Candidates: []*genai.Candidate{{
				FinishReason: genai.FinishReasonStop,
				Content:      &genai.Content{Parts: []*genai.Part{{Text: `{"selection":"cut_b","explanation":"tighter pacing"}`}}},
			}},
		},
	}
	eng := NewEngine(nil, "", "",
		WithGenaiClient(sg),
		WithModelOverride("models/gemini-4-argon"),
		WithBackend("enterprise"),
	)

	tmpl := registry.MetricTemplate{
		ID:                   "video-gen/best-variation",
		Kind:                 registry.KindChoice,
		AutoraterModel:       "gemini-3.5-flash-lite",
		Choices:              []string{"a", "b"},
		MetricPromptTemplate: "Given the creative brief: {{brief}}\nCompare {{variations}} and state which of {{variations}} best matches the brief.",
	}
	fps := 2.0
	inst := Instance{
		Fields: map[string]AssetRef{
			"brief": {Modality: registry.ModalityText, Text: "Energetic 6-second product reveal"},
			"variations": {
				Modality: registry.ModalityVideo,
				Items: []AssetRef{
					{
						Label:       "cut_a",
						Description: "Fast opening hook",
						URI:         "gs://studio-assets/cut_a.mp4",
						MimeType:    "video/mp4",
						VideoMeta:   &genai.VideoMetadata{StartOffset: 1 * time.Second, EndOffset: 5 * time.Second, FPS: &fps},
					},
					{
						Label:    "cut_b",
						URI:      "https://cdn.example.com/renders/cut_b.mp4?token=abc",
						MimeType: "video/mp4",
					},
				},
			},
		},
	}

	res, err := eng.Run(t.Context(), tmpl, inst,
		WithChoicesFromVariations("none"),
		WithMediaResolution("low"),
		WithMaxOutputTokens(512),
	)
	if err != nil {
		t.Fatalf("Engine.Run() error = %v", err)
	}
	if sg.gotModel != "gemini-4-argon" {
		t.Errorf("GenerateContent model = %q, want %q", sg.gotModel, "gemini-4-argon")
	}
	if sg.gotCfg.MediaResolution != genai.MediaResolutionLow {
		t.Errorf("cfg.MediaResolution = %q, want %q", sg.gotCfg.MediaResolution, genai.MediaResolutionLow)
	}
	if sg.gotCfg.MaxOutputTokens != 512 {
		t.Errorf("cfg.MaxOutputTokens = %d, want 512", sg.gotCfg.MaxOutputTokens)
	}

	// Verify Media-Preamble-First ordering & deduplication of repeated {{variations}}:
	// [LabelA, MediaA, LabelB, MediaB, FinalText] -> 5 parts total.
	parts := sg.gotContents[0].Parts
	if len(parts) != 5 {
		t.Fatalf("len(parts) = %d, want 5 (deduplicated media preamble + final text)", len(parts))
	}
	if parts[0].Text != `[video "cut_a" — Fast opening hook]:` {
		t.Errorf("parts[0].Text = %q, want label header for cut_a", parts[0].Text)
	}
	if parts[1].FileData == nil || parts[1].FileData.FileURI != "gs://studio-assets/cut_a.mp4" {
		t.Errorf("parts[1].FileData = %+v, want gs://studio-assets/cut_a.mp4", parts[1].FileData)
	}
	if parts[1].VideoMetadata == nil || parts[1].VideoMetadata.StartOffset != time.Second || parts[1].VideoMetadata.EndOffset != 5*time.Second {
		t.Errorf("parts[1].VideoMetadata = %+v, want [1s, 5s]", parts[1].VideoMetadata)
	}
	if parts[2].Text != `[video "cut_b"]:` {
		t.Errorf("parts[2].Text = %q, want label header for cut_b", parts[2].Text)
	}
	if parts[3].FileData == nil || parts[3].FileData.FileURI != "https://cdn.example.com/renders/cut_b.mp4?token=abc" {
		t.Errorf("parts[3].FileData = %+v, want https URI", parts[3].FileData)
	}
	finalPrompt := parts[4].Text
	if !strings.Contains(finalPrompt, "Energetic 6-second product reveal") {
		t.Errorf("finalPrompt = %q, missing brief text", finalPrompt)
	}
	if strings.Count(finalPrompt, `[attached video variations: "cut_a", "cut_b"]`) != 2 {
		t.Errorf("finalPrompt = %q, want 2 inline markers for repeated {{variations}}", finalPrompt)
	}
	if !strings.Contains(finalPrompt, "choices: [cut_a, cut_b, none]") {
		t.Errorf("finalPrompt = %q, missing effective choices directive", finalPrompt)
	}

	if res.ChoiceSelection != "cut_b" {
		t.Errorf("ChoiceSelection = %q, want %q", res.ChoiceSelection, "cut_b")
	}
	wantChoices := []string{"cut_a", "cut_b", "none"}
	if res.Applied == nil || !slices.Equal(res.Applied.EffectiveChoices, wantChoices) {
		t.Errorf("Applied = %+v, want EffectiveChoices=%v", res.Applied, wantChoices)
	}
	if res.Applied.Backend != "enterprise" || res.Applied.ModelSource != modelSourceEngineOverride || res.Applied.ModelVersion != "gemini-4-argon-001" {
		t.Errorf("Applied provenance = %+v, want Backend=enterprise ModelSource=engine-override ModelVersion=gemini-4-argon-001", res.Applied)
	}
}

func TestChoiceValidationAndOutOfEnumGuard(t *testing.T) {
	tmpl := registry.MetricTemplate{
		ID:                   "video-gen/pick",
		Kind:                 registry.KindChoice,
		Choices:              []string{"a", "b"},
		MetricPromptTemplate: "Pick one of {{variations}}",
	}

	t.Run("unoverridden_variation_mismatch_fails_fast", func(t *testing.T) {
		sg := &stubGenai{}
		eng := NewEngine(nil, "", "", WithGenaiClient(sg))
		inst := Instance{Fields: map[string]AssetRef{
			"variations": {Items: []AssetRef{
				{Label: "cut_1", URI: "gs://b/1.mp4", MimeType: "video/mp4"},
				{Label: "cut_2", URI: "gs://b/2.mp4", MimeType: "video/mp4"},
			}},
		}}
		if _, err := eng.Run(t.Context(), tmpl, inst); err == nil || !strings.Contains(err.Error(), "WithChoicesFromVariations") {
			t.Errorf("Run(unoverridden variations) error = %v, want mismatch error naming WithChoicesFromVariations", err)
		}
	})

	t.Run("out_of_enum_model_reply_rejected", func(t *testing.T) {
		sg := &stubGenai{
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					FinishReason: genai.FinishReasonStop,
					Content:      &genai.Content{Parts: []*genai.Part{{Text: `{"selection":"hallucinated","explanation":"bad"}`}}},
				}},
			},
		}
		eng := NewEngine(nil, "", "", WithGenaiClient(sg))
		inst := Instance{Fields: map[string]AssetRef{
			"variations": {Modality: registry.ModalityText, Text: "A vs B"},
		}}
		if _, err := eng.Run(t.Context(), tmpl, inst, WithChoices([]string{"x", "y"})); err == nil || !strings.Contains(err.Error(), "outside allowed choices") {
			t.Errorf("Run(out-of-enum reply) error = %v, want outside allowed choices error", err)
		}
	})

	t.Run("control_char_in_choice_rejected", func(t *testing.T) {
		sg := &stubGenai{}
		eng := NewEngine(nil, "", "", WithGenaiClient(sg))
		inst := Instance{Fields: map[string]AssetRef{
			"variations": {Modality: registry.ModalityText, Text: "A vs B"},
		}}
		if _, err := eng.Run(t.Context(), tmpl, inst, WithChoices([]string{"good", "bad\ninject"})); err == nil || !strings.Contains(err.Error(), "control characters") {
			t.Errorf("Run(control char choice) error = %v, want control characters error", err)
		}
	})
}

func TestAggregateInlineCap20MiB(t *testing.T) {
	sg := &stubGenai{}
	eng := NewEngine(nil, "", "", WithGenaiClient(sg))
	tmpl := registry.MetricTemplate{
		ID:                   "mm/compare",
		Kind:                 registry.KindBoul,
		MetricPromptTemplate: "Compare {{vars}}",
	}
	elevenMiB := make([]byte, 11<<20)
	copy(elevenMiB, jpegMagic)

	inst := Instance{Fields: map[string]AssetRef{
		"vars": {Items: []AssetRef{
			{Label: "v1", Bytes: elevenMiB, MimeType: "image/jpeg"},
			{Label: "v2", Bytes: elevenMiB, MimeType: "image/jpeg"},
		}},
	}}
	_, err := eng.Run(t.Context(), tmpl, inst)
	if err == nil || !strings.Contains(err.Error(), "total inline media payload") {
		t.Errorf("Run(2x11MiB) error = %v, want aggregate 20 MiB cap error", err)
	}
}

func TestValidateMediaURI_SSRFAndAllowlists(t *testing.T) {
	blocked := []string{
		"http://example.com/v.mp4",
		"file:///etc/passwd",
		"https://localhost/v.mp4",
		"https://sub.localhost/v.mp4",
		"https://127.0.0.1/v.mp4",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/v.mp4",
		"https://[::ffff:127.0.0.1]/v.mp4",
		"https://10.0.0.5/v.mp4",
		"https://100.64.1.2/v.mp4",
		"https://user:pass@cdn.example.com/v.mp4",
		"gs://bucket-only",
	}
	for _, u := range blocked {
		if err := ValidateMediaURI(u, nil, nil); err == nil {
			t.Errorf("ValidateMediaURI(%q, nil, nil) = nil, want SSRF/scheme rejection", u)
		}
	}

	if err := ValidateMediaURI("https://cdn.example.com/v.mp4", []string{"other.example.com"}, nil); err == nil {
		t.Error("ValidateMediaURI(unlisted host) = nil, want host allowlist rejection")
	}
	if err := ValidateMediaURI("https://CDN.EXAMPLE.COM/v.mp4", []string{"cdn.example.com"}, nil); err != nil {
		t.Errorf("ValidateMediaURI(allowed host case-insensitive) = %v, want nil", err)
	}
	if err := ValidateMediaURI("gs://other-bkt/v.mp4", nil, []string{"allowed-bkt"}); err == nil {
		t.Error("ValidateMediaURI(unlisted bucket) = nil, want bucket allowlist rejection")
	}
	if err := ValidateMediaURI("gs://allowed-bkt/v.mp4", nil, []string{"allowed-bkt"}); err != nil {
		t.Errorf("ValidateMediaURI(allowed bucket) = %v, want nil", err)
	}
}

func TestLocalFileDisabledAndVideoMetadataBounds(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "img.jpg")
	if err := os.WriteFile(fp, jpegMagic, 0o600); err != nil {
		t.Fatal(err)
	}

	sg := &stubGenai{}
	eng := NewEngine(nil, "", "", WithGenaiClient(sg), WithAllowLocalFiles(false))
	tmpl := registry.MetricTemplate{
		ID:                   "mm/check",
		Kind:                 registry.KindBoul,
		MetricPromptTemplate: "Check {{clip}}",
	}
	if _, err := eng.Run(t.Context(), tmpl, Instance{Fields: map[string]AssetRef{
		"clip": {FilePath: fp},
	}}); err == nil || !strings.Contains(err.Error(), "WithLocalFileAccess(true)") {
		t.Errorf("Run(local file disabled) error = %v, want WithLocalFileAccess(true) hint", err)
	}

	// Sub-second window [10.2s, 10.4s] rounds both endpoints to "10s" and must fail fast.
	if err := ValidateVideoMetadata(&genai.VideoMetadata{
		StartOffset: 10200 * time.Millisecond,
		EndOffset:   10400 * time.Millisecond,
	}); err == nil || !strings.Contains(err.Error(), "collapses to zero whole seconds") {
		t.Errorf("ValidateVideoMetadata(10.2s..10.4s) = %v, want whole-second collapse error", err)
	}
	badFPS := 30.0
	if err := ValidateVideoMetadata(&genai.VideoMetadata{FPS: &badFPS}); err == nil {
		t.Error("ValidateVideoMetadata(FPS=30) = nil, want (0, 24] bounds error")
	}
}

func TestFinishErrorSurfacedBeforeJSONParse(t *testing.T) {
	sg := &stubGenai{
		resp: &genai.GenerateContentResponse{
			ResponseID:   "resp-safety",
			ModelVersion: "gemini-4-argon-001",
			Candidates: []*genai.Candidate{{
				FinishReason:  genai.FinishReasonSafety,
				FinishMessage: "blocked by safety filter",
			}},
		},
	}
	eng := NewEngine(nil, "", "", WithGenaiClient(sg))
	tmpl := registry.MetricTemplate{
		ID:                   "safety/check",
		Kind:                 registry.KindBoul,
		MetricPromptTemplate: "Check {{text}}",
	}
	_, err := eng.Run(t.Context(), tmpl, Instance{Fields: map[string]AssetRef{
		"text": {Modality: registry.ModalityText, Text: "probe"},
	}})
	var finErr *FinishError
	if !errors.As(err, &finErr) {
		t.Fatalf("Run() error = %v, want *FinishError", err)
	}
	if finErr.FinishReason != string(genai.FinishReasonSafety) || finErr.ResponseID != "resp-safety" {
		t.Errorf("FinishError = %+v, want FinishReason=SAFETY ResponseID=resp-safety", finErr)
	}
}

func TestGenaiOnlyLegacyKindAutoRoutingAndPrebuiltRejection(t *testing.T) {
	t.Run("pointwise_auto_routes_to_score", func(t *testing.T) {
		sg := &stubGenai{
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					FinishReason: genai.FinishReasonStop,
					Content:      &genai.Content{Parts: []*genai.Part{{Text: `{"score":4,"explanation":"clear"}`}}},
				}},
			},
		}
		eng := NewEngine(nil, "", "", WithGenaiClient(sg), WithBackend("enterprise"))
		tmpl := registry.MetricTemplate{
			ID:                   "legacy/pw",
			Kind:                 registry.KindPointwise,
			MetricPromptTemplate: "Rate {{response}}",
		}
		res, err := eng.Run(t.Context(), tmpl, Instance{Fields: map[string]AssetRef{"response": {Text: "ok"}}})
		if err != nil {
			t.Fatalf("Run(pointwise) error = %v", err)
		}
		if res.Score == nil || *res.Score != 4 {
			t.Errorf("Score = %v, want 4", res.Score)
		}
		if res.Applied == nil || res.Applied.EffectiveHost != "genai" {
			t.Errorf("Applied = %+v, want EffectiveHost=genai", res.Applied)
		}
	})

	t.Run("rubric_auto_routes_and_populates_explanation", func(t *testing.T) {
		sg := &stubGenai{
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					FinishReason: genai.FinishReasonStop,
					Content: &genai.Content{Parts: []*genai.Part{{Text: `{
						"per_criterion": [{"group":"quality","criterion":"Sharp focus","score":5,"rationale":"crisp"}],
						"overall_score": 5,
						"explanation": "Sharp across frames"
					}`}}},
				}},
			},
		}
		eng := NewEngine(nil, "", "", WithGenaiClient(sg))
		tmpl := registry.MetricTemplate{
			ID:                   "legacy/rubric",
			Kind:                 registry.KindRubric,
			MetricPromptTemplate: "Evaluate {{response}}",
			RubricGroups:         map[string][]string{"quality": {"Sharp focus"}},
		}
		res, err := eng.Run(t.Context(), tmpl, Instance{Fields: map[string]AssetRef{"response": {Text: "ok"}}})
		if err != nil {
			t.Fatalf("Run(rubric) error = %v", err)
		}
		if !res.RubricDetail || res.Explanation != "Sharp across frames" {
			t.Errorf("Result = %+v, want RubricDetail=true and Explanation populated", res)
		}
	})

	t.Run("pairwise_auto_routes_to_genai", func(t *testing.T) {
		sg := &stubGenai{
			resp: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					FinishReason: genai.FinishReasonStop,
					Content:      &genai.Content{Parts: []*genai.Part{{Text: `{"selection":"CANDIDATE","explanation":"better"}`}}},
				}},
			},
		}
		eng := NewEngine(nil, "", "", WithGenaiClient(sg))
		tmpl := registry.MetricTemplate{
			ID:                   "legacy/pair",
			Kind:                 registry.KindPairwise,
			BaselineFieldName:    "base",
			CandidateFieldName:   "cand",
			MetricPromptTemplate: "Compare {{base}} vs {{cand}}",
		}
		res, err := eng.Run(t.Context(), tmpl, Instance{Fields: map[string]AssetRef{
			"base": {Text: "a"},
			"cand": {Text: "b"},
		}})
		if err != nil {
			t.Fatalf("Run(pairwise) error = %v", err)
		}
		if res.PairwiseChoice != "CANDIDATE" {
			t.Errorf("PairwiseChoice = %q, want CANDIDATE", res.PairwiseChoice)
		}
	})

	t.Run("prebuilt_fails_fast_with_actionable_error", func(t *testing.T) {
		sg := &stubGenai{}
		eng := NewEngine(nil, "", "", WithGenaiClient(sg), WithBackend("enterprise"))
		tmpl := registry.MetricTemplate{
			ID:     "vertex/coherence",
			Kind:   registry.KindPrebuilt,
			Native: &registry.NativeMetricSpec{Metric: "coherence"},
		}
		_, err := eng.Run(t.Context(), tmpl, Instance{Fields: map[string]AssetRef{"response": {Text: "ok"}}})
		if err == nil || !strings.Contains(err.Error(), "EvaluateInstances") || !strings.Contains(err.Error(), "enterprise") {
			t.Errorf("Run(prebuilt on genai-only) error = %v, want EvaluateInstances + enterprise message", err)
		}
	})
}

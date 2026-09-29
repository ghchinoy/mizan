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
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/eval/diffusion"
	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// prebuiltTmpl builds a kind:prebuilt template declaring an input per name.
func prebuiltTmpl(spec registry.NativeMetricSpec, model string, inputs ...string) registry.MetricTemplate {
	t := registry.MetricTemplate{
		ID:             "prebuilt/" + strings.ReplaceAll(spec.Metric, "_", "-"),
		Kind:           registry.KindPrebuilt,
		Native:         &spec,
		AutoraterModel: model,
	}
	for _, in := range inputs {
		t.Inputs = append(t.Inputs, registry.InputSpec{Name: in, Modality: registry.ModalityText, Required: true})
	}
	return t
}

// TestPrebuiltGroundednessVertex: the GroundednessInput carries prediction +
// context from the mapped fields, the AutoraterConfig carries the expanded model
// and sampling count, and the result maps Score/Confidence/Explanation/Passed.
func TestPrebuiltGroundednessVertex(t *testing.T) {
	thr := 1.0
	fc := &evaltest.FakeEvaluationClient{Resp: &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_GroundednessResult{GroundednessResult: &aiplatformpb.GroundednessResult{
			Score: proto.Float32(1), Confidence: proto.Float32(0.9), Explanation: "all claims supported"}}}}
	eng := NewEngine(fc, "proj-1", "us-central1")
	tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "groundedness", ResponseField: "answer", ContextField: "doc", PassThreshold: &thr},
		"gemini-2.5-flash", "answer", "doc")
	tmpl.SamplingCount = 3

	res, err := eng.Run(context.Background(), tmpl, textInst("answer", "Paris is in France.", "doc", "Paris is the capital of France."))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	req := fc.LastRequest()
	in := req.GetGroundednessInput()
	if in == nil || in.Instance.GetPrediction() != "Paris is in France." || in.Instance.GetContext() != "Paris is the capital of France." {
		t.Fatalf("GroundednessInput = %v", in)
	}
	// Predefined metrics reject AutoraterConfig (live API, 2026-09-26).
	if ac := req.GetAutoraterConfig(); ac != nil {
		t.Errorf("AutoraterConfig = %v, want nil for a predefined metric", ac)
	}
	if res.Score == nil || *res.Score != 1 || res.Confidence == nil || *res.Confidence != 0.9 || res.Explanation != "all claims supported" {
		t.Errorf("result = score %v conf %v expl %q", res.Score, res.Confidence, res.Explanation)
	}
	if res.Passed == nil || !*res.Passed {
		t.Errorf("Passed = %v, want true (score 1 >= threshold 1)", res.Passed)
	}
	if res.Applied == nil || res.Applied.Model != PrebuiltServiceJudge {
		t.Errorf("Applied = %#v, want the service-default judge", res.Applied)
	}
}

// TestPrebuiltSummarizationUsesReferenceAndPromptAlias: an explicit
// referenceField sets UseReference, and promptField feeds the instruction slot.
func TestPrebuiltSummarizationUsesReferenceAndPromptAlias(t *testing.T) {
	fc := &evaltest.FakeEvaluationClient{Resp: &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_SummarizationQualityResult{
			SummarizationQualityResult: &aiplatformpb.SummarizationQualityResult{Score: proto.Float32(4)}}}}
	eng := NewEngine(fc, "proj-1", "us-central1")
	tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "summarization_quality", PromptField: "prompt", ContextField: "article", ReferenceField: "gold"},
		"gemini-2.5-flash", "response", "prompt", "article", "gold")
	res, err := eng.Run(context.Background(), tmpl, textInst("response", "S", "prompt", "Summarize.", "article", "A", "gold", "G"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	in := fc.LastRequest().GetSummarizationQualityInput()
	if in == nil || !in.MetricSpec.GetUseReference() {
		t.Fatalf("SummarizationQualityInput spec = %v, want UseReference", in.GetMetricSpec())
	}
	got := in.Instance
	if got.GetPrediction() != "S" || got.GetInstruction() != "Summarize." || got.GetContext() != "A" || got.GetReference() != "G" {
		t.Errorf("instance = %v", got)
	}
	if res.Score == nil || *res.Score != 4 || res.Passed != nil {
		t.Errorf("Score = %v Passed = %v, want 4 / nil", res.Score, res.Passed)
	}
}

// TestPrebuiltPairwiseVertexRegionalNoAutorater: a pairwise_* metric sends
// baseline_prediction, maps the PairwiseChoice, and — because predefined metrics
// take no judge model — goes to the REGIONAL client with no AutoraterConfig even
// when the template names a global-only judge and enables flip.
func TestPrebuiltPairwiseVertexRegionalNoAutorater(t *testing.T) {
	regional := &evaltest.FakeEvaluationClient{Resp: &aiplatformpb.EvaluateInstancesResponse{
		EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PairwiseQuestionAnsweringQualityResult{
			PairwiseQuestionAnsweringQualityResult: &aiplatformpb.PairwiseQuestionAnsweringQualityResult{
				PairwiseChoice: aiplatformpb.PairwiseChoice_CANDIDATE, Explanation: "B is better", Confidence: proto.Float32(0.8)}}}}
	global := &evaltest.FakeEvaluationClient{}
	eng := NewEngine(regional, "proj-1", "us-central1", WithGlobalClient(global), WithNoticeWriter(&bytes.Buffer{}))
	tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "pairwise_question_answering_quality", InstructionField: "q", ContextField: "ctx", BaselineField: "base"},
		"gemini-3.5-flash-lite", "response", "q", "ctx", "base")
	tmpl.FlipEnabled = true

	res, err := eng.Run(context.Background(), tmpl, textInst("response", "cand", "q", "Q?", "ctx", "C", "base", "old"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if regional.Calls() != 1 || global.Calls() != 0 {
		t.Fatalf("calls regional=%d global=%d, want 1/0", regional.Calls(), global.Calls())
	}
	req := regional.LastRequest()
	if req.GetAutoraterConfig() != nil {
		t.Errorf("AutoraterConfig = %v, want nil", req.GetAutoraterConfig())
	}
	in := req.GetPairwiseQuestionAnsweringQualityInput()
	if in == nil || in.Instance.GetPrediction() != "cand" || in.Instance.GetBaselinePrediction() != "old" || in.Instance.GetInstruction() != "Q?" || in.Instance.GetContext() != "C" {
		t.Fatalf("pairwise instance = %v", in.GetInstance())
	}
	if res.PairwiseChoice != "CANDIDATE" || res.Confidence == nil || *res.Confidence != 0.8 || res.Score != nil {
		t.Errorf("result = choice %q conf %v score %v", res.PairwiseChoice, res.Confidence, res.Score)
	}
}

// TestPrebuiltEveryMetricBuilds guards against a metric missing from the input
// builder or the result extractor.
func TestPrebuiltEveryMetricBuilds(t *testing.T) {
	f := map[string]string{"response": "r", "reference": "g", "context": "c", "instruction": "i", "baseline": "b"}
	for _, m := range registry.NativeMetricIDs(registry.KindPrebuilt) {
		req := &aiplatformpb.EvaluateInstancesRequest{}
		if err := setPrebuiltInput(req, m, f); err != nil || req.MetricInputs == nil {
			t.Errorf("%s: input=%v err=%v", m, req.MetricInputs, err)
		}
		if _, ok := prebuiltDiffusionDefs[m]; !ok {
			t.Errorf("%s: no diffusion mapping", m)
		}
	}
}

// TestPrebuiltMissingField: a required role's input must be supplied.
func TestPrebuiltMissingField(t *testing.T) {
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1")
	tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "groundedness", ContextField: "ctx"}, "gemini-2.5-flash", "response", "ctx")
	if _, err := eng.Run(context.Background(), tmpl, textInst("response", "x")); err == nil || !strings.Contains(err.Error(), "ctx") {
		t.Errorf("err = %v, want missing ctx", err)
	}
}

// --- DiffusionGemma mapping -------------------------------------------------

func diffusionSchemaOf(t *testing.T, fd *fakeDiffusionClient) diffusion.DecisionSchemaPayload {
	t.Helper()
	var p diffusion.DecisionSchemaPayload
	if err := json.Unmarshal([]byte(fd.gotSchema), &p); err != nil {
		t.Fatalf("schema JSON: %v (%s)", err, fd.gotSchema)
	}
	return p
}

// TestPrebuiltDiffusionFluencyScore: a 1-5 Likert metric reads on levels 1..5,
// the state JSON carries the mapped response, and the expected level maps onto
// the Vertex scale.
func TestPrebuiltDiffusionFluencyScore(t *testing.T) {
	thr := 4.0
	fd := &fakeDiffusionClient{resp: &diffusion.StructuredDecisionResponse{Answers: map[string]diffusion.QuestionAnswer{
		"score": {Type: "score", Score: 4.25, Level: "4", Confidence: 0.7}}}}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(fd))
	tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "fluency", ResponseField: "text", PassThreshold: &thr}, "gemini-2.5-flash", "text")

	res, err := eng.Run(context.Background(), tmpl, textInst("text", "The quick brown fox."), WithEngine("diffusion"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := diffusionSchemaOf(t, fd)
	if len(p.Questions) != 1 || p.Questions[0].Type != "score" || !reflect.DeepEqual(p.Questions[0].Levels, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("questions = %+v", p.Questions)
	}
	if !strings.Contains(p.Questions[0].Instructions, "5: Completely fluent") {
		t.Errorf("instructions lack level descriptions: %q", p.Questions[0].Instructions)
	}
	var state map[string]any
	if err := json.Unmarshal([]byte(fd.gotState), &state); err != nil || state["response"] != "The quick brown fox." {
		t.Errorf("state = %s (err %v)", fd.gotState, err)
	}
	if res.Score == nil || math.Abs(float64(*res.Score)-4.25) > 1e-6 {
		t.Errorf("Score = %v, want 4.25", res.Score)
	}
	if res.Passed == nil || !*res.Passed {
		t.Errorf("Passed = %v, want true (4.25 >= 4)", res.Passed)
	}
	if res.CustomOutput["metric"] != "fluency" || res.CustomOutput["engine"] != "diffusion" {
		t.Errorf("CustomOutput = %v", res.CustomOutput)
	}
}

// TestPrebuiltDiffusionVerbosityScale: summarization_verbosity reads on -2..2,
// so level index 3 (the middle) maps to 0.
func TestPrebuiltDiffusionVerbosityScale(t *testing.T) {
	fd := &fakeDiffusionClient{resp: &diffusion.StructuredDecisionResponse{Answers: map[string]diffusion.QuestionAnswer{
		"score": {Type: "score", Score: 3, Level: "0"}}}}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(fd))
	tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "summarization_verbosity", ContextField: "article", InstructionField: "task"},
		"", "response", "article", "task")
	res, err := eng.Run(context.Background(), tmpl, textInst("response", "S", "article", "A", "task", "Summarize"), WithEngine("diffusion"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := diffusionSchemaOf(t, fd)
	if !reflect.DeepEqual(p.Questions[0].Levels, []string{"-2", "-1", "0", "1", "2"}) {
		t.Errorf("levels = %v", p.Questions[0].Levels)
	}
	var state map[string]any
	_ = json.Unmarshal([]byte(fd.gotState), &state)
	if state["context"] != "A" || state["instruction"] != "Summarize" || state["response"] != "S" {
		t.Errorf("state = %v", state)
	}
	if res.Score == nil || *res.Score != 0 {
		t.Errorf("Score = %v, want 0", res.Score)
	}
}

// TestPrebuiltDiffusionSafetyBoolean: safety is a boolean "the response is safe"
// question mapped to 1/0.
func TestPrebuiltDiffusionSafetyBoolean(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  float32
	}{{"yes", 1}, {"no", 0}} {
		fd := &fakeDiffusionClient{resp: &diffusion.StructuredDecisionResponse{Answers: map[string]diffusion.QuestionAnswer{
			"verdict": {Type: "boolean", Label: tc.label, Confidence: 0.95}}}}
		eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(fd))
		tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "safety"}, "", "response")
		res, err := eng.Run(context.Background(), tmpl, textInst("response", "hello"), WithEngine("diffusion"))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if q := diffusionSchemaOf(t, fd).Questions[0]; q.Type != "boolean" || !strings.Contains(q.Instructions, "safe") {
			t.Errorf("question = %+v", q)
		}
		if res.Score == nil || *res.Score != tc.want {
			t.Errorf("label %s: Score = %v, want %v", tc.label, res.Score, tc.want)
		}
	}
}

// swapAwareDiffusion answers the forward and mirrored pairwise reads with
// different probabilities, keyed on whether "response" holds the candidate.
type swapAwareDiffusion struct {
	fakeDiffusionClient
	calls  int
	states []string
}

func (s *swapAwareDiffusion) Decide(_ context.Context, schema, state string, _ ...string) (*diffusion.StructuredDecisionResponse, *diffusion.RequestStats, error) {
	s.calls++
	s.gotSchema = schema
	s.states = append(s.states, state)
	probs := map[string]float64{pairBaseline: 0.2, pairCandidate: 0.7, pairTie: 0.1} // forward: candidate wins
	if strings.Contains(state, `"response":"old"`) {
		probs = map[string]float64{pairBaseline: 0.6, pairCandidate: 0.3, pairTie: 0.1} // mirrored: new text is now the baseline
	}
	return &diffusion.StructuredDecisionResponse{ReadoutMode: diffusion.ReadoutEnvelope, Answers: map[string]diffusion.QuestionAnswer{
		"preference": {Type: "choice", Probabilities: probs}}}, nil, nil
}

// TestPrebuiltDiffusionPairwiseMirror: pairwise_summarization_quality reuses the
// preference choice + finishPairwise; with mirror on, the response/baseline
// fields are swapped for the second read and the position-corrected average is
// CANDIDATE.
func TestPrebuiltDiffusionPairwiseMirror(t *testing.T) {
	fd := &swapAwareDiffusion{}
	eng := NewEngine(&evaltest.FakeEvaluationClient{}, "p", "us-central1", WithDiffusionClient(fd))
	tmpl := prebuiltTmpl(registry.NativeMetricSpec{Metric: "pairwise_summarization_quality", ContextField: "article", InstructionField: "task", BaselineField: "base"},
		"", "response", "article", "task", "base")
	res, err := eng.Run(context.Background(), tmpl, textInst("response", "new", "article", "A", "task", "T", "base", "old"),
		WithEngine("diffusion"), WithDiffusionMirror(true))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fd.calls != 2 {
		t.Fatalf("diffusion calls = %d, want 2 (forward + mirror)", fd.calls)
	}
	if !strings.Contains(fd.states[0], `"baseline_response":"old"`) || !strings.Contains(fd.states[1], `"baseline_response":"new"`) {
		t.Errorf("states = %v, want baseline/response swapped on the mirror read", fd.states)
	}
	var p diffusion.DecisionSchemaPayload
	_ = json.Unmarshal([]byte(fd.gotSchema), &p)
	if q := p.Questions[0]; q.ID != "preference" || len(q.Options) != 3 || q.Options[0].Name != pairBaseline {
		t.Errorf("question = %+v", q)
	}
	// Forward CANDIDATE 0.7; mirrored BASELINE 0.6 means original candidate 0.6 -> avg 0.65.
	if res.PairwiseChoice != pairCandidate || res.Confidence == nil || math.Abs(float64(*res.Confidence)-0.65) > 1e-6 {
		t.Errorf("choice %q conf %v, want CANDIDATE 0.65", res.PairwiseChoice, res.Confidence)
	}
	if res.CustomOutput["position_consistent"] != true {
		t.Errorf("position_consistent = %v, want true", res.CustomOutput["position_consistent"])
	}
}

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
	"math"
	"reflect"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/eval/evaltest"
	"github.com/ghchinoy/mizan/internal/registry"
)

// computationTmpl builds a kind:computation template with response/reference
// inputs for the given native spec.
func computationTmpl(spec registry.NativeMetricSpec) registry.MetricTemplate {
	inputs := []registry.InputSpec{{Name: "response", Modality: registry.ModalityText, Required: true}}
	if spec.Metric != "trajectory_single_tool_use" {
		inputs = append(inputs, registry.InputSpec{Name: "reference", Modality: registry.ModalityText, Required: true})
	}
	return registry.MetricTemplate{
		ID:     "computation/" + strings.ReplaceAll(spec.Metric, "_", "-"),
		Kind:   registry.KindComputation,
		Inputs: inputs,
		Native: &spec,
	}
}

func textInst(kv ...string) Instance {
	inst := Instance{Fields: map[string]AssetRef{}}
	for i := 0; i+1 < len(kv); i += 2 {
		inst.Fields[kv[i]] = AssetRef{Modality: registry.ModalityText, Text: kv[i+1]}
	}
	return inst
}

// Shared tool-call and trajectory fixtures.
const (
	tcWeatherParis = `{"content":"","tool_calls":[{"name":"get_weather","arguments":{"city":"Paris","unit":"C","days":3}}]}`
	tcWeatherMixed = `{"content":"","tool_calls":[{"name":"get_weather","arguments":{"city":"Paris","unit":"F","days":3.0,"lang":"fr"}}]}`
	tcTimeParis    = `{"content":"","tool_calls":[{"name":"get_time","arguments":{"city":"Paris"}}]}`
	tcNoArgs       = `{"content":"","tool_calls":[{"name":"get_weather"}]}`
	tcStringArgs   = `{"content":"","tool_calls":[{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}]}`
	tcNone         = `{"content":"It is sunny."}`

	trajAB      = `[{"tool_name":"A","tool_input":{"x":1}},{"tool_name":"B","tool_input":{"y":2,"z":[1,2]}}]`
	trajABkeys  = `[{"tool_name":"A","tool_input":"{\"x\": 1}"},{"tool_name":"B","tool_input":{"z":[1,2],"y":2}}]` // same calls, string input + reordered keys
	trajACB     = `[{"tool_name":"A","tool_input":{"x":1}},{"tool_name":"C","tool_input":{}},{"tool_name":"B","tool_input":{"y":2,"z":[1,2]}}]`
	trajBA      = `[{"tool_name":"B","tool_input":{"y":2,"z":[1,2]}},{"tool_name":"A","tool_input":{"x":1}}]`
	trajAwrong  = `[{"tool_name":"A","tool_input":{"x":2}}]`
	trajAA      = `[{"tool_name":"A","tool_input":{"x":1}},{"tool_name":"A","tool_input":{"x":1}}]`
	trajEmpty   = `[]`
	trajInvalid = `not a trajectory`
)

// TestComputationLocalMetrics is the table of hand-computed expected values for
// every computation metric on the LOCAL engine. BLEU values were cross-checked
// against sacrebleu 2.6.0 sentence_bleu (defaults: 13a, exp smoothing,
// use_effective_order=True); each is also derivable by hand as annotated.
func TestComputationLocalMetrics(t *testing.T) {
	cases := []struct {
		name string
		spec registry.NativeMetricSpec
		pred string
		ref  string
		want float64
	}{
		// --- exact_match ---
		{"exact/trimmed-equal", registry.NativeMetricSpec{Metric: "exact_match"}, "  Paris\n", "Paris", 1},
		{"exact/case-differs", registry.NativeMetricSpec{Metric: "exact_match"}, "paris", "Paris", 0},

		// --- bleu ---
		// Tokens: "The cat sat on the mat ." (7) vs "The cat is sitting on the mat ." (8).
		// matches [6,4,2,1] / totals [7,6,5,4]; BP = exp(1-8/7);
		// BLEU = BP * (6/7 * 4/6 * 2/5 * 1/4)^(1/4) = 0.42383656. sacrebleu: 42.38365628278778.
		{"bleu/cat-mat", registry.NativeMetricSpec{Metric: "bleu"}, "The cat sat on the mat.", "The cat is sitting on the mat.", 0.4238365628278778},
		// Papineni et al. (2002) Candidate 1 vs Reference 1 (single reference):
		// matches [12,8,6,4] / totals [19,18,17,16], BP = 1. sacrebleu: 39.67088290836576.
		{"bleu/papineni", registry.NativeMetricSpec{Metric: "bleu"},
			"It is a guide to action which ensures that the military always obeys the commands of the party.",
			"It is a guide to action that ensures that the military will forever heed Party commands.", 0.3967088290836576},
		{"bleu/identical", registry.NativeMetricSpec{Metric: "bleu"}, "The dog bit the man.", "The dog bit the man.", 1},
		// Effective order: the 3-token hypothesis has no 4-grams, so only orders 1..3
		// are averaged; the zero-match 3-gram is exp-smoothed to 1/(2*1).
		// BP = exp(1-6/3); BLEU = e^-1 * (2/3 * 1/2 * 1/2)^(1/3) = 0.20245186. sacrebleu: 20.24518585186855.
		{"bleu/effective-order", registry.NativeMetricSpec{Metric: "bleu"}, "the cat sat", "the cat is on the mat", 0.2024518585186855},
		// 2-token hypothesis: orders 1..2 only, both precision 1; BP = exp(1-6/2) = e^-2.
		{"bleu/short-hyp", registry.NativeMetricSpec{Metric: "bleu"}, "the cat", "the cat sat on the mat", math.Exp(-2)},
		{"bleu/no-overlap", registry.NativeMetricSpec{Metric: "bleu"}, "xyz", "abc def", 0},

		// --- rouge --- pred "the cat sat on the mat" vs ref "the cat is on the mat":
		// unigram overlap 5 of 6/6 -> F = 5/6; bigram overlap 3 of 5/5 -> F = 0.6;
		// LCS "the cat on the mat" = 5 -> F = 5/6.
		{"rouge/rouge1", registry.NativeMetricSpec{Metric: "rouge", RougeType: "rouge1"}, "the cat sat on the mat", "the cat is on the mat", 5.0 / 6},
		{"rouge/rouge2", registry.NativeMetricSpec{Metric: "rouge", RougeType: "rouge2"}, "the cat sat on the mat", "the cat is on the mat", 0.6},
		{"rouge/rougeL-default", registry.NativeMetricSpec{Metric: "rouge"}, "the cat sat on the mat", "the cat is on the mat", 5.0 / 6},
		{"rouge/case-and-punct", registry.NativeMetricSpec{Metric: "rouge", RougeType: "rouge1"}, "The CAT!", "the cat", 1},
		// Swapped sentence order: flat LCS is only "on the mat" (3) -> rougeL = 0.5,
		// but summary-level union-LCS recovers "the cat" + "on the mat" (5 hits of
		// 6/6) -> rougeLsum = 5/6.
		{"rouge/rougeL-swapped", registry.NativeMetricSpec{Metric: "rouge", RougeType: "rougeL"}, "on the mat\nthe cat sat", "the cat is\non the mat", 0.5},
		{"rouge/rougeLsum-swapped", registry.NativeMetricSpec{Metric: "rouge", RougeType: "rougeLsum"}, "on the mat\nthe cat sat", "the cat is\non the mat", 5.0 / 6},
		{"rouge/empty-pred", registry.NativeMetricSpec{Metric: "rouge"}, "", "the cat", 0},

		// --- tool_call_valid (first tool call only) ---
		{"tcv/valid", registry.NativeMetricSpec{Metric: "tool_call_valid"}, tcWeatherParis, tcWeatherParis, 1},
		{"tcv/no-args", registry.NativeMetricSpec{Metric: "tool_call_valid"}, tcNoArgs, tcWeatherParis, 0},
		{"tcv/string-args", registry.NativeMetricSpec{Metric: "tool_call_valid"}, tcStringArgs, tcWeatherParis, 0},
		{"tcv/no-calls", registry.NativeMetricSpec{Metric: "tool_call_valid"}, tcNone, tcWeatherParis, 0},
		{"tcv/not-json", registry.NativeMetricSpec{Metric: "tool_call_valid"}, "get_weather(Paris)", tcWeatherParis, 0},

		// --- tool_name_match ---
		{"tnm/match", registry.NativeMetricSpec{Metric: "tool_name_match"}, tcWeatherMixed, tcWeatherParis, 1},
		{"tnm/mismatch", registry.NativeMetricSpec{Metric: "tool_name_match"}, tcTimeParis, tcWeatherParis, 0},
		{"tnm/both-none", registry.NativeMetricSpec{Metric: "tool_name_match"}, tcNone, tcNone, 1},
		{"tnm/pred-none", registry.NativeMetricSpec{Metric: "tool_name_match"}, tcNone, tcWeatherParis, 0},

		// --- tool_parameter_key_match: reference keys {city,unit,days} ---
		{"tpk/all-keys", registry.NativeMetricSpec{Metric: "tool_parameter_key_match"}, tcWeatherMixed, tcWeatherParis, 1}, // extra "lang" not penalized
		{"tpk/wrong-tool", registry.NativeMetricSpec{Metric: "tool_parameter_key_match"}, tcTimeParis, tcWeatherParis, 0},  // names differ -> 0 (Vertex parity)
		{"tpk/pred-invalid", registry.NativeMetricSpec{Metric: "tool_parameter_key_match"}, "{", tcWeatherParis, 0},

		// --- tool_parameter_kv_match: city equal, unit C!=F, days 3 == 3.0 ---
		{"tpkv/two-of-three", registry.NativeMetricSpec{Metric: "tool_parameter_kv_match"}, tcWeatherMixed, tcWeatherParis, 2.0 / 3},
		{"tpkv/exact", registry.NativeMetricSpec{Metric: "tool_parameter_kv_match"}, tcWeatherParis, tcWeatherParis, 1},

		// --- trajectories: reference [A{x:1}, B{y:2,z:[1,2]}] ---
		{"texact/same", registry.NativeMetricSpec{Metric: "trajectory_exact_match"}, trajABkeys, trajAB, 1},
		{"texact/extra", registry.NativeMetricSpec{Metric: "trajectory_exact_match"}, trajACB, trajAB, 0},
		{"tinorder/subsequence", registry.NativeMetricSpec{Metric: "trajectory_in_order_match"}, trajACB, trajAB, 1},
		{"tinorder/reversed", registry.NativeMetricSpec{Metric: "trajectory_in_order_match"}, trajBA, trajAB, 0},
		{"tanyorder/reversed", registry.NativeMetricSpec{Metric: "trajectory_any_order_match"}, trajBA, trajAB, 1},
		{"tanyorder/wrong-input", registry.NativeMetricSpec{Metric: "trajectory_any_order_match"}, trajAwrong, trajAB, 0},
		{"tanyorder/duplicate-needs-two", registry.NativeMetricSpec{Metric: "trajectory_any_order_match"}, trajAB, trajAA, 0},
		{"tprec/extra-call", registry.NativeMetricSpec{Metric: "trajectory_precision"}, trajACB, trajAB, 2.0 / 3},
		{"tprec/duplicate", registry.NativeMetricSpec{Metric: "trajectory_precision"}, trajAA, trajAB, 0.5},
		{"tprec/wrong-input", registry.NativeMetricSpec{Metric: "trajectory_precision"}, trajAwrong, trajAB, 0},
		{"tprec/both-empty", registry.NativeMetricSpec{Metric: "trajectory_precision"}, trajEmpty, trajEmpty, 1},
		{"tprec/invalid-pred", registry.NativeMetricSpec{Metric: "trajectory_precision"}, trajInvalid, trajAB, 0},
		{"trecall/extra-call", registry.NativeMetricSpec{Metric: "trajectory_recall"}, trajACB, trajAB, 1},
		{"trecall/duplicate", registry.NativeMetricSpec{Metric: "trajectory_recall"}, trajAA, trajAB, 0.5},
		{"trecall/empty-pred", registry.NativeMetricSpec{Metric: "trajectory_recall"}, trajEmpty, trajAB, 0},
		{"tsingle/present", registry.NativeMetricSpec{Metric: "trajectory_single_tool_use", ToolName: "B"}, trajACB, "", 1},
		{"tsingle/absent", registry.NativeMetricSpec{Metric: "trajectory_single_tool_use", ToolName: "B"}, trajAwrong, "", 0},
	}
	// A client-free engine: the local computation path must never need a client.
	eng := NewEngine(nil, "proj", "us-central1")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := computationTmpl(tc.spec)
			inst := textInst("response", tc.pred)
			// trajectory_single_tool_use reads no reference, and tool_call_valid
			// reads one only when referenceField is mapped explicitly.
			if tc.spec.Metric != "trajectory_single_tool_use" && tc.spec.Metric != "tool_call_valid" {
				inst.Fields["reference"] = AssetRef{Modality: registry.ModalityText, Text: tc.ref}
			}
			res, err := eng.Run(context.Background(), tmpl, inst)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Score == nil {
				t.Fatal("Score is nil")
			}
			if got := float64(*res.Score); math.Abs(got-tc.want) > 1e-6 {
				t.Errorf("score = %.10f, want %.10f (%s)", got, tc.want, res.Explanation)
			}
			if res.CustomOutput["engine"] != "local" || res.CustomOutput["metric"] != tc.spec.Metric {
				t.Errorf("CustomOutput = %v, want engine=local metric=%s", res.CustomOutput, tc.spec.Metric)
			}
			if res.Applied != nil {
				t.Errorf("Applied = %#v, want nil (no autorater)", res.Applied)
			}
			if res.Passed != nil {
				t.Errorf("Passed = %v, want nil without a passThreshold", *res.Passed)
			}
		})
	}
}

// TestSentenceBLEUHighPrecision pins the full-precision float64 values against
// sacrebleu (score/100) so a tokenization regression cannot hide inside the
// float32 Result.Score.
func TestSentenceBLEUHighPrecision(t *testing.T) {
	for _, tc := range []struct {
		hyp, ref string
		want     float64
	}{
		{"The cat sat on the mat.", "The cat is sitting on the mat.", 42.38365628278778 / 100},
		{"It is a guide to action which ensures that the military always obeys the commands of the party.",
			"It is a guide to action that ensures that the military will forever heed Party commands.", 39.67088290836576 / 100},
		{"the quick brown fox jumps over the lazy dog", "the quick brown fox jumped over the lazy dog", 59.694917920196445 / 100},
	} {
		if got := sentenceBLEU(tc.hyp, tc.ref); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("sentenceBLEU(%q) = %.15f, want %.15f", tc.hyp, got, tc.want)
		}
	}
}

// TestTokenize13a spot-checks the sacrebleu 13a rules (punctuation padding,
// digit-aware period/comma/dash splitting).
func TestTokenize13a(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"The cat sat on the mat.", "The cat sat on the mat ."},
		{"It costs $3.50, ok?", "It costs $ 3.50 , ok ?"},
		{"pages 10-12 (see [1])", "pages 10 - 12 ( see [ 1 ] )"},
		{"a &amp; b", "a & b"},
	} {
		if got := strings.Join(tokenize13a(tc.in), " "); got != tc.want {
			t.Errorf("tokenize13a(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestComputationPassThreshold: Passed = Score >= passThreshold.
func TestComputationPassThreshold(t *testing.T) {
	eng := NewEngine(nil, "proj", "us-central1")
	for _, tc := range []struct {
		thr  float64
		want bool
	}{{0.4, true}, {0.5, false}} {
		thr := tc.thr
		tmpl := computationTmpl(registry.NativeMetricSpec{Metric: "bleu", PassThreshold: &thr})
		res, err := eng.Run(context.Background(), tmpl, textInst("response", "The cat sat on the mat.", "reference", "The cat is sitting on the mat."))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.Passed == nil || *res.Passed != tc.want {
			t.Errorf("threshold %.2f: Passed = %v, want %v (score %v)", thr, res.Passed, tc.want, *res.Score)
		}
	}
	// A score exactly at the threshold passes.
	one := 1.0
	res, err := eng.Run(context.Background(), computationTmpl(registry.NativeMetricSpec{Metric: "exact_match", PassThreshold: &one}), textInst("response", "a", "reference", "a"))
	if err != nil || res.Passed == nil || !*res.Passed {
		t.Errorf("exact_match at threshold 1: Passed = %v, err = %v; want true", res.Passed, err)
	}
}

// TestComputationEngineGuards covers the engine-selection and field rules.
func TestComputationEngineGuards(t *testing.T) {
	ctx := context.Background()
	fd := &fakeDiffusionClient{}
	fc := &evaltest.FakeEvaluationClient{}
	eng := NewEngine(fc, "proj", "us-central1", WithDiffusionClient(fd))
	tmpl := computationTmpl(registry.NativeMetricSpec{Metric: "exact_match"})
	inst := textInst("response", "a", "reference", "a")

	// Diffusion is refused with a clear "no model" error, before any call.
	_, err := eng.Run(ctx, tmpl, inst, WithEngine("diffusion"))
	if err == nil || !strings.Contains(err.Error(), "need no model") {
		t.Errorf("diffusion on computation: err = %v, want a 'need no model' error", err)
	}
	if fd.gotSchema != "" || fc.Calls() != 0 {
		t.Error("diffusion refusal must not reach any client")
	}

	// "local" runs locally even when a Vertex client is wired; no Vertex call.
	res, err := eng.Run(ctx, tmpl, inst, WithEngine("local"))
	if err != nil || res.Score == nil || *res.Score != 1 {
		t.Fatalf("local: res=%v err=%v", res.Score, err)
	}
	if fc.Calls() != 0 {
		t.Errorf("local engine made %d Vertex call(s), want 0", fc.Calls())
	}

	// A malformed --model is irrelevant: computation never validates a model.
	if _, err := eng.Run(ctx, tmpl, inst, WithModel("projects/../evil"), WithEngine("local")); err != nil {
		t.Errorf("computation must ignore --model, got %v", err)
	}

	// "local" on an LLM-judged kind is refused.
	pw := registry.MetricTemplate{ID: "q/p", Kind: registry.KindPointwise, MetricPromptTemplate: "Rate {{response}}"}
	if _, err := eng.Run(ctx, pw, textInst("response", "x"), WithEngine("local")); err == nil || !strings.Contains(err.Error(), "model-free") {
		t.Errorf("local on pointwise: err = %v, want a model-free error", err)
	}

	// Unknown and missing fields fail loud.
	if _, err := eng.Run(ctx, tmpl, textInst("response", "a", "reference", "a", "answer", "b")); err == nil || !strings.Contains(err.Error(), "unknown instance field") {
		t.Errorf("unknown field: err = %v", err)
	}
	if _, err := eng.Run(ctx, tmpl, textInst("response", "a")); err == nil || !strings.Contains(err.Error(), "missing required field") {
		t.Errorf("missing field: err = %v", err)
	}
	// A malformed REFERENCE is an error (gold data), not a zero score.
	tn := computationTmpl(registry.NativeMetricSpec{Metric: "tool_name_match"})
	if _, err := eng.Run(ctx, tn, textInst("response", tcWeatherParis, "reference", "{oops")); err == nil {
		t.Error("invalid reference JSON: want an error")
	}
}

// TestComputationVertexRequests checks that --engine vertex builds the correct
// EvaluateInstances oneof input (with no AutoraterConfig) and maps the matching
// *Results message onto Result.Score.
func TestComputationVertexRequests(t *testing.T) {
	score := proto.Float32(0.75)
	cases := []struct {
		name  string
		spec  registry.NativeMetricSpec
		pred  string
		ref   string
		resp  *aiplatformpb.EvaluateInstancesResponse
		check func(t *testing.T, req *aiplatformpb.EvaluateInstancesRequest)
	}{
		{
			name: "exact_match",
			spec: registry.NativeMetricSpec{Metric: "exact_match"},
			pred: "Paris", ref: "paris",
			resp: &aiplatformpb.EvaluateInstancesResponse{EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_ExactMatchResults{
				ExactMatchResults: &aiplatformpb.ExactMatchResults{ExactMatchMetricValues: []*aiplatformpb.ExactMatchMetricValue{{Score: score}}}}},
			check: func(t *testing.T, req *aiplatformpb.EvaluateInstancesRequest) {
				in := req.GetExactMatchInput()
				if in == nil || len(in.Instances) != 1 || in.Instances[0].GetPrediction() != "Paris" || in.Instances[0].GetReference() != "paris" {
					t.Errorf("ExactMatchInput = %v", in)
				}
			},
		},
		{
			name: "rouge",
			spec: registry.NativeMetricSpec{Metric: "rouge", RougeType: "rougeLsum"},
			pred: "a b", ref: "a c",
			resp: &aiplatformpb.EvaluateInstancesResponse{EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_RougeResults{
				RougeResults: &aiplatformpb.RougeResults{RougeMetricValues: []*aiplatformpb.RougeMetricValue{{Score: score}}}}},
			check: func(t *testing.T, req *aiplatformpb.EvaluateInstancesRequest) {
				in := req.GetRougeInput()
				if in == nil || in.MetricSpec.GetRougeType() != "rougeLsum" || in.MetricSpec.GetUseStemmer() || in.MetricSpec.GetSplitSummaries() {
					t.Fatalf("RougeInput spec = %v", in.GetMetricSpec())
				}
				if in.Instances[0].GetPrediction() != "a b" || in.Instances[0].GetReference() != "a c" {
					t.Errorf("RougeInput instance = %v", in.Instances[0])
				}
			},
		},
		{
			name: "tool_parameter_kv_match",
			spec: registry.NativeMetricSpec{Metric: "tool_parameter_kv_match"},
			pred: tcWeatherMixed, ref: tcWeatherParis,
			resp: &aiplatformpb.EvaluateInstancesResponse{EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_ToolParameterKvMatchResults{
				ToolParameterKvMatchResults: &aiplatformpb.ToolParameterKVMatchResults{ToolParameterKvMatchMetricValues: []*aiplatformpb.ToolParameterKVMatchMetricValue{{Score: score}}}}},
			check: func(t *testing.T, req *aiplatformpb.EvaluateInstancesRequest) {
				in := req.GetToolParameterKvMatchInput()
				if in == nil || in.Instances[0].GetPrediction() != tcWeatherMixed || in.Instances[0].GetReference() != tcWeatherParis {
					t.Errorf("ToolParameterKvMatchInput = %v", in)
				}
			},
		},
		{
			name: "trajectory_precision",
			spec: registry.NativeMetricSpec{Metric: "trajectory_precision"},
			pred: trajABkeys, ref: trajAB,
			resp: &aiplatformpb.EvaluateInstancesResponse{EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_TrajectoryPrecisionResults{
				TrajectoryPrecisionResults: &aiplatformpb.TrajectoryPrecisionResults{TrajectoryPrecisionMetricValues: []*aiplatformpb.TrajectoryPrecisionMetricValue{{Score: score}}}}},
			check: func(t *testing.T, req *aiplatformpb.EvaluateInstancesRequest) {
				in := req.GetTrajectoryPrecisionInput()
				if in == nil || len(in.Instances) != 1 {
					t.Fatalf("TrajectoryPrecisionInput = %v", in)
				}
				var got []string
				for _, c := range in.Instances[0].GetPredictedTrajectory().GetToolCalls() {
					got = append(got, c.GetToolName()+" "+c.GetToolInput())
				}
				// tool_input is sent as CANONICAL JSON (sorted keys), whether it was
				// authored as an object or as an embedded JSON string.
				want := []string{`A {"x":1}`, `B {"y":2,"z":[1,2]}`}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("predicted tool calls = %q, want %q", got, want)
				}
				if n := len(in.Instances[0].GetReferenceTrajectory().GetToolCalls()); n != 2 {
					t.Errorf("reference tool calls = %d, want 2", n)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &evaltest.FakeEvaluationClient{Resp: tc.resp}
			eng := NewEngine(fc, "proj-1", "us-central1")
			res, err := eng.Run(context.Background(), computationTmpl(tc.spec), textInst("response", tc.pred, "reference", tc.ref), WithEngine("vertex"))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			req := fc.LastRequest()
			if req == nil {
				t.Fatal("no EvaluateInstances call")
			}
			if req.GetLocation() != "projects/proj-1/locations/us-central1" {
				t.Errorf("Location = %q, want the regional location", req.GetLocation())
			}
			if req.AutoraterConfig != nil {
				t.Errorf("AutoraterConfig = %v, want nil (computation has no model)", req.AutoraterConfig)
			}
			tc.check(t, req)
			if res.Score == nil || *res.Score != 0.75 {
				t.Errorf("Score = %v, want 0.75", res.Score)
			}
			if res.CustomOutput["engine"] != "vertex" || res.Applied != nil {
				t.Errorf("engine=%v Applied=%v, want vertex / nil", res.CustomOutput["engine"], res.Applied)
			}
		})
	}
}

// TestComputationVertexEveryMetricBuilds makes sure every computation metric
// builds a request with a non-nil oneof input (guards against a missing case).
func TestComputationVertexEveryMetricBuilds(t *testing.T) {
	for _, m := range registry.NativeMetricIDs(registry.KindComputation) {
		spec := &registry.NativeMetricSpec{Metric: m, ToolName: "A"}
		pred, ref := "x", "x"
		if strings.HasPrefix(m, "trajectory_") {
			pred, ref = trajAB, trajAB
		}
		req, err := buildComputationRequest("projects/p/locations/l", spec, map[string]string{"response": pred, "reference": ref})
		if err != nil || req.MetricInputs == nil {
			t.Errorf("%s: req=%v err=%v", m, req, err)
		}
	}
}

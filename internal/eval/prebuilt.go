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

// prebuilt.go implements kind:prebuilt on Vertex: Google's named judge metrics
// (safety, groundedness, fluency, coherence, fulfillment, summarization_*,
// question_answering_*, pairwise_summarization_quality,
// pairwise_question_answering_quality) run through EvaluateInstances with the
// metric's dedicated oneof input (SafetyInput, GroundednessInput, ...). The judge
// prompt and rating scale are Vertex's own; mizan only maps the template's
// declared inputs into the instance slots each proto defines and supplies the
// AutoraterConfig (model, sampling, and — for pairwise_* — flip).
//
// The request goes through evaluateRouted, so a global-only judge model is routed
// to the global host (fast path) or retried there on an autorater NOT_FOUND,
// exactly like the pointwise/pairwise paths. The DiffusionGemma mapping for the
// same metrics lives in diffusion.go (buildPrebuiltDiffusionSchema).

import (
	"context"
	"fmt"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// prebuiltResult is the common shape of every prebuilt *Result message: the
// pointwise ones expose GetScore, the pairwise ones GetPairwiseChoice; all expose
// an explanation and an optional confidence.
type prebuiltResult struct {
	score      *float32
	confidence *float32
	choice     *aiplatformpb.PairwiseChoice
	explain    string
}

// optStr returns a proto string pointer for an optional role, nil when absent.
func optStr(f map[string]string, role string) *string {
	if v, ok := f[role]; ok {
		return proto.String(v)
	}
	return nil
}

// setPrebuiltInput places the metric's oneof input on req, built from the
// role->text map. The *Spec UseReference flag is set exactly when a reference
// was mapped (and supplied), so Vertex uses its reference-aware template variant.
func setPrebuiltInput(req *aiplatformpb.EvaluateInstancesRequest, metric string, f map[string]string) error {
	pred := optStr(f, registry.NativeRoleResponse)
	ref := optStr(f, registry.NativeRoleReference)
	ctx := optStr(f, registry.NativeRoleContext)
	instr := optStr(f, registry.NativeRoleInstruction)
	base := optStr(f, registry.NativeRoleBaseline)
	useRef := ref != nil

	switch metric {
	case "safety":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_SafetyInput{SafetyInput: &aiplatformpb.SafetyInput{
			MetricSpec: &aiplatformpb.SafetySpec{}, Instance: &aiplatformpb.SafetyInstance{Prediction: pred}}}
	case "groundedness":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_GroundednessInput{GroundednessInput: &aiplatformpb.GroundednessInput{
			MetricSpec: &aiplatformpb.GroundednessSpec{}, Instance: &aiplatformpb.GroundednessInstance{Prediction: pred, Context: ctx}}}
	case "fluency":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_FluencyInput{FluencyInput: &aiplatformpb.FluencyInput{
			MetricSpec: &aiplatformpb.FluencySpec{}, Instance: &aiplatformpb.FluencyInstance{Prediction: pred}}}
	case "coherence":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_CoherenceInput{CoherenceInput: &aiplatformpb.CoherenceInput{
			MetricSpec: &aiplatformpb.CoherenceSpec{}, Instance: &aiplatformpb.CoherenceInstance{Prediction: pred}}}
	case "fulfillment":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_FulfillmentInput{FulfillmentInput: &aiplatformpb.FulfillmentInput{
			MetricSpec: &aiplatformpb.FulfillmentSpec{}, Instance: &aiplatformpb.FulfillmentInstance{Prediction: pred, Instruction: instr}}}
	case "summarization_quality":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_SummarizationQualityInput{SummarizationQualityInput: &aiplatformpb.SummarizationQualityInput{
			MetricSpec: &aiplatformpb.SummarizationQualitySpec{UseReference: useRef},
			Instance:   &aiplatformpb.SummarizationQualityInstance{Prediction: pred, Reference: ref, Context: ctx, Instruction: instr}}}
	case "summarization_helpfulness":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_SummarizationHelpfulnessInput{SummarizationHelpfulnessInput: &aiplatformpb.SummarizationHelpfulnessInput{
			MetricSpec: &aiplatformpb.SummarizationHelpfulnessSpec{UseReference: useRef},
			Instance:   &aiplatformpb.SummarizationHelpfulnessInstance{Prediction: pred, Reference: ref, Context: ctx, Instruction: instr}}}
	case "summarization_verbosity":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_SummarizationVerbosityInput{SummarizationVerbosityInput: &aiplatformpb.SummarizationVerbosityInput{
			MetricSpec: &aiplatformpb.SummarizationVerbositySpec{UseReference: useRef},
			Instance:   &aiplatformpb.SummarizationVerbosityInstance{Prediction: pred, Reference: ref, Context: ctx, Instruction: instr}}}
	case "question_answering_quality":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_QuestionAnsweringQualityInput{QuestionAnsweringQualityInput: &aiplatformpb.QuestionAnsweringQualityInput{
			MetricSpec: &aiplatformpb.QuestionAnsweringQualitySpec{UseReference: useRef},
			Instance:   &aiplatformpb.QuestionAnsweringQualityInstance{Prediction: pred, Reference: ref, Context: ctx, Instruction: instr}}}
	case "question_answering_relevance":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_QuestionAnsweringRelevanceInput{QuestionAnsweringRelevanceInput: &aiplatformpb.QuestionAnsweringRelevanceInput{
			MetricSpec: &aiplatformpb.QuestionAnsweringRelevanceSpec{UseReference: useRef},
			Instance:   &aiplatformpb.QuestionAnsweringRelevanceInstance{Prediction: pred, Reference: ref, Context: ctx, Instruction: instr}}}
	case "question_answering_helpfulness":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_QuestionAnsweringHelpfulnessInput{QuestionAnsweringHelpfulnessInput: &aiplatformpb.QuestionAnsweringHelpfulnessInput{
			MetricSpec: &aiplatformpb.QuestionAnsweringHelpfulnessSpec{UseReference: useRef},
			Instance:   &aiplatformpb.QuestionAnsweringHelpfulnessInstance{Prediction: pred, Reference: ref, Context: ctx, Instruction: instr}}}
	case "question_answering_correctness":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_QuestionAnsweringCorrectnessInput{QuestionAnsweringCorrectnessInput: &aiplatformpb.QuestionAnsweringCorrectnessInput{
			MetricSpec: &aiplatformpb.QuestionAnsweringCorrectnessSpec{UseReference: useRef},
			Instance:   &aiplatformpb.QuestionAnsweringCorrectnessInstance{Prediction: pred, Reference: ref, Context: ctx, Instruction: instr}}}
	case "pairwise_summarization_quality":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_PairwiseSummarizationQualityInput{PairwiseSummarizationQualityInput: &aiplatformpb.PairwiseSummarizationQualityInput{
			MetricSpec: &aiplatformpb.PairwiseSummarizationQualitySpec{UseReference: useRef},
			Instance: &aiplatformpb.PairwiseSummarizationQualityInstance{Prediction: pred, BaselinePrediction: base, Reference: ref,
				Context: ctx, Instruction: instr}}}
	case "pairwise_question_answering_quality":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_PairwiseQuestionAnsweringQualityInput{PairwiseQuestionAnsweringQualityInput: &aiplatformpb.PairwiseQuestionAnsweringQualityInput{
			MetricSpec: &aiplatformpb.PairwiseQuestionAnsweringQualitySpec{UseReference: useRef},
			Instance: &aiplatformpb.PairwiseQuestionAnsweringQualityInstance{Prediction: pred, BaselinePrediction: base, Reference: ref,
				Context: ctx, Instruction: instr}}}
	default:
		return fmt.Errorf("unknown prebuilt metric %q", metric)
	}
	return nil
}

// prebuiltResultOf extracts the metric's result message from the response.
func prebuiltResultOf(metric string, resp *aiplatformpb.EvaluateInstancesResponse) (prebuiltResult, bool) {
	type pointwise interface {
		GetScore() float32
		GetExplanation() string
		GetConfidence() float32
	}
	pw := func(r pointwise, score, conf *float32) (prebuiltResult, bool) {
		if r == nil || score == nil {
			return prebuiltResult{}, false
		}
		return prebuiltResult{score: score, confidence: conf, explain: r.GetExplanation()}, true
	}
	switch metric {
	case "safety":
		if r := resp.GetSafetyResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "groundedness":
		if r := resp.GetGroundednessResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "fluency":
		if r := resp.GetFluencyResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "coherence":
		if r := resp.GetCoherenceResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "fulfillment":
		if r := resp.GetFulfillmentResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "summarization_quality":
		if r := resp.GetSummarizationQualityResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "summarization_helpfulness":
		if r := resp.GetSummarizationHelpfulnessResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "summarization_verbosity":
		if r := resp.GetSummarizationVerbosityResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "question_answering_quality":
		if r := resp.GetQuestionAnsweringQualityResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "question_answering_relevance":
		if r := resp.GetQuestionAnsweringRelevanceResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "question_answering_helpfulness":
		if r := resp.GetQuestionAnsweringHelpfulnessResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "question_answering_correctness":
		if r := resp.GetQuestionAnsweringCorrectnessResult(); r != nil {
			return pw(r, r.Score, r.Confidence)
		}
	case "pairwise_summarization_quality":
		if r := resp.GetPairwiseSummarizationQualityResult(); r != nil {
			c := r.GetPairwiseChoice()
			return prebuiltResult{choice: &c, confidence: r.Confidence, explain: r.GetExplanation()}, true
		}
	case "pairwise_question_answering_quality":
		if r := resp.GetPairwiseQuestionAnsweringQualityResult(); r != nil {
			c := r.GetPairwiseChoice()
			return prebuiltResult{choice: &c, confidence: r.Confidence, explain: r.GetExplanation()}, true
		}
	}
	return prebuiltResult{}, false
}

// runPrebuilt materializes the metric's oneof input + AutoraterConfig, calls
// EvaluateInstances via evaluateRouted (R-GLOBAL routing), and maps the result:
// Score / Confidence / Explanation for pointwise metrics, PairwiseChoice for the
// pairwise_* metrics, and Passed = Score >= passThreshold when one is declared.
// PrebuiltServiceJudge labels the judge of a Vertex predefined metric, which
// the caller cannot choose (see runPrebuilt).
const PrebuiltServiceJudge = "vertex-service-default"

func (e *Engine) runPrebuilt(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	if e.client == nil {
		return Result{}, fmt.Errorf("eval: no evaluation client configured")
	}
	fields, err := nativeTextFields(tmpl, inst)
	if err != nil {
		return Result{}, err
	}
	metric := tmpl.Native.Metric
	pairwise := registry.IsPairwiseNativeMetric(metric)
	flip := false // predefined metrics take no AutoraterConfig, so flip cannot be requested
	_ = pairwise

	// build re-materializes the request per attempt so req.Location and the
	// autorater location always match the host actually used (R-GLOBAL).
	var buildErr error
	build := func(loc, _ string) *aiplatformpb.EvaluateInstancesRequest {
		// NO AutoraterConfig: EvaluateInstances rejects it for predefined metrics
		// ("AutoraterConfig only supports PointwiseMetric and PairwiseMetric",
		// observed 2026-09-26). The judge, sampling and flip are the service's
		// own defaults (documented judge: gemini-2.5-flash), so a template's
		// autorater.model / samplingCount / flipEnabled do not apply here.
		req := &aiplatformpb.EvaluateInstancesRequest{
			Location: fmt.Sprintf("projects/%s/locations/%s", e.projectID, loc),
		}
		if err := setPrebuiltInput(req, metric, fields); err != nil {
			buildErr = err
		}
		return req
	}
	// Surface an unknown metric before any network call (it cannot happen for a
	// validated template, but the engine must not send an input-less request).
	if err := setPrebuiltInput(&aiplatformpb.EvaluateInstancesRequest{}, metric, fields); err != nil {
		return Result{}, fmt.Errorf("eval: template %q: %w", tmpl.ID, err)
	}

	// Direct regional call: predefined metrics are served on the regional host
	// and take no judge model, so the resolved model neither forces global
	// routing nor is expanded into a resource name.
	_ = model
	resp, err := e.client.EvaluateInstances(ctx, build(e.location, ""))
	if err != nil {
		err = fmt.Errorf("eval: EvaluateInstances (%s): %w", metric, err)
	}
	if buildErr != nil {
		return Result{}, fmt.Errorf("eval: template %q: %w", tmpl.ID, buildErr)
	}
	if err != nil {
		return Result{}, err
	}
	pr, ok := prebuiltResultOf(metric, resp)
	if !ok {
		return Result{}, fmt.Errorf("eval: EvaluateInstances response contained no %s result", metric)
	}
	res := Result{
		Score:        pr.score,
		Confidence:   pr.confidence,
		Explanation:  pr.explain,
		CustomOutput: map[string]any{"metric": metric, "engine": engineVertex},
	}
	if pr.choice != nil {
		res.PairwiseChoice = pairwiseChoiceString(*pr.choice)
		res.CustomOutput["selection"] = res.PairwiseChoice
	}
	if flip {
		res.Warnings = append(res.Warnings, pairwiseFlipWarning)
	}
	res.CustomOutput["judge"] = PrebuiltServiceJudge
	applyPassThreshold(&res, tmpl.Native)
	return res, nil
}

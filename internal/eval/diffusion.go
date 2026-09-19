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
	"fmt"
	"strings"

	"github.com/ghchinoy/mizan/internal/eval/diffusion"
	"github.com/ghchinoy/mizan/internal/registry"
)

// WithDiffusionClient sets the diffusion client used by the DiffusionGemma path.
func WithDiffusionClient(d diffusion.Client) Option {
	return func(e *Engine) { e.diffusion = d }
}

// WithEngine selects an engine override for a run (e.g. "vertex", "diffusion", "genai").
func WithEngine(engine string) RunOption {
	return func(rc *runConfig) { rc.engineOverride = strings.ToLower(strings.TrimSpace(engine)) }
}

// buildDiffusionSchema creates the DiffusionGemma decision schema based on template kind.
func buildDiffusionSchema(tmpl registry.MetricTemplate) (string, error) {
	var q diffusion.QuestionSchema
	switch tmpl.Kind {
	case registry.KindBoul:
		q = diffusion.QuestionSchema{
			ID:           "verdict",
			Type:         "boolean",
			Instructions: tmpl.MetricPromptTemplate,
		}
	case registry.KindChoice:
		var opts []diffusion.ChoiceOption
		for _, c := range tmpl.Choices {
			opts = append(opts, diffusion.ChoiceOption{Name: c})
		}
		q = diffusion.QuestionSchema{
			ID:           "selection",
			Type:         "choice",
			Instructions: tmpl.MetricPromptTemplate,
			Options:      opts,
		}
	case registry.KindScore, registry.KindPointwise:
		q = diffusion.QuestionSchema{
			ID:           "score",
			Type:         "score",
			Instructions: tmpl.MetricPromptTemplate,
			Levels:       []string{"1", "2", "3", "4", "5"},
		}
	default:
		return "", fmt.Errorf("eval: diffusion engine does not support metric kind %q", tmpl.Kind)
	}

	payload := diffusion.DecisionSchemaPayload{
		Questions: []diffusion.QuestionSchema{q},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("eval: marshal diffusion schema: %w", err)
	}
	return string(b), nil
}

// extractDiffusionInputs substitutes text variables and collects any image references,
// formatting the state into a JSON dictionary as required by DiffusionGemma.
func extractDiffusionInputs(template string, inst Instance) (string, []string, error) {
	vars := extractVars(template)
	var missing []string
	for _, v := range vars {
		if _, ok := inst.Fields[v]; !ok {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		return "", nil, fmt.Errorf("eval: instance is missing values for template variables %v", missing)
	}

	state := make(map[string]any)
	for k, ref := range inst.Fields {
		if isTextRef(ref) {
			state[k] = ref.Text
		}
	}

	var images []string
	rendered := varPattern.ReplaceAllStringFunc(template, func(match string) string {
		name := varPattern.FindStringSubmatch(match)[1]
		ref := inst.Fields[name]
		if isTextRef(ref) {
			return ref.Text
		}
		if ref.FilePath != "" {
			images = append(images, ref.FilePath)
		} else if ref.GCSUri != "" {
			images = append(images, ref.GCSUri)
		}
		return fmt.Sprintf("[attached %s: %q]", ref.Modality, name)
	})
	state["_prompt"] = rendered

	b, err := json.Marshal(state)
	if err != nil {
		return "", nil, fmt.Errorf("eval: marshal diffusion state: %w", err)
	}
	return string(b), images, nil
}

// runDiffusion evaluates a template using the DiffusionGemma client.
func (e *Engine) runDiffusion(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	if e.diffusion == nil {
		return Result{}, fmt.Errorf("eval: no diffusion client configured (wire WithDiffusionClient)")
	}

	schemaJSON, err := buildDiffusionSchema(tmpl)
	if err != nil {
		return Result{}, err
	}

	stateText, images, err := extractDiffusionInputs(tmpl.MetricPromptTemplate, inst)
	if err != nil {
		return Result{}, err
	}

	resp, stats, err := e.diffusion.Decide(ctx, schemaJSON, stateText, images...)
	if err != nil {
		return Result{}, err
	}

	res := Result{
		CustomOutput: map[string]any{},
	}

	if stats != nil {
		res.Stats.Duration = stats.WallTime
		res.CustomOutput["denoise_ms"] = stats.DenoiseMs
		res.CustomOutput["prefill_ms"] = stats.PrefillMs
		res.CustomOutput["steps"] = stats.DenoiseSteps
		res.CustomOutput["samples"] = stats.SamplesN
		res.Stats.TokenUsage = &TokenUsage{
			PromptTokens:     int32(stats.PromptTokens),
			CandidatesTokens: int32(stats.OutputTokens),
			TotalTokens:      int32(stats.TotalTokens),
		}
	}

	switch tmpl.Kind {
	case registry.KindBoul:
		if ans, ok := resp.Answers["verdict"]; ok {
			lbl := strings.ToLower(strings.TrimSpace(ans.Label))
			passed := lbl == "yes" || lbl == "true" || ans.Noul >= 0.5
			res.Passed = &passed
			var s float32
			if passed {
				s = 1.0
			} else {
				s = 0.0
			}
			res.Score = &s
			c32 := float32(ans.Confidence)
			res.Confidence = &c32
			res.Explanation = fmt.Sprintf("DiffusionGemma verdict: %s (confidence: %.1f%%, stderr: ±%.4f)", ans.Label, ans.Confidence*100, ans.Stderr)
			res.CustomOutput["passed"] = passed
			res.CustomOutput["stderr"] = ans.Stderr
			res.CustomOutput["agreement"] = ans.Agreement
			res.CustomOutput["probabilities"] = ans.Probabilities
		}
	case registry.KindChoice:
		if ans, ok := resp.Answers["selection"]; ok {
			choice := ans.Choice
			if choice == "" {
				choice = ans.Label
			}
			res.ChoiceSelection = choice
			c32 := float32(ans.Confidence)
			res.Confidence = &c32
			res.Explanation = fmt.Sprintf("DiffusionGemma classification: %s (confidence: %.1f%%)", choice, ans.Confidence*100)
			res.CustomOutput["selection"] = choice
			res.CustomOutput["stderr"] = ans.Stderr
			res.CustomOutput["probabilities"] = ans.Probabilities
		}
	case registry.KindScore, registry.KindPointwise:
		if ans, ok := resp.Answers["score"]; ok {
			s32 := float32(ans.Score)
			res.Score = &s32
			c32 := float32(ans.Confidence)
			res.Confidence = &c32
			res.Explanation = fmt.Sprintf("DiffusionGemma score: %g (confidence: %.1f%%)", ans.Score, ans.Confidence*100)
			res.CustomOutput["score"] = ans.Score
			res.CustomOutput["stderr"] = ans.Stderr
			res.CustomOutput["probabilities"] = ans.Probabilities
		}
	}

	return res, nil
}

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

// Run options that trade judge latency against accuracy on the genai path
// (Experiment 07b): an explicit Gemini thinking budget, and routing pairwise
// templates through genai structured output instead of native
// EvaluateInstances (which exposes no thinking control).

import (
	"context"
	"fmt"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

type thinkingBudgetKey struct{}

// WithThinkingBudget sets GenerateContentConfig.ThinkingConfig.ThinkingBudget on
// every genai call of the run (0 disables thinking where the model allows it).
// A negative value means "model default" (the option is then a no-op). It has
// no effect on the native EvaluateInstances paths.
func WithThinkingBudget(n int) RunOption {
	return func(rc *runConfig) {
		rc.thinkingBudget = n
		rc.thinkingSet = n >= 0
	}
}

// WithPairwiseGenai routes KindPairwise templates through genai structured
// output (a BASELINE/CANDIDATE/TIE enum) instead of native EvaluateInstances.
func WithPairwiseGenai(on bool) RunOption {
	return func(rc *runConfig) { rc.pairwiseGenai = on }
}

func withThinkingCtx(ctx context.Context, rc runConfig) context.Context {
	if !rc.thinkingSet {
		return ctx
	}
	return context.WithValue(ctx, thinkingBudgetKey{}, rc.thinkingBudget)
}

// applyThinking copies a context-carried thinking budget onto cfg.
func applyThinking(ctx context.Context, cfg *genai.GenerateContentConfig) {
	if n, ok := ctx.Value(thinkingBudgetKey{}).(int); ok {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: genai.Ptr(clampInt32(n))}
	}
}

// runPairwiseGenai judges a pairwise template with genai structured output.
func (e *Engine) runPairwiseGenai(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	choices := []string{pairBaseline, pairCandidate, pairTie}
	prompt := fmt.Sprintf("%s\n\nAnswer %s if the %q response is better, %s if the %q response is better, or %s if they are equally good. "+
		"Return a JSON object with \"selection\" (one of %v) and \"explanation\" (string).",
		tmpl.MetricPromptTemplate, pairBaseline, tmpl.BaselineFieldName, pairCandidate, tmpl.CandidateFieldName, pairTie, choices)
	res, err := e.runGenaiStructured(ctx, tmpl, inst, prompt, generateChoiceSchema(choices), model)
	if err != nil {
		return Result{}, err
	}
	if sel, ok := res.CustomOutput["selection"].(string); ok {
		res.PairwiseChoice = sel
	}
	if expl, ok := res.CustomOutput["explanation"].(string); ok {
		res.Explanation = expl
	}
	return res, nil
}

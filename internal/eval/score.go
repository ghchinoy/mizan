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
	"fmt"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

// generateScoreSchema returns the structured output schema for KindScore.
func generateScoreSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"score": {
				Type:        genai.TypeNumber,
				Description: "The numeric score for the evaluation.",
			},
			"explanation": {
				Type:        genai.TypeString,
				Description: "Concise explanation of the score.",
			},
		},
		Required: []string{"score", "explanation"},
	}
}

// runScore executes a KindScore evaluator via the direct genai structured-output path.
func (e *Engine) runScore(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string, rc runConfig) (Result, error) {
	if len(tmpl.RubricGroups) > 0 {
		min, max, scaleWarning := e.resolveRubricScale(tmpl, rc)
		res, err := e.runRubricStructured(ctx, tmpl, inst, model, min, max)
		if err == nil && scaleWarning != "" {
			res.Warnings = append(res.Warnings, scaleWarning)
		}
		return res, err
	}

	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}

	prompt := tmpl.MetricPromptTemplate + "\n\nRate the input according to the instructions above. Return a JSON object with \"score\" (number) and \"explanation\" (string)."
	schema := generateScoreSchema()

	res, err := e.runGenaiStructured(ctx, tmpl, inst, prompt, schema, model)
	if err != nil {
		return Result{}, err
	}

	if sVal, ok := toFloat(res.CustomOutput["score"]); ok {
		f32 := float32(sVal)
		res.Score = &f32
	}
	if explVal, ok := res.CustomOutput["explanation"].(string); ok && explVal != "" {
		res.Explanation = explVal
	}

	return res, nil
}

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

// generateBoulSchema returns the strict structured output schema for KindBoul.
func generateBoulSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"passed": {
				Type:        genai.TypeBoolean,
				Description: "True if the proposition holds or the evaluation passes; false otherwise.",
			},
			"confidence": {
				Type:        genai.TypeNumber,
				Description: "A float between 0.0 and 1.0 representing confidence in the decision.",
			},
			"explanation": {
				Type:        genai.TypeString,
				Description: "Concise rationale explaining why the proposition passed or failed.",
			},
		},
		Required: []string{"passed", "explanation"},
	}
}

// runBoul executes a KindBoul evaluator via the direct genai structured-output path.
func (e *Engine) runBoul(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}

	prompt := tmpl.MetricPromptTemplate + "\n\nEvaluate the proposition above. Return a JSON object with \"passed\" (boolean), \"confidence\" (float between 0.0 and 1.0), and \"explanation\" (string)."
	schema := generateBoulSchema()

	res, err := e.runGenaiStructured(ctx, tmpl, inst, prompt, schema, model)
	if err != nil {
		return Result{}, err
	}

	if passedVal, ok := res.CustomOutput["passed"].(bool); ok {
		res.Passed = &passedVal
		var score float32
		if passedVal {
			score = 1.0
		} else {
			score = 0.0
		}
		res.Score = &score
	}
	if confVal, ok := toFloat(res.CustomOutput["confidence"]); ok {
		c32 := float32(confVal)
		res.Confidence = &c32
	}
	if explVal, ok := res.CustomOutput["explanation"].(string); ok && explVal != "" {
		res.Explanation = explVal
	}

	return res, nil
}

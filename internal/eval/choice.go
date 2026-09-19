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
	"strings"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

// generateChoiceSchema returns the structured output schema for KindChoice with
// the allowed enum choices.
func generateChoiceSchema(choices []string) *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"selection": {
				Type:        genai.TypeString,
				Enum:        choices,
				Description: "The selected choice from the predefined list of allowed choices.",
			},
			"explanation": {
				Type:        genai.TypeString,
				Description: "Concise rationale for the selection.",
			},
		},
		Required: []string{"selection", "explanation"},
	}
}

// runChoice executes a KindChoice evaluator via the direct genai structured-output path.
func (e *Engine) runChoice(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	if len(tmpl.Choices) < 2 {
		return Result{}, fmt.Errorf("eval: choice template %q has fewer than 2 choices", tmpl.ID)
	}

	prompt := fmt.Sprintf("%s\n\nClassify/select exactly one of the following choices: [%s]. Return a JSON object with \"selection\" (matching one of the choices) and \"explanation\" (string).",
		tmpl.MetricPromptTemplate, strings.Join(tmpl.Choices, ", "))
	schema := generateChoiceSchema(tmpl.Choices)

	res, err := e.runGenaiStructured(ctx, tmpl, inst, prompt, schema, model)
	if err != nil {
		return Result{}, err
	}

	if selVal, ok := res.CustomOutput["selection"].(string); ok {
		res.ChoiceSelection = selVal
	}
	if explVal, ok := res.CustomOutput["explanation"].(string); ok && explVal != "" {
		res.Explanation = explVal
	}

	return res, nil
}

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
	"slices"
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

// resolveEffectiveChoices computes and validates the discrete choice enum for a
// KindChoice run under the precedence chain:
//
//  1. WithChoices(choices) explicit per-run override;
//  2. WithChoicesFromVariations(extra...) derived from bound Variation labels;
//  3. tmpl.Choices (verifying any bound Variation labels are covered).
func resolveEffectiveChoices(tmpl registry.MetricTemplate, inst Instance, rc runConfig, maxChoices int) ([]string, error) {
	vars := extractVars(tmpl.MetricPromptTemplate)
	var choices []string
	switch {
	case rc.choicesOverrideSet:
		choices = slices.Clone(rc.choicesOverride)
	case rc.choicesFromVariations:
		for _, v := range vars {
			ref := inst.Fields[v]
			for _, item := range ref.Items {
				choices = append(choices, item.Label)
			}
		}
		if len(choices) == 0 {
			return nil, fmt.Errorf("eval: WithChoicesFromVariations on template %q requires at least one bound Variations placeholder", tmpl.ID)
		}
		choices = append(choices, rc.choicesExtra...)
	default:
		choices = slices.Clone(tmpl.Choices)
		for _, v := range vars {
			ref := inst.Fields[v]
			for _, item := range ref.Items {
				if !slices.Contains(choices, item.Label) {
					return nil, fmt.Errorf("eval: variation label %q in placeholder %q is not in template %q choices %v (pass WithChoicesFromVariations or WithChoices)", item.Label, v, tmpl.ID, choices)
				}
			}
		}
	}

	if len(choices) < 2 {
		return nil, fmt.Errorf("eval: choice template %q has fewer than 2 choices (%d)", tmpl.ID, len(choices))
	}
	if len(choices) > maxChoices {
		return nil, fmt.Errorf("eval: choice template %q has %d choices, exceeds maximum %d", tmpl.ID, len(choices), maxChoices)
	}
	seen := make(map[string]bool, len(choices))
	for _, c := range choices {
		if strings.TrimSpace(c) == "" {
			return nil, fmt.Errorf("eval: choice template %q contains an empty choice", tmpl.ID)
		}
		if hasControlChars(c) {
			return nil, fmt.Errorf("eval: choice %q in template %q contains ASCII control characters", c, tmpl.ID)
		}
		if seen[c] {
			return nil, fmt.Errorf("eval: duplicate choice %q in template %q", c, tmpl.ID)
		}
		seen[c] = true
	}
	return choices, nil
}

// runChoice executes a KindChoice evaluator via the direct genai structured-output path.
func (e *Engine) runChoice(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string, rc runConfig) (Result, error) {
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	choices, err := resolveEffectiveChoices(tmpl, inst, rc, MaxChoices)
	if err != nil {
		return Result{}, err
	}

	directive := fmt.Sprintf("Classify/select exactly one of the following choices: [%s]. Return a JSON object with \"selection\" (matching one of the choices) and \"explanation\" (string).",
		strings.Join(choices, ", "))
	schema := generateChoiceSchema(choices)

	res, err := e.runGenaiStructured(ctx, tmpl, inst, tmpl.MetricPromptTemplate, directive, schema, model, rc)
	if err != nil {
		return Result{}, err
	}

	selVal, ok := res.CustomOutput["selection"].(string)
	if !ok || selVal == "" {
		return Result{}, fmt.Errorf("eval: choice template %q returned empty or non-string selection", tmpl.ID)
	}
	if !slices.Contains(choices, selVal) {
		return Result{}, fmt.Errorf("eval: choice template %q returned selection %q outside allowed choices %v", tmpl.ID, selVal, choices)
	}
	res.ChoiceSelection = selVal
	if explVal, ok := res.CustomOutput["explanation"].(string); ok && explVal != "" {
		res.Explanation = explVal
	}
	res.Applied = &AppliedAutorater{EffectiveChoices: choices}

	return res, nil
}

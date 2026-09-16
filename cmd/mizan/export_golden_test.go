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

package main

import (
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/staxexport"
)

// These golden tests lock the EXACT wire bytes the exporter emits against the
// interchange spec's §6 worked examples (design/mizan-stax-export-spec.md), which
// are byte-shaped to the REAL Stax LLMEvaluatorRequestDTO (google-labs-code/stax:
// domain/evaluator/llm/dto/LLMEvaluatorRequestDTO + BaseLLMEvaluatorRequestDTO,
// llmproviders/dto/Prompt, evaluator/dto/OutputCategoryDTO, EvaluatorVariableDTO).
// They marshal through the real CLI path (staxexport.Export -> marshalEvaluators),
// so they catch serialization regressions the struct-level tests cannot: a changed
// JSON tag (output_categories -> outputCategories), a body field renamed from
// "text" back to "content", a lowercased role, output_categories reverting to a
// map/object or losing value-string typing, or a re-ordered field would all keep
// the round-trip tests green yet break Stax ingestion. If the spec §6 examples and
// these strings ever diverge, code and spec have drifted and one must be fixed —
// do not "fix" the test by pasting the new output.

// wantPointwiseGolden is spec §6.2's pointwise worked example, byte-for-byte
// (real Stax create-evaluator request DTO), with a bound model_id.
const wantPointwiseGolden = `{
  "name": "acme/pointwise-quality",
  "output_format_type": "Choices",
  "variables": [
    {
      "name": "output",
      "required": true
    }
  ],
  "model_id": "model-123",
  "prompts": [
    {
      "role": "SYSTEM",
      "text": "Be strict."
    },
    {
      "role": "USER",
      "text": "Rate the response: {{output}}"
    }
  ],
  "output_categories": [
    {
      "name": "1-poor",
      "value": "1"
    },
    {
      "name": "score-2",
      "value": "2"
    },
    {
      "name": "score-3",
      "value": "3"
    },
    {
      "name": "score-4",
      "value": "4"
    },
    {
      "name": "5-great",
      "value": "5"
    }
  ]
}
`

// wantRubricFanoutGolden is spec §6.1's Option B (fan-out) worked example,
// byte-for-byte: a JSON array of one real-DTO evaluator per (group, criterion).
const wantRubricFanoutGolden = `[
  {
    "name": "acme/rubric-brand::clarity::clear",
    "output_format_type": "Choices",
    "variables": [
      {
        "name": "output",
        "required": true
      }
    ],
    "model_id": "model-123",
    "prompts": [
      {
        "role": "USER",
        "text": "Evaluate {{output}} on the criterion \"clear\" (rubric group: clarity). Return ONE category."
      }
    ],
    "output_categories": [
      {
        "name": "1-poor",
        "value": "1"
      },
      {
        "name": "score-2",
        "value": "2"
      },
      {
        "name": "score-3",
        "value": "3"
      },
      {
        "name": "score-4",
        "value": "4"
      },
      {
        "name": "5-great",
        "value": "5"
      }
    ]
  },
  {
    "name": "acme/rubric-brand::clarity::concise",
    "output_format_type": "Choices",
    "variables": [
      {
        "name": "output",
        "required": true
      }
    ],
    "model_id": "model-123",
    "prompts": [
      {
        "role": "USER",
        "text": "Evaluate {{output}} on the criterion \"concise\" (rubric group: clarity). Return ONE category."
      }
    ],
    "output_categories": [
      {
        "name": "1-poor",
        "value": "1"
      },
      {
        "name": "score-2",
        "value": "2"
      },
      {
        "name": "score-3",
        "value": "3"
      },
      {
        "name": "score-4",
        "value": "4"
      },
      {
        "name": "5-great",
        "value": "5"
      }
    ]
  },
  {
    "name": "acme/rubric-brand::tone::on-brand",
    "output_format_type": "Choices",
    "variables": [
      {
        "name": "output",
        "required": true
      }
    ],
    "model_id": "model-123",
    "prompts": [
      {
        "role": "USER",
        "text": "Evaluate {{output}} on the criterion \"on-brand\" (rubric group: tone). Return ONE category."
      }
    ],
    "output_categories": [
      {
        "name": "score-1",
        "value": "1"
      },
      {
        "name": "score-2",
        "value": "2"
      },
      {
        "name": "score-3",
        "value": "3"
      },
      {
        "name": "score-4",
        "value": "4"
      },
      {
        "name": "score-5",
        "value": "5"
      }
    ]
  }
]
`

// pointwiseQualityTemplate is spec §6.2's source template (kind: pointwise, with
// the inline RatingRubric the spec adds to show band derivation).
func pointwiseQualityTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   "acme/pointwise-quality",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		Inputs:               []registry.InputSpec{{Name: "response", Modality: registry.ModalityText, Required: true}},
		MetricPromptTemplate: "Rate the response: {{response}}",
		SystemInstruction:    "Be strict.",
		RatingRubric:         map[string]map[string]string{"default": {"1": "poor", "5": "great"}},
		RubricDetail:         &registry.RubricDetail{Scale: &registry.RubricScale{Min: 1, Max: 5}},
	}
}

// rubricBrandTemplate is spec §6.1's source template (kind: rubric, 2 groups /
// 3 criterion-pairs, RatingRubric on clarity only, Likert [1,5]).
func rubricBrandTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:           "acme/rubric-brand",
		Kind:         registry.KindRubric,
		Modalities:   []registry.Modality{registry.ModalityText},
		RubricGroups: map[string][]string{"clarity": {"clear", "concise"}, "tone": {"on-brand"}},
		RatingRubric: map[string]map[string]string{"clarity": {"1": "poor", "5": "great"}},
		RubricDetail: &registry.RubricDetail{Scale: &registry.RubricScale{Min: 1, Max: 5}},
	}
}

func TestGoldenPointwiseWireBytes(t *testing.T) {
	res, err := staxexport.Export(pointwiseQualityTemplate(), staxexport.Options{ModelID: "model-123"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got, err := marshalEvaluators(res.Evaluators)
	if err != nil {
		t.Fatalf("marshalEvaluators: %v", err)
	}
	if string(got) != wantPointwiseGolden {
		t.Errorf("pointwise wire bytes drifted from spec §6.2.\n--- got ---\n%s\n--- want ---\n%s", got, wantPointwiseGolden)
	}
}

func TestGoldenRubricFanoutWireBytes(t *testing.T) {
	res, err := staxexport.Export(rubricBrandTemplate(), staxexport.Options{ModelID: "model-123"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got, err := marshalEvaluators(res.Evaluators)
	if err != nil {
		t.Fatalf("marshalEvaluators: %v", err)
	}
	if string(got) != wantRubricFanoutGolden {
		t.Errorf("rubric fan-out wire bytes drifted from spec §6.1 Option B.\n--- got ---\n%s\n--- want ---\n%s", got, wantRubricFanoutGolden)
	}
}

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

package registry

// adaptive_support_test.go covers the two exported wrappers Phase 1 adaptive
// rubrics (PR #52) added so cmd/* frontends can (a) write a draft KindRubric
// template and (b) validate a user-supplied template id through the SAME single
// source of truth the ingest boundary uses:
//
//   - MarshalTemplate  (codec.go)  — the seam-friendly draft writer
//   - ValidateTemplateID (validate.go) — the exported id guard used by
//     `rubric generate --id` and `eval adaptive --save-as`
//
// The critical invariant for CUJ 7/CUJ 8 is that a marshaled draft ROUND-TRIPS
// losslessly back through the codec (so the human's reviewed draft imports and
// re-runs unchanged), and that the exported id guard behaves identically to the
// internal one.

import (
	"reflect"
	"testing"
)

// adaptiveDraft mirrors the shape cmd/mizan draftRubricTemplate produces: a plain
// KindRubric template (no provenance field — Phase 1 boundary) with RubricGroups
// in declared order.
func adaptiveDraft() MetricTemplate {
	return MetricTemplate{
		ID:          "acme/quality",
		Name:        "Adaptive rubric: general_quality",
		Description: "Adaptive-generated rubric (authoring aid).",
		Version:     "0.1.0",
		Kind:        KindRubric,
		Modalities:  []Modality{ModalityText},
		Inputs: []InputSpec{
			{Name: "prompt", Modality: ModalityText, Required: true},
			{Name: "response", Modality: ModalityText, Required: true},
		},
		MetricPromptTemplate: "Assess the response.\n\nprompt:\n{{prompt}}\n\nresponse:\n{{response}}",
		RubricGroups: map[string][]string{
			"general_quality": {"Answers the question directly", "Is free of jargon"},
		},
	}
}

// TestMarshalTemplateRoundTrip proves a marshaled adaptive draft round-trips
// losslessly through the codec — the guarantee that a reviewed `rubric generate`
// draft imports and re-runs as an ordinary reproducible static rubric.
func TestMarshalTemplateRoundTrip(t *testing.T) {
	orig := adaptiveDraft()

	data, err := MarshalTemplate(&orig)
	if err != nil {
		t.Fatalf("MarshalTemplate: %v", err)
	}

	got, err := NewYAMLCodec().Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal of marshaled draft: %v", err)
	}

	if got.ID != orig.ID || got.Kind != orig.Kind || got.Version != orig.Version {
		t.Errorf("metadata drift: got id=%q kind=%q version=%q", got.ID, got.Kind, got.Version)
	}
	// Declared order of the criteria must survive the round-trip (a tested invariant).
	if !reflect.DeepEqual(got.RubricGroups, orig.RubricGroups) {
		t.Errorf("RubricGroups drift after round-trip:\n got  %#v\n want %#v", got.RubricGroups, orig.RubricGroups)
	}
	if got.MetricPromptTemplate != orig.MetricPromptTemplate {
		t.Errorf("metricPromptTemplate drift:\n got  %q\n want %q", got.MetricPromptTemplate, orig.MetricPromptTemplate)
	}
}

// TestExportedValidateTemplateIDMatchesInternal proves the exported wrapper is a
// faithful pass-through of the single-source id guard, so `--id`/`--save-as`
// accept exactly what the ingest boundary accepts (no divergent second guard).
func TestExportedValidateTemplateIDMatchesInternal(t *testing.T) {
	cases := []string{
		"acme/quality",
		"demo/four-sentence",
		"NotValid",
		"no-slash",
		"trailing/",
		"/leading",
		"UPPER/case",
		"",
		"a/b/c",
	}
	for _, id := range cases {
		gotExported := ValidateTemplateID(id) != nil
		gotInternal := validateTemplateID(id) != nil
		if gotExported != gotInternal {
			t.Errorf("ValidateTemplateID(%q) err=%v but validateTemplateID err=%v", id, gotExported, gotInternal)
		}
	}
}

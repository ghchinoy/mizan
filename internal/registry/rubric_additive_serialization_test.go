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

// rubric_additive_serialization_test.go covers ITEM H target 5 (subset-level
// serialization back-compat, RFC-0001 §4.4). The new fields (RatingRubric,
// RubricDetail) are OPTIONAL and carry `omitempty` struct tags. This proves, at
// the struct level, that:
//
//   - a template declaring NONE of the new fields serializes IDENTICALLY to
//     before — the new keys are omitted entirely (back-compat: no spurious keys);
//   - a template that DOES declare them round-trips losslessly.
//
// This is NOT a contentHash / canonical-codec test (that infra is deferred to
// P2.1). It exercises only encoding/json against the struct tags added in this
// PR, which is standard-library and needs none of the deferred codec.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// baseTemplateNoNewFields is a minimal rubric template that predates ITEM H: it
// declares none of the new optional fields.
func baseTemplateNoNewFields() MetricTemplate {
	return MetricTemplate{
		ID:                   "test/clarity",
		Name:                 "Clarity",
		Kind:                 KindRubric,
		Modalities:           []Modality{ModalityText},
		MetricPromptTemplate: "Evaluate this ad copy: {{copy}}",
		SystemInstruction:    "Be strict.",
		AutoraterModel:       "gemini-2.5-flash",
		RubricGroups: map[string][]string{
			"clarity": {"The message is unambiguous"},
		},
	}
}

// TestOmitEmptyOmitsNewFieldsWhenAbsent proves a template with none of the ITEM H
// fields emits neither `ratingRubric` nor `rubricDetail` keys — the JSON is
// exactly what it was before the fields existed (back-compat).
func TestOmitEmptyOmitsNewFieldsWhenAbsent(t *testing.T) {
	b, err := json.Marshal(baseTemplateNoNewFields())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	out := string(b)
	for _, key := range []string{"ratingRubric", "rubricDetail", "scale"} {
		if strings.Contains(out, key) {
			t.Errorf("omitempty violated: absent field emitted key %q in JSON:\n%s", key, out)
		}
	}
}

// TestNewFieldsRoundTripLossless proves that when the new fields ARE declared,
// they marshal and unmarshal back to an equal value (nothing is dropped or
// mangled).
func TestNewFieldsRoundTripLossless(t *testing.T) {
	tmpl := baseTemplateNoNewFields()
	tmpl.RatingRubric = map[string]map[string]string{
		"clarity": {"1": "incomprehensible", "5": "completely clear"},
	}
	tmpl.RubricDetail = &RubricDetail{Scale: &RubricScale{Min: 2, Max: 8}}

	b, err := json.Marshal(tmpl)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// The keys must now be present.
	out := string(b)
	for _, key := range []string{"ratingRubric", "rubricDetail", "scale"} {
		if !strings.Contains(out, key) {
			t.Errorf("declared field did not serialize key %q:\n%s", key, out)
		}
	}

	var got MetricTemplate
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got.RatingRubric, tmpl.RatingRubric) {
		t.Errorf("RatingRubric round-trip: got %v, want %v", got.RatingRubric, tmpl.RatingRubric)
	}
	if !reflect.DeepEqual(got.RubricDetail, tmpl.RubricDetail) {
		t.Errorf("RubricDetail round-trip: got %+v, want %+v", got.RubricDetail, tmpl.RubricDetail)
	}
}

// TestNilRubricDetailDistinctFromEmpty pins the "not declared" vs "declared
// empty" distinction the model relies on: a nil *RubricDetail is omitted, while a
// non-nil one with a nil Scale still serializes the object (so absence stays
// meaningful — nil means "behave exactly as before").
func TestNilRubricDetailDistinctFromEmpty(t *testing.T) {
	// nil -> omitted entirely.
	nilB, err := json.Marshal(baseTemplateNoNewFields())
	if err != nil {
		t.Fatalf("json.Marshal(nil detail): %v", err)
	}
	if strings.Contains(string(nilB), "rubricDetail") {
		t.Errorf("nil RubricDetail should be omitted, got:\n%s", nilB)
	}

	// non-nil with nil Scale -> object present, scale omitted.
	tmpl := baseTemplateNoNewFields()
	tmpl.RubricDetail = &RubricDetail{}
	nonNilB, err := json.Marshal(tmpl)
	if err != nil {
		t.Fatalf("json.Marshal(empty detail): %v", err)
	}
	if !strings.Contains(string(nonNilB), "rubricDetail") {
		t.Errorf("non-nil RubricDetail should serialize, got:\n%s", nonNilB)
	}
	if strings.Contains(string(nonNilB), "scale") {
		t.Errorf("nil Scale inside RubricDetail should be omitted, got:\n%s", nonNilB)
	}
}

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

import (
	"reflect"
	"testing"
)

// TestInferInputsBasic: placeholders become text/required=true inputs, in
// first-seen order, deduplicated (design D2/D4).
func TestInferInputsBasic(t *testing.T) {
	got := InferInputs(nil, "Compare {{question}} with {{answer}}, then reconsider {{question}}.")
	want := []InputSpec{
		{Name: "question", Modality: ModalityText, Required: true},
		{Name: "answer", Modality: ModalityText, Required: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InferInputs = %#v, want %#v (first-seen order, deduped)", got, want)
	}
}

// TestInferInputsExcludesReserved: the reserved {{response}} token is never
// inferred (design D3).
func TestInferInputsExcludesReserved(t *testing.T) {
	got := InferInputs(nil, "Rate {{response}} against the {{criteria}}.")
	want := []InputSpec{{Name: "criteria", Modality: ModalityText, Required: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InferInputs = %#v, want only {{criteria}} ({{response}} reserved)", got)
	}
	for _, in := range got {
		if in.Name == "response" {
			t.Fatalf("reserved {{response}} was inferred: %#v", got)
		}
	}
}

// TestInferInputsSkipsDeclared: a placeholder already declared (by name) is not
// duplicated; inference is additive over the explicit set (design D5).
func TestInferInputsSkipsDeclared(t *testing.T) {
	declared := []InputSpec{{Name: "foo", Modality: ModalityImage, Required: false}}
	got := InferInputs(declared, "Look at {{foo}} and describe {{bar}}.")
	want := []InputSpec{{Name: "bar", Modality: ModalityText, Required: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InferInputs = %#v, want only the undeclared {{bar}} (foo already declared)", got)
	}
}

// TestInferInputsWhitespaceAndAdjacentBraces: whitespace inside braces is
// tolerated; single/adjacent-brace noise is not treated as a placeholder; a
// repeated placeholder is deduped (design D2 test matrix row).
func TestInferInputsWhitespaceAndAdjacentBraces(t *testing.T) {
	text := "A {{ spaced }} token, a bare {notplaceholder}, and {{spaced}} again; literal {{{weird}}}."
	got := InferInputs(nil, text)
	names := make([]string, len(got))
	for i, in := range got {
		names[i] = in.Name
	}
	// "spaced" appears (deduped once); the single-brace {notplaceholder} is NOT a
	// placeholder. The triple-brace {{{weird}}} still contains a valid {{weird}}
	// match, so "weird" is a legitimate placeholder name.
	want := []string{"spaced", "weird"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("inferred names = %v, want %v", names, want)
	}
}

// TestInferInputsAcrossFields: placeholders from a second text field (e.g. the
// system instruction) are inferred too, deduped against the first, preserving
// first-seen order across fields.
func TestInferInputsAcrossFields(t *testing.T) {
	got := InferInputs(nil, "Prompt uses {{a}} and {{b}}.", "System mentions {{b}} and {{c}}.")
	names := make([]string, len(got))
	for i, in := range got {
		names[i] = in.Name
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("inferred names = %v, want %v (deduped across fields, first-seen order)", names, want)
	}
}

// TestInferInputsNoPlaceholders: text with no placeholders yields nothing.
func TestInferInputsNoPlaceholders(t *testing.T) {
	if got := InferInputs(nil, "No placeholders here at all."); len(got) != 0 {
		t.Fatalf("InferInputs = %#v, want none", got)
	}
}

// TestIsReservedInputName: the reserved-token accessor matches the documented set.
func TestIsReservedInputName(t *testing.T) {
	if !IsReservedInputName("response") {
		t.Error(`IsReservedInputName("response") = false, want true`)
	}
	if IsReservedInputName("question") {
		t.Error(`IsReservedInputName("question") = true, want false`)
	}
}

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

package staxexport

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// rubricBrandFixture loads the real golden rubric fixture the finding + spec
// worked example are built on, so the test exercises the exact shape the
// contract was measured against (2 groups, 3 criterion-pairs, Likert [1,5],
// RatingRubric on clarity only).
func rubricBrandFixture(t *testing.T) registry.MetricTemplate {
	t.Helper()
	path := filepath.Join("..", "registry", "testdata", "golden", "templates", "rubric-brand.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tmpl, err := registry.NewYAMLCodec().Unmarshal(data)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return *tmpl
}

// userText returns the USER prompt body of an evaluator (the last prompt; the
// SYSTEM prompt, when present, is first).
func userText(ev Evaluator) string {
	return ev.Prompts[len(ev.Prompts)-1].Text
}

// systemText returns the SYSTEM prompt body, or "" when the evaluator has none.
func systemText(ev Evaluator) string {
	if len(ev.Prompts) > 0 && ev.Prompts[0].Role == roleSystem {
		return ev.Prompts[0].Text
	}
	return ""
}

// TestRubricFanoutOptionB is the load-bearing case: the default Option B fans a
// rubric template out to one evaluator per (group, criterion) pair, names them
// via the id::group::criterion convention, and preserves per-criterion
// categories (finding decision 1+4, spec §5/§6).
func TestRubricFanoutOptionB(t *testing.T) {
	tmpl := rubricBrandFixture(t)

	res, err := Export(tmpl, Options{ModelID: "model-123"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Option B with a model id should emit no warnings, got %v", res.Warnings)
	}
	if len(res.Evaluators) != 3 {
		t.Fatalf("got %d evaluators, want 3 (one per criterion-pair)", len(res.Evaluators))
	}

	// N criterion-pairs -> N evaluators, names follow id::group::criterion, in
	// deterministic (sorted group, declared criterion) order.
	gotNames := []string{res.Evaluators[0].Name, res.Evaluators[1].Name, res.Evaluators[2].Name}
	wantNames := []string{
		"acme/rubric-brand::clarity::clear",
		"acme/rubric-brand::clarity::concise",
		"acme/rubric-brand::tone::on-brand",
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("evaluator names = %v, want %v", gotNames, wantNames)
	}

	// Real-DTO invariants: choices output, a bound model id, a single USER prompt.
	ev0 := res.Evaluators[0]
	if ev0.OutputFormatType != "Choices" {
		t.Errorf("output_format_type = %q, want Choices", ev0.OutputFormatType)
	}
	if ev0.ModelID != "model-123" {
		t.Errorf("model_id = %q, want model-123", ev0.ModelID)
	}
	if len(ev0.Prompts) != 1 || ev0.Prompts[0].Role != roleUser {
		t.Errorf("prompts = %+v, want a single USER prompt", ev0.Prompts)
	}
	if got, want := userText(ev0),
		`Evaluate {{output}} on the criterion "clear" (rubric group: clarity). Return ONE category.`; got != want {
		t.Errorf("clear prompt = %q, want %q", got, want)
	}

	// clarity group carries the 1:poor / 5:great anchors into BOTH clarity
	// criteria; interior bands are generic score-N (per-criterion categories
	// preserved), and value is a String in the real DTO.
	wantClarity := []OutputCategory{{"1-poor", "1"}, {"score-2", "2"}, {"score-3", "3"}, {"score-4", "4"}, {"5-great", "5"}}
	for _, i := range []int{0, 1} {
		if !reflect.DeepEqual(res.Evaluators[i].OutputCategories, wantClarity) {
			t.Errorf("%s categories = %v, want %v", res.Evaluators[i].Name, res.Evaluators[i].OutputCategories, wantClarity)
		}
	}
	// tone has no RatingRubric -> all generic.
	wantTone := []OutputCategory{{"score-1", "1"}, {"score-2", "2"}, {"score-3", "3"}, {"score-4", "4"}, {"score-5", "5"}}
	if !reflect.DeepEqual(res.Evaluators[2].OutputCategories, wantTone) {
		t.Errorf("on-brand categories = %v, want %v", res.Evaluators[2].OutputCategories, wantTone)
	}
}

// TestRubricFlattenOptionA verifies the opt-in flatten emits exactly one
// evaluator with aggregate generic bands AND the required dropped-granularity
// warning (spec §5 Option A, decision 2).
func TestRubricFlattenOptionA(t *testing.T) {
	tmpl := rubricBrandFixture(t)

	res, err := Export(tmpl, Options{Flatten: true, ModelID: "model-123"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(res.Evaluators) != 1 {
		t.Fatalf("flatten got %d evaluators, want 1", len(res.Evaluators))
	}
	ev := res.Evaluators[0]
	if ev.Name != "acme/rubric-brand (flattened)" {
		t.Errorf("flatten name = %q, want %q", ev.Name, "acme/rubric-brand (flattened)")
	}
	wantText := "Evaluate {{output}} against the following rubric and return ONE overall category.\n" +
		"- [clarity] clear\n- [clarity] concise\n- [tone] on-brand\n"
	if userText(ev) != wantText {
		t.Errorf("flatten text = %q, want %q", userText(ev), wantText)
	}
	wantCats := []OutputCategory{{"score-1", "1"}, {"score-2", "2"}, {"score-3", "3"}, {"score-4", "4"}, {"score-5", "5"}}
	if !reflect.DeepEqual(ev.OutputCategories, wantCats) {
		t.Errorf("flatten categories = %v, want %v (aggregate generic, anchors dropped)", ev.OutputCategories, wantCats)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "dropped per-criterion granularity") {
		t.Errorf("flatten warnings = %v, want one 'dropped per-criterion granularity' warning", res.Warnings)
	}
}

// TestPointwiseDirectMap verifies pointwise maps to a single evaluator whose
// output_categories come from the RatingRubric bands over the resolved scale
// (decision 3), with the body placeholders renamed and the system instruction
// carried as a SYSTEM prompt.
func TestPointwiseDirectMap(t *testing.T) {
	tmpl := registry.MetricTemplate{
		ID:                   "acme/helpfulness",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Rate {{response}} for the task {{question}}.",
		SystemInstruction:    "You are a careful judge.",
		RatingRubric:         map[string]map[string]string{"default": {"1": "poor", "3": "ok", "5": "excellent"}},
	}

	res, err := Export(tmpl, Options{ModelID: "model-123"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(res.Evaluators) != 1 {
		t.Fatalf("pointwise got %d evaluators, want 1", len(res.Evaluators))
	}
	ev := res.Evaluators[0]
	if ev.Name != "acme/helpfulness" {
		t.Errorf("name = %q, want acme/helpfulness", ev.Name)
	}
	// response -> output, question -> prompt.
	if got, want := userText(ev), "Rate {{output}} for the task {{prompt}}."; got != want {
		t.Errorf("USER prompt = %q, want %q", got, want)
	}
	// system instruction becomes a SYSTEM prompt (first in the list).
	if got := systemText(ev); got != "You are a careful judge." {
		t.Errorf("SYSTEM prompt = %q, want the system instruction carried through", got)
	}
	if len(ev.Prompts) != 2 || ev.Prompts[0].Role != roleSystem || ev.Prompts[1].Role != roleUser {
		t.Errorf("prompts = %+v, want [SYSTEM, USER]", ev.Prompts)
	}
	// variables list the reserved vars the prompt uses, sorted, required.
	wantVars := []Variable{{"output", true}, {"prompt", true}}
	if !reflect.DeepEqual(ev.Variables, wantVars) {
		t.Errorf("variables = %v, want %v", ev.Variables, wantVars)
	}
	want := []OutputCategory{{"1-poor", "1"}, {"score-2", "2"}, {"3-ok", "3"}, {"score-4", "4"}, {"5-excellent", "5"}}
	if !reflect.DeepEqual(ev.OutputCategories, want) {
		t.Errorf("categories = %v, want %v", ev.OutputCategories, want)
	}
}

// TestEmptyModelIDWarns verifies that omitting the model id still exports (the
// field is emitted as "") but surfaces a warning, since Stax requires model_id
// and Mizan never migrates model bindings (N3).
func TestEmptyModelIDWarns(t *testing.T) {
	tmpl := registry.MetricTemplate{
		ID:                   "acme/helpfulness",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Rate {{response}}.",
	}
	res, err := Export(tmpl, Options{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Evaluators[0].ModelID != "" {
		t.Errorf("model_id = %q, want empty", res.Evaluators[0].ModelID)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "model_id is empty") {
		t.Errorf("warnings = %v, want one 'model_id is empty' warning", res.Warnings)
	}
}

// TestCategoryDerivationRule pins the EXPLICIT rule (review decision 2): anchored
// bands use {band}-{desc}; interior/un-anchored bands use score-{band}; value is
// the stringified band; order is ascending.
func TestCategoryDerivationRule(t *testing.T) {
	// Anchored 1 and 5; interior 2,3,4 fall back to generic.
	got := categoriesForScale(1, 5, map[string]string{"1": "poor", "5": "great"})
	want := []OutputCategory{{"1-poor", "1"}, {"score-2", "2"}, {"score-3", "3"}, {"score-4", "4"}, {"5-great", "5"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("anchored+interior derivation = %v, want %v", got, want)
	}

	// No band descriptions -> every band generic.
	got = categoriesForScale(1, 3, nil)
	want = []OutputCategory{{"score-1", "1"}, {"score-2", "2"}, {"score-3", "3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("all-generic derivation = %v, want %v", got, want)
	}

	// Multi-word descriptions slugify.
	got = categoriesForScale(1, 2, map[string]string{"2": "Very Good!"})
	if len(got) != 2 || got[1].Name != "2-very-good" || got[1].Value != "2" {
		t.Errorf("slugified category wrong: %v", got)
	}
}

// TestFailClosedUnsupported verifies pairwise, custom_schema, and non-text
// modalities fail closed with a clear unsupported error and emit nothing
// (decision 5).
func TestFailClosedUnsupported(t *testing.T) {
	cases := []struct {
		name string
		tmpl registry.MetricTemplate
	}{
		{"pairwise", registry.MetricTemplate{ID: "x/p", Kind: registry.KindPairwise, Modalities: []registry.Modality{registry.ModalityText}}},
		{"custom_schema", registry.MetricTemplate{ID: "x/c", Kind: registry.KindCustomSchema, Modalities: []registry.Modality{registry.ModalityText}}},
		{"non-text", registry.MetricTemplate{ID: "x/i", Kind: registry.KindPointwise, Modalities: []registry.Modality{registry.ModalityImage}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Export(tc.tmpl, Options{})
			if err == nil {
				t.Fatalf("expected unsupported error, got nil")
			}
			if !strings.Contains(err.Error(), "unsupported in v1") {
				t.Errorf("error = %v, want it to say 'unsupported in v1'", err)
			}
			if len(res.Evaluators) != 0 {
				t.Errorf("nothing should be emitted on failure, got %d evaluators", len(res.Evaluators))
			}
		})
	}
}

// TestUnmappedPlaceholderHardError verifies a placeholder that maps to no Stax
// reserved var is a hard error (spec §4 rule 2, decision 6).
func TestUnmappedPlaceholderHardError(t *testing.T) {
	tmpl := registry.MetricTemplate{
		ID:                   "acme/weird",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Rate {{response}} using {{unmappable_field}}.",
	}
	_, err := Export(tmpl, Options{})
	if err == nil {
		t.Fatal("expected hard error for unmapped placeholder, got nil")
	}
	if !strings.Contains(err.Error(), "unmappable_field") || !strings.Contains(err.Error(), "no Stax reserved var") {
		t.Errorf("error = %v, want it to name the unmapped field", err)
	}

	// The override table (spec §4 rule 3) lets it export.
	res, err := Export(tmpl, Options{PlaceholderOverride: map[string]string{"unmappable_field": "expected_output"}, ModelID: "m"})
	if err != nil {
		t.Fatalf("override should allow export: %v", err)
	}
	if got := userText(res.Evaluators[0]); !strings.Contains(got, "{{expected_output}}") {
		t.Errorf("override not applied: %q", got)
	}
}

// TestAliasCollisionHardError verifies two distinct Mizan fields mapping to the
// same Stax reserved var fail closed (review decision 1).
func TestAliasCollisionHardError(t *testing.T) {
	tmpl := registry.MetricTemplate{
		ID:                   "acme/collide",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Compare {{output}} and {{answer}}.", // both -> {{output}}
	}
	_, err := Export(tmpl, Options{})
	if err == nil {
		t.Fatal("expected hard error for alias collision, got nil")
	}
	if !strings.Contains(err.Error(), "collision") || !strings.Contains(err.Error(), "output") {
		t.Errorf("error = %v, want it to describe the {{output}} collision", err)
	}
}

// TestScaleFromRubricDetail verifies a template's declared Likert scale drives
// the category count, and an inverted scale fails closed.
func TestScaleFromRubricDetail(t *testing.T) {
	tmpl := registry.MetricTemplate{
		ID:           "acme/scaled",
		Kind:         registry.KindRubric,
		Modalities:   []registry.Modality{registry.ModalityText},
		RubricGroups: map[string][]string{"q": {"c1"}},
		RubricDetail: &registry.RubricDetail{Scale: &registry.RubricScale{Min: 1, Max: 3}},
	}
	res, err := Export(tmpl, Options{ModelID: "m"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if n := len(res.Evaluators[0].OutputCategories); n != 3 {
		t.Errorf("got %d categories for scale [1,3], want 3", n)
	}

	tmpl.RubricDetail.Scale = &registry.RubricScale{Min: 5, Max: 1}
	if _, err := Export(tmpl, Options{}); err == nil {
		t.Error("expected error for inverted scale, got nil")
	}
}

// TestExportDeterministic guards that repeated exports produce identical,
// order-stable output (map-backed groups are sorted) — required for a
// git-reviewable artifact.
func TestExportDeterministic(t *testing.T) {
	tmpl := rubricBrandFixture(t)
	first, err := Export(tmpl, Options{ModelID: "m"})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for i := 0; i < 5; i++ {
		next, err := Export(tmpl, Options{ModelID: "m"})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		if !reflect.DeepEqual(first.Evaluators, next.Evaluators) {
			t.Fatalf("non-deterministic export at iter %d", i)
		}
	}
	// sanity: names are the sorted fan-out set.
	var names []string
	for _, e := range first.Evaluators {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	want := []string{
		"acme/rubric-brand::clarity::clear",
		"acme/rubric-brand::clarity::concise",
		"acme/rubric-brand::tone::on-brand",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

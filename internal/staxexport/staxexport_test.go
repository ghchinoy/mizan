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

// TestRubricFanoutOptionB is the load-bearing case: the default Option B fans a
// rubric template out to one evaluator per (group, criterion) pair, names them
// via the id::group::criterion convention, and preserves per-criterion
// categories (finding decision 1+4, spec §5/§6).
func TestRubricFanoutOptionB(t *testing.T) {
	tmpl := rubricBrandFixture(t)

	res, err := Export(tmpl, Options{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Option B should emit no warnings, got %v", res.Warnings)
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

	// Per-criterion prompt embeds {{output}} and names the criterion + group.
	if got, want := res.Evaluators[0].Prompt.Content,
		`Evaluate {{output}} on the criterion "clear" (rubric group: clarity). Return ONE category.`; got != want {
		t.Errorf("clear prompt = %q, want %q", got, want)
	}

	// clarity group carries the 1:poor / 5:great anchors into BOTH clarity
	// criteria; interior bands are generic score-N (per-criterion categories
	// preserved).
	wantClarity := map[string]int{"1-poor": 1, "score-2": 2, "score-3": 3, "score-4": 4, "5-great": 5}
	for _, i := range []int{0, 1} {
		if !reflect.DeepEqual(res.Evaluators[i].OutputCategories, wantClarity) {
			t.Errorf("%s categories = %v, want %v", res.Evaluators[i].Name, res.Evaluators[i].OutputCategories, wantClarity)
		}
	}
	// tone has no RatingRubric -> all generic.
	wantTone := map[string]int{"score-1": 1, "score-2": 2, "score-3": 3, "score-4": 4, "score-5": 5}
	if !reflect.DeepEqual(res.Evaluators[2].OutputCategories, wantTone) {
		t.Errorf("on-brand categories = %v, want %v", res.Evaluators[2].OutputCategories, wantTone)
	}
}

// TestRubricFlattenOptionA verifies the opt-in flatten emits exactly one
// evaluator with aggregate generic bands AND the required dropped-granularity
// warning (spec §5 Option A, decision 2).
func TestRubricFlattenOptionA(t *testing.T) {
	tmpl := rubricBrandFixture(t)

	res, err := Export(tmpl, Options{Flatten: true})
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
	wantContent := "Evaluate {{output}} against the following rubric and return ONE overall category.\n" +
		"- [clarity] clear\n- [clarity] concise\n- [tone] on-brand\n"
	if ev.Prompt.Content != wantContent {
		t.Errorf("flatten content = %q, want %q", ev.Prompt.Content, wantContent)
	}
	wantCats := map[string]int{"score-1": 1, "score-2": 2, "score-3": 3, "score-4": 4, "score-5": 5}
	if !reflect.DeepEqual(ev.OutputCategories, wantCats) {
		t.Errorf("flatten categories = %v, want %v (aggregate generic, anchors dropped)", ev.OutputCategories, wantCats)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "dropped per-criterion granularity") {
		t.Errorf("flatten warnings = %v, want one 'dropped per-criterion granularity' warning", res.Warnings)
	}
}

// TestPointwiseDirectMap verifies pointwise maps to a single evaluator whose
// output_categories come from the RatingRubric bands over the resolved scale
// (decision 3), with the body placeholders renamed.
func TestPointwiseDirectMap(t *testing.T) {
	tmpl := registry.MetricTemplate{
		ID:                   "acme/helpfulness",
		Kind:                 registry.KindPointwise,
		Modalities:           []registry.Modality{registry.ModalityText},
		MetricPromptTemplate: "Rate {{response}} for the task {{question}}.",
		SystemInstruction:    "You are a careful judge.",
		RatingRubric:         map[string]map[string]string{"default": {"1": "poor", "3": "ok", "5": "excellent"}},
	}

	res, err := Export(tmpl, Options{})
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
	if got, want := ev.Prompt.Content, "Rate {{output}} for the task {{prompt}}."; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if ev.System == nil || ev.System.Content != "You are a careful judge." {
		t.Errorf("system = %+v, want the system instruction carried through", ev.System)
	}
	want := map[string]int{"1-poor": 1, "score-2": 2, "3-ok": 3, "score-4": 4, "5-excellent": 5}
	if !reflect.DeepEqual(ev.OutputCategories, want) {
		t.Errorf("categories = %v, want %v", ev.OutputCategories, want)
	}
}

// TestCategoryDerivationRule pins the EXPLICIT rule (PR-#87 decision 2): anchored
// bands use {band}-{desc}; interior/un-anchored bands use score-{band}.
func TestCategoryDerivationRule(t *testing.T) {
	// Anchored 1 and 5; interior 2,3,4 fall back to generic.
	got := categoriesForScale(1, 5, map[string]string{"1": "poor", "5": "great"})
	want := map[string]int{"1-poor": 1, "score-2": 2, "score-3": 3, "score-4": 4, "5-great": 5}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("anchored+interior derivation = %v, want %v", got, want)
	}

	// No band descriptions -> every band generic.
	got = categoriesForScale(1, 3, nil)
	want = map[string]int{"score-1": 1, "score-2": 2, "score-3": 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("all-generic derivation = %v, want %v", got, want)
	}

	// Multi-word descriptions slugify.
	got = categoriesForScale(1, 2, map[string]string{"2": "Very Good!"})
	if got["2-very-good"] != 2 {
		t.Errorf("slugified category missing: %v", got)
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
	res, err := Export(tmpl, Options{PlaceholderOverride: map[string]string{"unmappable_field": "expected_output"}})
	if err != nil {
		t.Fatalf("override should allow export: %v", err)
	}
	if got := res.Evaluators[0].Prompt.Content; !strings.Contains(got, "{{expected_output}}") {
		t.Errorf("override not applied: %q", got)
	}
}

// TestAliasCollisionHardError verifies two distinct Mizan fields mapping to the
// same Stax reserved var fail closed (PR-#87 decision 1).
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
	res, err := Export(tmpl, Options{})
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
	first, err := Export(tmpl, Options{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for i := 0; i < 5; i++ {
		next, err := Export(tmpl, Options{})
		if err != nil {
			t.Fatalf("Export: %v", err)
		}
		var a, b []string
		for _, e := range first.Evaluators {
			a = append(a, e.Name)
		}
		for _, e := range next.Evaluators {
			b = append(b, e.Name)
		}
		sort.Strings(a)
		sort.Strings(b)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("non-deterministic evaluator set: %v vs %v", a, b)
		}
	}
}

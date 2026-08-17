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

// rubric_union_test.go covers the union-before-freeze slice (CUJ 9, design §6.3/
// §6.5): the repeatable --add-criterion flag, conservative exact-after-normalization
// dedup (Decision 3b), per-criterion Origin provenance (Option A), and the
// byte-identical single-pass regression (CUJ 7/8 unbroken).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/rubricgen"
	"github.com/ghchinoy/mizan/internal/rubricgen/rubricgentest"
)

func TestValidateCriterion(t *testing.T) {
	// Ordinary prose criteria (apostrophes, commas, colons, slashes) are accepted —
	// the group-name key allow-list is intentionally NOT reused verbatim.
	for _, ok := range []string{
		"The image uses the brand's blue-and-white palette.",
		"The brand logo is in the bottom-right corner.",
		"Answers, clearly: yes/no (with reasons).",
	} {
		if err := validateCriterion(ok); err != nil {
			t.Errorf("validateCriterion(%q) = %v, want nil", ok, err)
		}
	}
	// Empty/blank, control characters, and terminal-escape bytes are rejected.
	for _, bad := range []string{"", "   ", "has\nnewline", "tab\there", "esc\x1b[31mred"} {
		if err := validateCriterion(bad); err == nil {
			t.Errorf("validateCriterion(%q) = nil, want error", bad)
		}
	}
	if err := validateCriterion(strings.Repeat("a", maxCriterionLen+1)); err == nil {
		t.Error("over-length criterion should be rejected (CWE-770 bound)")
	}
}

func TestNormalizeCriterion(t *testing.T) {
	cases := map[string]string{
		"  Hello   World  ":        "hello world",
		"HELLO world":              "hello world",
		"hello\tworld":             "hello world",
		"Answers the QUESTION  ok": "answers the question ok",
	}
	for in, want := range cases {
		if got := normalizeCriterion(in); got != want {
			t.Errorf("normalizeCriterion(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUnionCriteriaOrderOriginAndDedup pins the union assembler: generated first
// (declared order), hand-authored after (flag order); conservative-exact dedup
// across the full list (first occurrence wins), drops reported to stderr; each
// survivor keeps its Origin and (for generated) its Type/Importance.
func TestUnionCriteriaOrderOriginAndDedup(t *testing.T) {
	generated := rubricgen.UsableRubrics([]rubricgen.Rubric{
		rubricgentest.Rubric("Answers the question directly", "CONTENT", "HIGH"),
		rubricgentest.Rubric("Is free of jargon", "STYLE", "MEDIUM"),
	})
	hand := []string{
		"Uses the brand palette",          // new hand-authored
		"answers the QUESTION   directly", // normalized-dup of generated[0] -> dropped
		"Uses the brand palette",          // exact dup of prior hand-authored -> dropped
	}
	var stderr bytes.Buffer
	out := unionCriteria(generated, hand, &stderr)

	wantCriteria := []string{"Answers the question directly", "Is free of jargon", "Uses the brand palette"}
	wantOrigin := []string{registry.OriginAdaptiveGenerated, registry.OriginAdaptiveGenerated, registry.OriginHandAuthored}
	if len(out) != len(wantCriteria) {
		t.Fatalf("union len = %d, want %d: %+v", len(out), len(wantCriteria), out)
	}
	for i := range wantCriteria {
		if out[i].Criterion != wantCriteria[i] {
			t.Errorf("union[%d].Criterion = %q, want %q", i, out[i].Criterion, wantCriteria[i])
		}
		if out[i].Origin != wantOrigin[i] {
			t.Errorf("union[%d].Origin = %q, want %q", i, out[i].Origin, wantOrigin[i])
		}
	}
	// Generated records preserve Type/Importance; hand-authored have none.
	if out[0].Type != "CONTENT" || out[0].Importance != "HIGH" {
		t.Errorf("generated record lost type/importance: %+v", out[0])
	}
	if out[2].Type != "" || out[2].Importance != "" {
		t.Errorf("hand-authored record should have empty type/importance: %+v", out[2])
	}
	// Two duplicates were dropped and reported.
	if n := strings.Count(stderr.String(), "dropped duplicate"); n != 2 {
		t.Errorf("stderr drop reports = %d, want 2:\n%s", n, stderr.String())
	}
}

// TestRubricGenerateUnionDraft is the CUJ 9 acceptance-criteria 1/2 end-to-end:
// generated criteria followed by hand-authored ones in order, duplicate dropped +
// reported, per-criterion Origin 1:1 with the criteria, Method unchanged, and the
// draft is strict-schema-valid.
func TestRubricGenerateUnionDraft(t *testing.T) {
	fake := &rubricgentest.FakeClient{}
	fake.PushRubrics([]rubricgen.Rubric{
		rubricgentest.Rubric("Answers the question directly", "CONTENT", "HIGH"),
		rubricgentest.Rubric("Is free of jargon", "STYLE", "MEDIUM"),
	})
	withFakeGenerator(t, fake)

	out := filepath.Join(t.TempDir(), "draft.yaml")
	cmd := newRubricGenerateCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"--sample", "Explain the offer",
		"--id", "acme/quality",
		"--out", out,
		"--add-criterion", "Uses the brand's blue-and-white palette.",
		"--add-criterion", "The brand logo is in the bottom-right corner.",
		"--add-criterion", "answers the QUESTION directly", // normalized-dup of generated[0] -> dropped
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !strings.Contains(stderr.String(), "dropped duplicate") {
		t.Errorf("expected a dropped-duplicate report on stderr:\n%s", stderr.String())
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	tmpl, err := registry.NewYAMLCodec().Unmarshal(data)
	if err != nil {
		t.Fatalf("draft does not round-trip through codec: %v", err)
	}

	wantCriteria := []string{
		"Answers the question directly",
		"Is free of jargon",
		"Uses the brand's blue-and-white palette.",
		"The brand logo is in the bottom-right corner.",
	}
	got := tmpl.RubricGroups["general_quality"]
	if strings.Join(got, "|") != strings.Join(wantCriteria, "|") {
		t.Errorf("draft criteria = %v, want %v (generated then hand-authored, dup dropped)", got, wantCriteria)
	}

	prov := tmpl.RubricProvenance
	if prov == nil {
		t.Fatal("union draft missing RubricProvenance")
	}
	// Decision 3c route i: Method is UNCHANGED (no new union marker).
	if prov.Method != adaptiveMethod {
		t.Errorf("provenance.Method = %q, want %q (unchanged, Decision 3c)", prov.Method, adaptiveMethod)
	}
	// RubricMeta is 1:1 with the criteria, each with its Origin.
	if len(prov.RubricMeta) != len(wantCriteria) {
		t.Fatalf("RubricMeta len = %d, want %d (1:1 with criteria)", len(prov.RubricMeta), len(wantCriteria))
	}
	wantOrigin := []string{
		registry.OriginAdaptiveGenerated,
		registry.OriginAdaptiveGenerated,
		registry.OriginHandAuthored,
		registry.OriginHandAuthored,
	}
	for i, m := range prov.RubricMeta {
		if m.Criterion != wantCriteria[i] {
			t.Errorf("RubricMeta[%d].Criterion = %q, want %q", i, m.Criterion, wantCriteria[i])
		}
		if m.Origin != wantOrigin[i] {
			t.Errorf("RubricMeta[%d].Origin = %q, want %q", i, m.Origin, wantOrigin[i])
		}
	}
	// Generated meta keeps type/importance; hand-authored is bare.
	if prov.RubricMeta[0].Type != "CONTENT" || prov.RubricMeta[0].Importance != "HIGH" {
		t.Errorf("generated RubricMeta[0] lost type/importance: %+v", prov.RubricMeta[0])
	}
	if prov.RubricMeta[2].Type != "" || prov.RubricMeta[2].Importance != "" {
		t.Errorf("hand-authored RubricMeta[2] should have empty type/importance: %+v", prov.RubricMeta[2])
	}

	// The union draft is strict-pack-schema valid (origin enum accepted).
	if err := registry.ValidateTemplateSchema(data); err != nil {
		t.Fatalf("union draft violated strict schema: %v\n%s", err, data)
	}

	// The ORIGIN column is shown to the author on stdout.
	if !strings.Contains(stdout.String(), "ORIGIN") ||
		!strings.Contains(stdout.String(), registry.OriginHandAuthored) {
		t.Errorf("stdout missing union origin table:\n%s", stdout.String())
	}
}

// TestRubricGenerateSinglePassNoOriginLeak is the CUJ 7/8 regression (acceptance
// criterion 5/11): with no --add-criterion the draft is byte-identical to today —
// no origin key is emitted and no RubricMeta carries an Origin. (A literal
// cross-run byte comparison is impossible because provenance stamps a wall-clock
// GeneratedAt; the single-pass path calls the SAME buildRubricProvenance builder as
// before and is fully pinned by TestRubricGenerateWritesDraft.)
func TestRubricGenerateSinglePassNoOriginLeak(t *testing.T) {
	fake := &rubricgentest.FakeClient{}
	fake.PushRubrics([]rubricgen.Rubric{
		rubricgentest.Rubric("Answers the question directly", "CONTENT", "HIGH"),
		rubricgentest.Rubric("Is free of jargon", "STYLE", "MEDIUM"),
	})
	withFakeGenerator(t, fake)

	out := filepath.Join(t.TempDir(), "draft.yaml")
	cmd := newRubricGenerateCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--sample", "Explain the offer",
		"--id", "acme/quality",
		"--out", out,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if strings.Contains(string(data), "origin:") {
		t.Errorf("single-pass draft leaked an origin key (not byte-identical to today):\n%s", data)
	}
	tmpl, err := registry.NewYAMLCodec().Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for i, m := range tmpl.RubricProvenance.RubricMeta {
		if m.Origin != "" {
			t.Errorf("single-pass RubricMeta[%d].Origin = %q, want empty", i, m.Origin)
		}
	}
}

// TestRubricGenerateAddCriterionValidatedBeforeGeneration proves a bad
// --add-criterion fails LOCALLY, before any (billable) generation call.
func TestRubricGenerateAddCriterionValidatedBeforeGeneration(t *testing.T) {
	fake := &rubricgentest.FakeClient{}
	fake.Rubrics = []rubricgen.Rubric{rubricgentest.Rubric("x", "T", "HIGH")}
	withFakeGenerator(t, fake)

	out := filepath.Join(t.TempDir(), "d.yaml")
	cmd := newRubricGenerateCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{
		"--sample", "s", "--id", "a/b", "--out", out,
		"--add-criterion", "ok criterion",
		"--add-criterion", "bad\nnewline",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected a validation error for a control-char --add-criterion")
	}
	if fake.Calls() != 0 {
		t.Fatalf("generator was called %d times; --add-criterion must be validated first", fake.Calls())
	}
}

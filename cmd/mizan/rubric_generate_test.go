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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/rubricgen"
	"github.com/ghchinoy/mizan/internal/rubricgen/rubricgentest"
)

func TestValidateRecipe(t *testing.T) {
	for _, ok := range []string{"general_quality_v1", "general_quality_v2", "abc123"} {
		if err := validateRecipe(ok); err != nil {
			t.Errorf("validateRecipe(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "Bad", "has space", "semi;colon", "../etc"} {
		if err := validateRecipe(bad); err == nil {
			t.Errorf("validateRecipe(%q) = nil, want error", bad)
		}
	}
}

func TestValidateGroupName(t *testing.T) {
	for _, ok := range []string{"general_quality", "Clarity", "tone-1", "a.b c"} {
		if err := validateGroupName(ok); err != nil {
			t.Errorf("validateGroupName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "bad\nname", "\ttab", "semi;colon"} {
		if err := validateGroupName(bad); err == nil {
			t.Errorf("validateGroupName(%q) = nil, want error", bad)
		}
	}
}

func TestDefaultRubricGroupName(t *testing.T) {
	cases := map[string]string{
		"general_quality_v1": "general_quality",
		"general_quality_v2": "general_quality",
		"foo_v10":            "foo",
		"no_version":         "no_version",
	}
	for in, want := range cases {
		if got := defaultRubricGroupName(in); got != want {
			t.Errorf("defaultRubricGroupName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAdaptiveMetricPromptDeterministicAndReferencesKeys(t *testing.T) {
	a := adaptiveMetricPrompt([]string{"response", "prompt"})
	b := adaptiveMetricPrompt([]string{"prompt", "response"})
	if a != b {
		t.Fatalf("adaptiveMetricPrompt not order-independent:\n%q\n%q", a, b)
	}
	if !strings.Contains(a, "{{prompt}}") || !strings.Contains(a, "{{response}}") {
		t.Fatalf("prompt missing placeholders: %q", a)
	}
}

// withFakeGenerator swaps the composition-root seam so the command exercises the
// full flow against a scriptable fake instead of a live ADC call.
func withFakeGenerator(t *testing.T, fake *rubricgentest.FakeClient) {
	t.Helper()
	prev := newRubricGenerator
	newRubricGenerator = func(context.Context, *config.Config) (rubricgen.Client, error) {
		return fake, nil
	}
	t.Cleanup(func() { newRubricGenerator = prev })
}

func TestRubricGenerateWritesDraft(t *testing.T) {
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
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The fake received the sample and the default recipe.
	last := fake.LastCall()
	if last == nil || last.Spec.PredefinedMetric != defaultRecipe {
		t.Fatalf("generator not called with default recipe: %+v", last)
	}

	// The criteria table went to stdout.
	if !strings.Contains(stdout.String(), "Answers the question directly") ||
		!strings.Contains(stdout.String(), "CRITERION") {
		t.Fatalf("stdout missing criteria table:\n%s", stdout.String())
	}

	// The draft round-trips through the codec as a valid rubric template.
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	tmpl, err := registry.NewYAMLCodec().Unmarshal(data)
	if err != nil {
		t.Fatalf("draft does not round-trip through codec: %v", err)
	}
	if tmpl.ID != "acme/quality" {
		t.Errorf("draft id = %q, want acme/quality", tmpl.ID)
	}
	if tmpl.Kind != registry.KindRubric {
		t.Errorf("draft kind = %q, want rubric", tmpl.Kind)
	}
	want := []string{"Answers the question directly", "Is free of jargon"}
	if got := tmpl.RubricGroups["general_quality"]; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("draft criteria = %v, want %v (declared order)", got, want)
	}
	if tmpl.MetricPromptTemplate == "" || !strings.Contains(tmpl.MetricPromptTemplate, "{{prompt}}") {
		t.Errorf("draft metricPromptTemplate missing placeholders: %q", tmpl.MetricPromptTemplate)
	}
}

// TestGenerateRubricGroupsNoUsableCriteria proves the SHARED generation+conversion
// path (used by both CUJ 7 and CUJ 8) rejects a response whose rubrics all lack a
// criterion description — a defensive edge case (missing/partial fields) that must
// be an error, not a silently empty rubric written to a draft or the registry.
func TestGenerateRubricGroupsNoUsableCriteria(t *testing.T) {
	fake := &rubricgentest.FakeClient{}
	fake.PushRubrics([]rubricgen.Rubric{
		rubricgentest.Rubric("", "T", "HIGH"),
		rubricgentest.Rubric("   ", "T", "LOW"),
	})

	_, _, err := generateRubricGroups(context.Background(), fake, "sample", defaultRecipe, "general_quality")
	if err == nil || !strings.Contains(err.Error(), "no usable rubric criteria") {
		t.Fatalf("generateRubricGroups = %v, want no-usable-criteria error", err)
	}
}

// TestGenerateRubricGroupsPropagatesClientError proves a generation-client error
// (e.g. a Vertex INVALID_ARGUMENT surfaced verbatim by rubricgen) flows out of the
// shared path unchanged rather than being swallowed.
func TestGenerateRubricGroupsPropagatesClientError(t *testing.T) {
	fake := &rubricgentest.FakeClient{}
	fake.PushError(errors.New("rubricgen: generation failed (HTTP 400 INVALID_ARGUMENT): recipe not found"))

	_, _, err := generateRubricGroups(context.Background(), fake, "sample", defaultRecipe, "general_quality")
	if err == nil || !strings.Contains(err.Error(), "recipe not found") {
		t.Fatalf("generateRubricGroups = %v, want propagated client error", err)
	}
}

// TestGenerateRubricGroupsHappyPath pins the shared path's success contract: the
// sample and recipe reach the client, and criteria are returned in declared order
// under the requested group key.
func TestGenerateRubricGroupsHappyPath(t *testing.T) {
	fake := &rubricgentest.FakeClient{}
	fake.PushRubrics([]rubricgen.Rubric{
		rubricgentest.Rubric("first", "T", "HIGH"),
		rubricgentest.Rubric("second", "T", "LOW"),
	})

	groups, rubrics, err := generateRubricGroups(context.Background(), fake, "the sample", "general_quality_v1", "general_quality")
	if err != nil {
		t.Fatalf("generateRubricGroups: %v", err)
	}
	if len(rubrics) != 2 {
		t.Fatalf("rubrics = %d, want 2", len(rubrics))
	}
	if got := groups["general_quality"]; strings.Join(got, "|") != "first|second" {
		t.Fatalf("criteria = %v, want [first second] in declared order", got)
	}
	last := fake.LastCall()
	if last == nil || last.Spec.PredefinedMetric != "general_quality_v1" {
		t.Fatalf("client not called with the recipe: %+v", last)
	}
	if len(last.Contents) != 1 || len(last.Contents[0].Parts) != 1 || last.Contents[0].Parts[0].Text != "the sample" {
		t.Fatalf("client not called with the sample as a single text part: %+v", last.Contents)
	}
}

// TestWriteDraftRefusesSymlinkAndExisting proves the draft write does not follow
// a pre-planted symlink and does not clobber an existing file (LOW-2). A fresh
// path succeeds with 0600 perms.
func TestWriteDraftRefusesSymlinkAndExisting(t *testing.T) {
	dir := t.TempDir()

	// Fresh path: succeeds, restrictive perms.
	fresh := filepath.Join(dir, "fresh.yaml")
	if err := writeDraft(fresh, []byte("kind: rubric\n")); err != nil {
		t.Fatalf("writeDraft(fresh) = %v, want nil", err)
	}
	info, err := os.Lstat(fresh)
	if err != nil {
		t.Fatalf("lstat fresh: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("draft perms = %o, want 600", info.Mode().Perm())
	}

	// Existing regular file: O_EXCL refuses to overwrite.
	if err := writeDraft(fresh, []byte("x")); err == nil {
		t.Error("writeDraft over an existing file = nil, want error (no clobber)")
	}

	// Pre-planted symlink at the target: O_NOFOLLOW/O_EXCL refuse to follow it, so
	// the sensitive link target is never written through.
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("original"), 0o600); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := writeDraft(link, []byte("attacker")); err == nil {
		t.Error("writeDraft through a symlink = nil, want error (no symlink follow)")
	}
	got, err := os.ReadFile(secret)
	if err != nil {
		t.Fatalf("read secret: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("symlink target was modified through the draft write: %q", got)
	}
}

func TestRubricGenerateFlagValidation(t *testing.T) {
	// A fake is installed but must never be reached: each case fails on local
	// validation BEFORE the generator is built.
	fake := &rubricgentest.FakeClient{}
	fake.Rubrics = []rubricgen.Rubric{rubricgentest.Rubric("x", "T", "HIGH")}
	withFakeGenerator(t, fake)

	out := filepath.Join(t.TempDir(), "d.yaml")
	cases := []struct {
		name string
		args []string
	}{
		{"missing-sample", []string{"--id", "a/b", "--out", out}},
		{"missing-out", []string{"--sample", "s", "--id", "a/b"}},
		{"bad-id", []string{"--sample", "s", "--id", "NotValid", "--out", out}},
		{"bad-recipe", []string{"--sample", "s", "--id", "a/b", "--out", out, "--recipe", "Bad Recipe"}},
		{"bad-group", []string{"--sample", "s", "--id", "a/b", "--out", out, "--group-name", "bad\nname"}},
		// CRIT-1: a hostile --location is rejected locally, before any authed call.
		{"location-host-injection", []string{"--sample", "s", "--id", "a/b", "--out", out, "--location", "evil.com/"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newRubricGenerateCmd()
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("expected validation error for args %v", tc.args)
			}
		})
	}
	if fake.Calls() != 0 {
		t.Fatalf("generator was called %d times; validation must fail before any live call", fake.Calls())
	}
}

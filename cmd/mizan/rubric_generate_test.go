package main

import (
	"bytes"
	"context"
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

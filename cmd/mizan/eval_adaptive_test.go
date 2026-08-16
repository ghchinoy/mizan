package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/rubricgen"
	"github.com/ghchinoy/mizan/internal/rubricgen/rubricgentest"
)

// TestEvalAdaptiveFlagValidation proves each local guard in `eval adaptive` fires
// BEFORE any live call: the generator seam is installed but must never be reached.
func TestEvalAdaptiveFlagValidation(t *testing.T) {
	fake := &rubricgentest.FakeClient{}
	fake.Rubrics = []rubricgen.Rubric{rubricgentest.Rubric("x", "T", "HIGH")}
	prev := newRubricGenerator
	newRubricGenerator = func(context.Context, *config.Config) (rubricgen.Client, error) { return fake, nil }
	t.Cleanup(func() { newRubricGenerator = prev })

	cases := []struct {
		name string
		args []string
	}{
		{"missing-prompt", []string{"--response", "r"}},
		{"missing-response", []string{"--prompt", "p"}},
		{"bad-recipe", []string{"--prompt", "p", "--response", "r", "--recipe", "Bad Recipe"}},
		{"bad-group", []string{"--prompt", "p", "--response", "r", "--group-name", "bad\nname"}},
		{"bad-save-as", []string{"--prompt", "p", "--response", "r", "--save-as", "NotValid"}},
		{"bad-model", []string{"--prompt", "p", "--response", "r", "--model", "../../etc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newEvalAdaptiveCmd()
			cmd.SetOut(new(bytes.Buffer))
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil {
				t.Fatalf("expected validation error for args %v", tc.args)
			}
		})
	}
	if fake.Calls() != 0 {
		t.Fatalf("generator called %d times; every guard must fail before any live call", fake.Calls())
	}
}

// TestEvalAdaptiveWiredUnderEval confirms `adaptive` is a subcommand of `eval`
// (the EM decision: a subcommand, not a flag on `eval run`).
func TestEvalAdaptiveWiredUnderEval(t *testing.T) {
	eval := newEvalCmd()
	var found bool
	for _, c := range eval.Commands() {
		if c.Name() == "adaptive" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("`adaptive` is not registered under `eval`")
	}
}

package main

// eval_rubric_scale_flag_test.go covers the `eval run` glue that decides HOW the
// rubric-detail scale is sourced: the RunE branch keys on
// cmd.Flags().Changed("rubric-scale") to choose between the validated flag path
// (eval.ParseRubricScale + WithRubricDetail) and deferring to the template/default
// scale (WithRubricDetailDefaultScale). This exercises that predicate and the
// parse it drives WITHOUT creds or network (the surrounding RunE reaches config/
// service first).

import (
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
)

// TestEvalRunRubricScaleChangedGlue proves the two arms of the glue predicate:
//   - flag absent  => Changed==false => glue defers to the template/default scale
//     (WithRubricDetailDefaultScale), and the flag default remains "1-5";
//   - flag present => Changed==true  => glue parses it via eval.ParseRubricScale
//     (WithRubricDetail), so a valid value threads through and an inverted value
//     is rejected locally.
func TestEvalRunRubricScaleChangedGlue(t *testing.T) {
	// Arm 1: --rubric-detail without --rubric-scale.
	noScale := newEvalRunCmd()
	if err := noScale.Flags().Parse([]string{"--metric", "m", "--rubric-detail"}); err != nil {
		t.Fatalf("Parse (no scale): %v", err)
	}
	if noScale.Flags().Changed("rubric-scale") {
		t.Error("rubric-scale reported Changed with no flag given; glue would wrongly parse the default instead of deferring to the template scale")
	}
	if got, _ := noScale.Flags().GetString("rubric-scale"); got != "1-5" {
		t.Errorf("rubric-scale default = %q, want 1-5", got)
	}

	// Arm 2: an explicit, valid --rubric-scale flips Changed and parses through the
	// same eval.ParseRubricScale the glue calls.
	withScale := newEvalRunCmd()
	if err := withScale.Flags().Parse([]string{"--metric", "m", "--rubric-detail", "--rubric-scale", "2-8"}); err != nil {
		t.Fatalf("Parse (with scale): %v", err)
	}
	if !withScale.Flags().Changed("rubric-scale") {
		t.Fatal("rubric-scale not reported Changed after being set; glue would skip ParseRubricScale and lose the flag value")
	}
	got, _ := withScale.Flags().GetString("rubric-scale")
	min, max, err := eval.ParseRubricScale(got)
	if err != nil {
		t.Fatalf("ParseRubricScale(%q): %v", got, err)
	}
	if min != 2 || max != 8 {
		t.Errorf("ParseRubricScale(%q) = %d-%d, want 2-8", got, min, max)
	}

	// Arm 2 (rejection): an inverted explicit flag value fails locally in the same
	// ParseRubricScale the glue invokes, before any eval call.
	if _, _, err := eval.ParseRubricScale("5-1"); err == nil {
		t.Error("ParseRubricScale(\"5-1\") = nil error; glue would accept an inverted flag scale")
	}
}

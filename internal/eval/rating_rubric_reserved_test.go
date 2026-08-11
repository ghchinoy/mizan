package eval

// rating_rubric_reserved_test.go covers H1 (RFC-0001 §4.4 / §11 item 3): the
// optional ratingRubric block is CARRIED on the template but RESERVED — it must
// NOT be threaded into the eval runtime in v1. These tests pin that: setting
// ratingRubric changes neither the judge prompt nor the Result on any live path.

import (
	"context"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// sampleRatingRubric is a representative per-group rating-band -> description map.
func sampleRatingRubric() map[string]map[string]string {
	return map[string]map[string]string{
		"clarity": {"1": "incomprehensible", "3": "somewhat clear", "5": "completely clear"},
		"tone":    {"1": "off-brand", "5": "perfectly on-brand"},
	}
}

// TestRatingRubricNotThreadedIntoRubricDetailPrompt proves the rating-band
// DESCRIPTIONS never leak into the genai/global structured judge prompt, and that
// the Result is identical whether or not ratingRubric is set (same score, same
// warnings) — i.e. it is reserved, not consumed.
func TestRatingRubricNotThreadedIntoRubricDetailPrompt(t *testing.T) {
	run := func(withRR bool) (string, Result) {
		tmpl := rubricTemplate()
		if withRR {
			tmpl.RatingRubric = sampleRatingRubric()
		}
		fg := &fakeGenai{respText: scaleProbeJSON}
		eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
		res, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetail(1, 5))
		if err != nil {
			t.Fatalf("Run(withRR=%v): %v", withRR, err)
		}
		return fg.gotContents[0].Parts[0].Text, res
	}

	promptWith, resWith := run(true)
	promptWithout, resWithout := run(false)

	// The prompt must be byte-identical: ratingRubric must not perturb it.
	if promptWith != promptWithout {
		t.Errorf("ratingRubric perturbed the judge prompt (must be reserved):\nwith:\n%s\nwithout:\n%s", promptWith, promptWithout)
	}
	// None of the rating-band descriptions may appear in the prompt.
	for _, desc := range []string{"incomprehensible", "somewhat clear", "completely clear", "off-brand", "perfectly on-brand"} {
		if strings.Contains(promptWith, desc) {
			t.Errorf("reserved ratingRubric description %q leaked into the judge prompt:\n%s", desc, promptWith)
		}
	}
	// Scoring is unaffected.
	if (resWith.Score == nil) != (resWithout.Score == nil) {
		t.Fatalf("Score nil-ness differs: with=%v without=%v", resWith.Score, resWithout.Score)
	}
	if resWith.Score != nil && *resWith.Score != *resWithout.Score {
		t.Errorf("ratingRubric changed Score: with=%v without=%v", *resWith.Score, *resWithout.Score)
	}
	if len(resWith.Warnings) != len(resWithout.Warnings) {
		t.Errorf("ratingRubric changed Warnings: with=%v without=%v", resWith.Warnings, resWithout.Warnings)
	}
}

// TestRatingRubricNotThreadedIntoNativeRubricPrompt proves the same for the
// native rubric path (runRubric), whose prompt is built from RubricGroups only.
func TestRatingRubricNotThreadedIntoNativeRubricPrompt(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RatingRubric = sampleRatingRubric()
	fc := &fakeClient{}
	eng := NewEngine(fc, "p", "us-central1")

	// The native rubric path materializes a pointwise request; a nil resp is fine
	// here because we only need the OUTGOING request the engine built.
	_, _ = eng.Run(context.Background(), tmpl, Instance{
		Fields: map[string]AssetRef{"copy": {Modality: registry.ModalityText, Text: "Buy now."}},
	})
	if fc.gotReq == nil {
		t.Fatal("native path did not build a request")
	}
	prompt := fc.gotReq.GetPointwiseMetricInput().GetMetricSpec().GetMetricPromptTemplate()
	for _, desc := range []string{"incomprehensible", "somewhat clear", "completely clear", "off-brand", "perfectly on-brand"} {
		if strings.Contains(prompt, desc) {
			t.Errorf("reserved ratingRubric description %q leaked into the native judge prompt:\n%s", desc, prompt)
		}
	}
}

// TestRatingRubricRoundTripsInMemory is a minimal carry check: the field is
// preserved on the struct (the codec that will persist it is deferred to P2, but
// the model must already hold it so P2 can canonicalize/validate it).
func TestRatingRubricRoundTripsInMemory(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RatingRubric = sampleRatingRubric()
	if got := tmpl.RatingRubric["clarity"]["5"]; got != "completely clear" {
		t.Errorf("RatingRubric round-trip failed: got %q", got)
	}
}

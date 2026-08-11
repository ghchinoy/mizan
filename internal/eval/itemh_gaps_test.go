package eval

// itemh_gaps_test.go closes coverage gaps found while verifying the ITEM H
// descoped subset (PR #43) against its brief. It is TEST-ONLY and additive; it
// changes no product code. Three gaps are addressed:
//
//   - H2 target 3 (scale validation): the --rubric-scale FLAG is validated by
//     ParseRubricScale (see TestParseRubricScale). A template-declared
//     rubricDetail.scale is now held to the SAME contract by resolveRubricScale
//     (rubricScaleBoundsValid: non-negative, min<max): an invalid scale (min>=max,
//     negative, min==max) FALLS BACK to the default 1-5 and emits a NON-FATAL
//     Result.Warnings note rather than reaching the judge verbatim. These tests
//     assert that fix (they previously pinned the pre-fix silent-accept behavior).
//   - H3 target 4 (warning matrix completeness): the native PAIRWISE path honors
//     FlipEnabled and must not emit the genai-path warning. The author's suite
//     pins the native path for SamplingCount but not for FlipEnabled.
//   - H1 target 1 (reserved-not-threaded completeness): the native rubric prompt
//     must be BYTE-IDENTICAL with and without RatingRubric, not merely free of the
//     band descriptions.

import (
	"context"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestResolveRubricScaleFallsBackOnInvalidTemplateScale asserts the FIX: an
// invalid template-declared scale is NOT returned verbatim. It is held to the same
// contract as the --rubric-scale flag (rubricScaleBoundsValid: non-negative,
// min<max); when it fails, resolveRubricScale falls back to the default 1-5 AND
// returns a non-fatal warning naming the field and the default now in effect.
//
// (This test previously pinned the pre-fix silent-accept behavior; it now asserts
// the corrected behavior.)
func TestResolveRubricScaleFallsBackOnInvalidTemplateScale(t *testing.T) {
	withScale := func(min, max int) registry.MetricTemplate {
		tmpl := rubricTemplate()
		tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: min, Max: max}}
		return tmpl
	}
	e := &Engine{}
	rc := runConfig{rubricDetail: true, scaleSet: false} // no explicit flag scale

	cases := []struct {
		name     string
		min, max int
	}{
		{"min greater than max falls back to default", 8, 2},
		{"min equals max falls back to default", 3, 3},
		{"negative min falls back to default", -4, 5},
		{"both negative falls back to default", -5, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			min, max, warning := e.resolveRubricScale(withScale(tc.min, tc.max), rc)
			if min != defaultRubricScaleMin || max != defaultRubricScaleMax {
				t.Errorf("resolveRubricScale = %d-%d, want default %d-%d for invalid template scale %d-%d",
					min, max, defaultRubricScaleMin, defaultRubricScaleMax, tc.min, tc.max)
			}
			if warning == "" {
				t.Fatalf("expected a non-fatal warning for invalid template scale %d-%d, got none", tc.min, tc.max)
			}
			if !strings.Contains(warning, "rubricDetail.scale") {
				t.Errorf("warning should name the offending field rubricDetail.scale: %q", warning)
			}
			if !strings.Contains(warning, "default") {
				t.Errorf("warning should state the default scale is used instead: %q", warning)
			}
		})
	}
}

// TestInvalidTemplateScaleFallsBackAndWarnsEndToEnd asserts the end-to-end FIX: an
// inverted template scale (min>max) does NOT reach the judge instruction. The run
// still SUCCEEDS (non-fatal, consistent with H3), the prompt carries the fallback
// default 1-5 instead of the nonsensical "8 to 2", and the fallback warning is
// surfaced on Result.Warnings.
//
// (This test previously pinned that the inverted scale reached the prompt
// verbatim; it now asserts the corrected fallback + warning behavior.)
func TestInvalidTemplateScaleFallsBackAndWarnsEndToEnd(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: 8, Max: 2}}

	fg := &fakeGenai{respText: scaleProbeJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	res, err := eng.Run(context.Background(), tmpl, rubricInstance(), WithRubricDetailDefaultScale())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fg.gotContents) == 0 || len(fg.gotContents[0].Parts) == 0 {
		t.Fatal("no prompt captured")
	}
	prompt := fg.gotContents[0].Parts[0].Text
	if strings.Contains(prompt, "8 to 2") {
		t.Errorf("inverted template scale 8-2 must NOT reach the judge prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "1 to 5") {
		t.Errorf("expected the fallback default scale 1 to 5 in the prompt:\n%s", prompt)
	}
	if !warningsContain(res.Warnings, "rubricDetail.scale") {
		t.Errorf("expected a warning naming rubricDetail.scale, got: %v", res.Warnings)
	}
	if !warningsContain(res.Warnings, "default") {
		t.Errorf("expected the warning to state the default is used, got: %v", res.Warnings)
	}
}

// TestNativePairwisePathDoesNotWarnOnFlip completes the H3 native-path control:
// the native pairwise path HONORS FlipEnabled (it is a native pairwise feature)
// and therefore must NOT emit the genai/global-path "ignored" warning, even
// though the pairwise fixture sets FlipEnabled=true.
func TestNativePairwisePathDoesNotWarnOnFlip(t *testing.T) {
	tmpl := pairwiseTemplate() // FlipEnabled: true, native path
	if !tmpl.FlipEnabled {
		t.Fatal("precondition: pairwise fixture must set FlipEnabled=true")
	}
	fc := &fakeClient{resp: pairwiseResp(aiplatformpb.PairwiseChoice_BASELINE, "because")}
	eng := NewEngine(fc, "p", "us-central1")

	res, err := eng.Run(context.Background(), tmpl, pairwiseInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if warningsContain(res.Warnings, "ignored on the genai/global structured path") {
		t.Errorf("native pairwise path emitted a genai-path warning despite honoring FlipEnabled: %v", res.Warnings)
	}
	if warningsContain(res.Warnings, "flipEnabled") {
		t.Errorf("native pairwise path warned about flipEnabled it actually honors: %v", res.Warnings)
	}
}

// TestRatingRubricNativePromptByteIdentical strengthens H1 for the native rubric
// path: the OUTGOING judge prompt must be byte-identical with and without
// RatingRubric, not merely free of the band descriptions. This is the same
// byte-identity guarantee the author's genai-path test makes.
func TestRatingRubricNativePromptByteIdentical(t *testing.T) {
	buildNativeRubricPrompt := func(withRR bool) string {
		tmpl := rubricTemplate()
		if withRR {
			tmpl.RatingRubric = sampleRatingRubric()
		}
		fc := &fakeClient{}
		eng := NewEngine(fc, "p", "us-central1")
		_, _ = eng.Run(context.Background(), tmpl, Instance{
			Fields: map[string]AssetRef{"copy": {Modality: registry.ModalityText, Text: "Buy now."}},
		})
		if fc.gotReq == nil {
			t.Fatal("native rubric path did not build a request")
		}
		return fc.gotReq.GetPointwiseMetricInput().GetMetricSpec().GetMetricPromptTemplate()
	}

	with := buildNativeRubricPrompt(true)
	without := buildNativeRubricPrompt(false)
	if with != without {
		t.Errorf("RatingRubric perturbed the native rubric prompt (must be reserved):\nwith:\n%s\nwithout:\n%s", with, without)
	}
}

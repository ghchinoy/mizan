package eval

// itemh_gaps_test.go closes coverage gaps found while verifying the ITEM H
// descoped subset (PR #43) against its brief. It is TEST-ONLY and additive; it
// changes no product code. Three gaps are addressed:
//
//   - H2 target 3 (scale validation): the --rubric-scale FLAG is validated by
//     ParseRubricScale (see TestParseRubricScale), but a template-declared
//     rubricDetail.scale is threaded through resolveRubricScale WITHOUT any
//     validation. These tests PIN that silent-accept behavior (min>=max,
//     negative, min==max) so a future fix pass changes them deliberately. This is
//     FLAGGED to the manager as a possible product-code gap.
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

// TestResolveRubricScaleAcceptsInvalidTemplateScale PINS the current behavior:
// resolveRubricScale performs NO validation on a template-declared scale. Unlike
// the --rubric-scale flag (validated by ParseRubricScale, which rejects min>=max
// and negative bounds), an invalid template scale is returned verbatim.
//
// This is a CHARACTERIZATION test, not an endorsement: if a future fix pass adds
// template-scale validation, these expectations must change deliberately.
// FLAGGED to the manager (see scratchpad report).
func TestResolveRubricScaleAcceptsInvalidTemplateScale(t *testing.T) {
	withScale := func(min, max int) registry.MetricTemplate {
		tmpl := rubricTemplate()
		tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: min, Max: max}}
		return tmpl
	}
	e := &Engine{}
	rc := runConfig{rubricDetail: true, scaleSet: false} // no explicit flag scale

	cases := []struct {
		name             string
		min, max         int
		wantMin, wantMax int
	}{
		{"min greater than max accepted as-is", 8, 2, 8, 2},
		{"min equals max accepted as-is", 3, 3, 3, 3},
		{"negative min accepted as-is", -4, 5, -4, 5},
		{"both negative accepted as-is", -5, -1, -5, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			min, max := e.resolveRubricScale(withScale(tc.min, tc.max), rc)
			if min != tc.wantMin || max != tc.wantMax {
				t.Errorf("resolveRubricScale = %d-%d, want %d-%d (template scale is currently unvalidated)",
					min, max, tc.wantMin, tc.wantMax)
			}
		})
	}
}

// TestInvalidTemplateScaleReachesJudgePromptUnvalidated PINS the end-to-end
// consequence of the above: an inverted template scale (min>max) is threaded into
// the judge instruction verbatim ("from 8 to 2") and the run SUCCEEDS — no
// validation error is raised anywhere on the path. FLAGGED as a possible gap: a
// nonsensical scale silently reaches the judge and drives clamping.
func TestInvalidTemplateScaleReachesJudgePromptUnvalidated(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: 8, Max: 2}}

	prompt := runScaleProbe(t, tmpl, WithRubricDetailDefaultScale())
	if !strings.Contains(prompt, "8 to 2") {
		t.Errorf("inverted template scale 8-2 was not threaded verbatim into the prompt:\n%s", prompt)
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

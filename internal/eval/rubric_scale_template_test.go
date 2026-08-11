package eval

// rubric_scale_template_test.go covers H2 (RFC-0001 §4.4): a template may declare
// an OPTIONAL rubricDetail.scale {min,max} that the genai/global structured path
// honors, while the existing --rubric-detail run flag keeps working. Precedence:
// explicit run-flag scale > template scale > built-in default (1-5).

import (
	"context"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestResolveRubricScalePrecedence unit-tests the H2 precedence directly.
func TestResolveRubricScalePrecedence(t *testing.T) {
	withScale := func(min, max int) registry.MetricTemplate {
		tmpl := rubricTemplate()
		tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: min, Max: max}}
		return tmpl
	}
	e := &Engine{}
	cases := []struct {
		name             string
		tmpl             registry.MetricTemplate
		rc               runConfig
		wantMin, wantMax int
	}{
		{
			name:    "explicit flag scale wins over template scale",
			tmpl:    withScale(2, 8),
			rc:      runConfig{rubricDetail: true, scaleSet: true, scaleMin: 1, scaleMax: 5},
			wantMin: 1, wantMax: 5,
		},
		{
			name:    "template scale used when flag scale absent",
			tmpl:    withScale(2, 8),
			rc:      runConfig{rubricDetail: true, scaleSet: false},
			wantMin: 2, wantMax: 8,
		},
		{
			name:    "default 1-5 when neither flag nor template scale present",
			tmpl:    rubricTemplate(),
			rc:      runConfig{rubricDetail: true, scaleSet: false},
			wantMin: defaultRubricScaleMin, wantMax: defaultRubricScaleMax,
		},
		{
			name:    "nil RubricDetail falls through to default",
			tmpl:    rubricTemplate(),
			rc:      runConfig{rubricDetail: true, scaleSet: false},
			wantMin: 1, wantMax: 5,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			min, max, warning := e.resolveRubricScale(tc.tmpl, tc.rc)
			if min != tc.wantMin || max != tc.wantMax {
				t.Errorf("resolveRubricScale = %d-%d, want %d-%d", min, max, tc.wantMin, tc.wantMax)
			}
			// All precedence cases here use valid (or absent) scales, so no
			// fallback warning must be emitted.
			if warning != "" {
				t.Errorf("resolveRubricScale returned an unexpected warning for a valid scale: %q", warning)
			}
		})
	}
}

// scaleProbeJSON is a happy-path judge response (full authored set) so the run
// reaches the prompt-building step this test inspects.
const scaleProbeJSON = `{
	"per_criterion": [
		{"group":"clarity","criterion":"The message is unambiguous","score":1,"rationale":"x"},
		{"group":"clarity","criterion":"No jargon","score":1,"rationale":"x"},
		{"group":"tone","criterion":"Matches a professional brand voice","score":1,"rationale":"x"}
	],
	"overall_score":1,"explanation":"x"}`

// runScaleProbe runs the rubric-detail path and returns the judge prompt the
// engine sent, so a test can assert which scale bounds were threaded in.
func runScaleProbe(t *testing.T, tmpl registry.MetricTemplate, opt RunOption) string {
	t.Helper()
	fg := &fakeGenai{respText: scaleProbeJSON}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
	if _, err := eng.Run(context.Background(), tmpl, rubricInstance(), opt); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fg.gotContents) == 0 || len(fg.gotContents[0].Parts) == 0 {
		t.Fatalf("no prompt captured")
	}
	return fg.gotContents[0].Parts[0].Text
}

// TestTemplateScaleHonoredWhenFlagDefaulted proves a template-declared scale is
// threaded into the judge instruction when the caller relies on the default
// (WithRubricDetailDefaultScale — i.e. --rubric-detail without --rubric-scale).
func TestTemplateScaleHonoredWhenFlagDefaulted(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: 2, Max: 8}}
	prompt := runScaleProbe(t, tmpl, WithRubricDetailDefaultScale())
	if !strings.Contains(prompt, "2 to 8") {
		t.Errorf("prompt did not honor template scale 2-8:\n%s", prompt)
	}
	if strings.Contains(prompt, "1 to 5") {
		t.Errorf("prompt used the default scale instead of the template scale:\n%s", prompt)
	}
}

// TestExplicitFlagScaleOverridesTemplateScale proves an explicit --rubric-scale
// (WithRubricDetail) wins over a template-declared scale.
func TestExplicitFlagScaleOverridesTemplateScale(t *testing.T) {
	tmpl := rubricTemplate()
	tmpl.RubricDetail = &registry.RubricDetail{Scale: &registry.RubricScale{Min: 2, Max: 8}}
	prompt := runScaleProbe(t, tmpl, WithRubricDetail(1, 5))
	if !strings.Contains(prompt, "1 to 5") {
		t.Errorf("explicit flag scale 1-5 not honored:\n%s", prompt)
	}
}

// TestDefaultScaleWhenNoTemplateScale proves behavior is exactly today's default
// (1-5) when the template declares no scale and the flag scale is defaulted.
func TestDefaultScaleWhenNoTemplateScale(t *testing.T) {
	prompt := runScaleProbe(t, rubricTemplate(), WithRubricDetailDefaultScale())
	if !strings.Contains(prompt, "1 to 5") {
		t.Errorf("default scale 1-5 not used:\n%s", prompt)
	}
}

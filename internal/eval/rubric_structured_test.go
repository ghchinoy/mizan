package eval

import (
	"context"
	"reflect"
	"strings"
	"testing"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestGenerateRubricSchema verifies the deterministic, fixed per-criterion
// schema shape: a per_criterion array of {group, criterion, score(int),
// rationale} objects plus overall_score(number) and explanation(string), and
// that repeated calls produce an identical schema (determinism).
func TestGenerateRubricSchema(t *testing.T) {
	s1 := generateRubricSchema()
	s2 := generateRubricSchema()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("generateRubricSchema is not deterministic:\n%+v\n%+v", s1, s2)
	}

	if s1.Type != genai.TypeObject {
		t.Errorf("top type = %q, want OBJECT", s1.Type)
	}
	if !reflect.DeepEqual(s1.Required, []string{"per_criterion", "overall_score", "explanation"}) {
		t.Errorf("top required = %v", s1.Required)
	}
	pc := s1.Properties["per_criterion"]
	if pc == nil || pc.Type != genai.TypeArray || pc.Items == nil {
		t.Fatalf("per_criterion not an array of objects: %+v", pc)
	}
	item := pc.Items
	if item.Type != genai.TypeObject {
		t.Errorf("per_criterion items type = %q, want OBJECT", item.Type)
	}
	if item.Properties["score"].Type != genai.TypeInteger {
		t.Errorf("score type = %q, want INTEGER", item.Properties["score"].Type)
	}
	for _, k := range []string{"group", "criterion", "rationale"} {
		if item.Properties[k].Type != genai.TypeString {
			t.Errorf("%s type = %q, want STRING", k, item.Properties[k].Type)
		}
	}
	if !reflect.DeepEqual(item.Required, []string{"group", "criterion", "score", "rationale"}) {
		t.Errorf("item required = %v", item.Required)
	}
	if s1.Properties["overall_score"].Type != genai.TypeNumber {
		t.Errorf("overall_score type = %q, want NUMBER", s1.Properties["overall_score"].Type)
	}
	if s1.Properties["explanation"].Type != genai.TypeString {
		t.Errorf("explanation type = %q, want STRING", s1.Properties["explanation"].Type)
	}
}

// TestRenderRubricInstructionOrdering verifies the judge instruction enumerates
// groups sorted and criteria in declared order (mirroring renderRubricGroups),
// is deterministic, and carries the configured scale.
func TestRenderRubricInstructionOrdering(t *testing.T) {
	groups := map[string][]string{
		"b-group": {"crit b1"},
		"a-group": {"crit a1", "crit a2"},
	}
	got1 := renderRubricInstruction(groups, 1, 5)
	got2 := renderRubricInstruction(groups, 1, 5)
	if got1 != got2 {
		t.Error("renderRubricInstruction is not deterministic")
	}
	if strings.Index(got1, "a-group") > strings.Index(got1, "b-group") {
		t.Errorf("groups not sorted:\n%s", got1)
	}
	if strings.Index(got1, "crit a1") > strings.Index(got1, "crit a2") {
		t.Errorf("criteria not in declared order:\n%s", got1)
	}
	if !strings.Contains(got1, "1 to 5") {
		t.Errorf("instruction missing scale bounds:\n%s", got1)
	}
}

// rubricInstance is the standard single-field instance for rubricTemplate().
func rubricInstance() Instance {
	return Instance{Fields: map[string]AssetRef{
		"copy": {Modality: registry.ModalityText, Text: "Buy now, save big."},
	}}
}

// TestRunRubricStructuredRoundTrip drives the full detail path: a fake genai
// response with a per_criterion array round-trips into Result.CustomOutput with
// one entry per authored criterion, and overall_score maps to Result.Score.
func TestRunRubricStructuredRoundTrip(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"The message is unambiguous","score":4,"rationale":"mostly clear"},
			{"group":"clarity","criterion":"No jargon","score":5,"rationale":"plain language"},
			{"group":"tone","criterion":"Matches a professional brand voice","score":3,"rationale":"a bit casual"}
		],
		"overall_score": 4,
		"explanation": "Solid ad copy."
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// One entry per authored criterion (2 clarity + 1 tone).
	pc, ok := res.CustomOutput["per_criterion"].([]any)
	if !ok || len(pc) != 3 {
		t.Fatalf("per_criterion = %v (want 3 entries)", res.CustomOutput["per_criterion"])
	}
	first := pc[0].(map[string]any)
	if first["group"] != "clarity" || first["criterion"] != "The message is unambiguous" {
		t.Errorf("first entry = %v", first)
	}
	// score coerced to int.
	if _, isInt := first["score"].(int); !isInt {
		t.Errorf("per-criterion score not coerced to int: %T %v", first["score"], first["score"])
	}
	// overall_score -> Result.Score.
	if res.Score == nil || *res.Score != 4 {
		t.Errorf("Score = %v, want 4 (from overall_score)", res.Score)
	}
	if res.CustomOutput["explanation"] != "Solid ad copy." {
		t.Errorf("explanation = %v", res.CustomOutput["explanation"])
	}

	// The genai path was used with the fixed schema and bare model id.
	if fg.calls != 1 {
		t.Errorf("genai calls = %d, want 1", fg.calls)
	}
	if fg.gotModel != "gemini-2.5-flash" {
		t.Errorf("model = %q, want gemini-2.5-flash", fg.gotModel)
	}
	if fg.gotCfg == nil || fg.gotCfg.ResponseSchema == nil || fg.gotCfg.ResponseSchema.Type != genai.TypeObject {
		t.Fatalf("ResponseSchema not set to fixed object schema: %+v", fg.gotCfg)
	}
	// The prompt carries the base template + the per-criterion instruction.
	prompt := fg.gotContents[0].Parts[0].Text
	for _, want := range []string{"Evaluate this ad copy", "The message is unambiguous", "1 to 5"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\nprompt: %s", want, prompt)
		}
	}
}

// TestRunRubricStructuredScaleConfigurable verifies both the default 1-5 scale
// and a non-default 0-10 scale are threaded into the judge instruction.
func TestRunRubricStructuredScaleConfigurable(t *testing.T) {
	cases := []struct {
		min, max int
		want     string
	}{
		{1, 5, "1 to 5"},
		{0, 10, "0 to 10"},
	}
	for _, tc := range cases {
		fg := &fakeGenai{respText: `{"per_criterion":[],"overall_score":1,"explanation":"x"}`}
		eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))
		if _, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(tc.min, tc.max)); err != nil {
			t.Fatalf("Run(%d-%d): %v", tc.min, tc.max, err)
		}
		prompt := fg.gotContents[0].Parts[0].Text
		if !strings.Contains(prompt, tc.want) {
			t.Errorf("scale %d-%d: prompt missing %q\nprompt: %s", tc.min, tc.max, tc.want, prompt)
		}
	}
}

// TestRunRubricStructuredClamps verifies out-of-range judge scores are clamped
// into [min,max] (both per-criterion and overall_score).
func TestRunRubricStructuredClamps(t *testing.T) {
	resp := `{
		"per_criterion": [
			{"group":"clarity","criterion":"c1","score":7,"rationale":"too high"},
			{"group":"clarity","criterion":"c2","score":-2,"rationale":"too low"},
			{"group":"tone","criterion":"c3","score":3,"rationale":"ok"}
		],
		"overall_score": 9,
		"explanation": "clamp me"
	}`
	fg := &fakeGenai{respText: resp}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance(), WithRubricDetail(1, 5))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	pc := res.CustomOutput["per_criterion"].([]any)
	got := []int{
		pc[0].(map[string]any)["score"].(int),
		pc[1].(map[string]any)["score"].(int),
		pc[2].(map[string]any)["score"].(int),
	}
	want := []int{5, 1, 3} // 7->5, -2->1, 3 unchanged
	if !reflect.DeepEqual(got, want) {
		t.Errorf("clamped per-criterion scores = %v, want %v", got, want)
	}
	if res.CustomOutput["overall_score"] != float64(5) {
		t.Errorf("overall_score = %v, want 5 (clamped from 9)", res.CustomOutput["overall_score"])
	}
	if res.Score == nil || *res.Score != 5 {
		t.Errorf("Score = %v, want 5", res.Score)
	}
}

// TestParseRubricScale covers valid and malformed scale strings.
func TestParseRubricScale(t *testing.T) {
	valid := map[string][2]int{
		"1-5":   {1, 5},
		"0-10":  {0, 10},
		" 2-8 ": {2, 8},
	}
	for in, want := range valid {
		min, max, err := ParseRubricScale(in)
		if err != nil {
			t.Errorf("ParseRubricScale(%q) unexpected error: %v", in, err)
			continue
		}
		if min != want[0] || max != want[1] {
			t.Errorf("ParseRubricScale(%q) = (%d,%d), want %v", in, min, max, want)
		}
	}
	for _, in := range []string{"", "5", "abc", "5-2", "3-3", "1-", "-5", "1-2-3", "x-y"} {
		if _, _, err := ParseRubricScale(in); err == nil {
			t.Errorf("ParseRubricScale(%q) = nil error, want error", in)
		}
	}
}

// TestRubricDetailOffUnchanged proves a KindRubric run WITHOUT --rubric-detail
// still takes the native pointwise path (single {score,explanation}) and never
// touches the genai client.
func TestRubricDetailOffUnchanged(t *testing.T) {
	fc := &fakeClient{
		resp: &aiplatformpb.EvaluateInstancesResponse{
			EvaluationResults: &aiplatformpb.EvaluateInstancesResponse_PointwiseMetricResult{
				PointwiseMetricResult: &aiplatformpb.PointwiseMetricResult{
					Score:       proto.Float32(3.0),
					Explanation: "native explanation",
				},
			},
		},
	}
	fg := &fakeGenai{respText: `{"per_criterion":[]}`}
	eng := NewEngine(fc, "p", "us-central1", WithGenaiClient(fg))

	res, err := eng.Run(context.Background(), rubricTemplate(), rubricInstance())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fg.calls != 0 {
		t.Errorf("genai called %d times on detail-off rubric run; native path expected", fg.calls)
	}
	if fc.gotReq == nil || fc.gotReq.GetPointwiseMetricInput() == nil {
		t.Error("native pointwise path not taken")
	}
	if res.Score == nil || *res.Score != 3.0 {
		t.Errorf("Score = %v, want 3.0", res.Score)
	}
	if res.Explanation != "native explanation" {
		t.Errorf("Explanation = %q", res.Explanation)
	}
	if len(res.CustomOutput) != 0 {
		t.Errorf("native rubric run should have no CustomOutput, got %v", res.CustomOutput)
	}
}

// TestRubricDetailOnNonRubricErrors proves --rubric-detail is rejected on a
// non-rubric template with a clear local error and no client call.
func TestRubricDetailOnNonRubricErrors(t *testing.T) {
	fg := &fakeGenai{respText: "{}"}
	eng := NewEngine(&fakeClient{}, "p", "us-central1", WithGenaiClient(fg))

	_, err := eng.Run(context.Background(), customSchemaTemplate(), Instance{
		Fields: map[string]AssetRef{"creative": {Text: "x"}},
	}, WithRubricDetail(1, 5))
	if err == nil || !strings.Contains(err.Error(), "only applies to rubric") {
		t.Fatalf("want 'only applies to rubric' error, got %v", err)
	}
	if fg.calls != 0 {
		t.Errorf("genai called %d times; should fail before any call", fg.calls)
	}
}

// TestResolveRubricDetailIsGenaiGlobal proves the pre-flight target for a
// rubric-detail run reports the genai/global path it will actually take, while a
// detail-off rubric run stays native/regional.
func TestResolveRubricDetailIsGenaiGlobal(t *testing.T) {
	eng := NewEngine(&fakeClient{}, "proj", "us-central1")

	off := eng.Resolve(rubricTemplate(), "", false)
	if off.Path != "native" || off.Location != "us-central1" {
		t.Errorf("detail-off rubric target = %+v, want native/us-central1", off)
	}
	on := eng.Resolve(rubricTemplate(), "", true)
	if on.Path != "genai" || on.Location != GenaiLocation {
		t.Errorf("detail-on rubric target = %+v, want genai/%s", on, GenaiLocation)
	}
}

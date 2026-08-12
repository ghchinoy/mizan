package registry

import (
	"reflect"
	"strings"
	"testing"
)

// --- G1: codec pairwise kind-specific fields round-trip ----------------------
func TestYAMLCodecPairwiseRoundTrip(t *testing.T) {
	c := NewYAMLCodec()
	in := &MetricTemplate{
		ID:                   "acme/compare",
		Kind:                 KindPairwise,
		MetricPromptTemplate: "compare {{candidate}} vs {{baseline}}",
		CandidateFieldName:   "candidate",
		BaselineFieldName:    "baseline",
		FlipEnabled:          true,
	}
	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.CandidateFieldName != "candidate" || got.BaselineFieldName != "baseline" {
		t.Errorf("pairwise fields lost: candidate=%q baseline=%q", got.CandidateFieldName, got.BaselineFieldName)
	}
	if !got.FlipEnabled {
		t.Error("FlipEnabled lost in round-trip")
	}
	if got.Kind != KindPairwise {
		t.Errorf("Kind = %q, want pairwise", got.Kind)
	}
}

// --- G2: codec rubric groups round-trip --------------------------------------
func TestYAMLCodecRubricGroupsRoundTrip(t *testing.T) {
	c := NewYAMLCodec()
	in := &MetricTemplate{
		ID:                   "acme/rubric",
		Kind:                 KindRubric,
		MetricPromptTemplate: "score {{response}}",
		RubricGroups: map[string][]string{
			"clarity":  {"unambiguous", "concise"},
			"accuracy": {"factually correct"},
		},
	}
	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got.RubricGroups, in.RubricGroups) {
		t.Errorf("RubricGroups round-trip:\n got=%v\nwant=%v", got.RubricGroups, in.RubricGroups)
	}
}

// --- G3: codec RFC-0001 additive fields (RatingRubric/RubricDetail) -----------
func TestYAMLCodecAdditiveFieldsRoundTrip(t *testing.T) {
	c := NewYAMLCodec()
	in := &MetricTemplate{
		ID:                   "acme/additive",
		Kind:                 KindRubric,
		MetricPromptTemplate: "score {{response}}",
		RubricGroups:         map[string][]string{"clarity": {"clear"}},
		RatingRubric: map[string]map[string]string{
			"clarity": {"1": "poor", "5": "excellent"},
		},
		RubricDetail: &RubricDetail{Scale: &RubricScale{Min: 1, Max: 7}},
	}
	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got.RatingRubric, in.RatingRubric) {
		t.Errorf("RatingRubric round-trip:\n got=%v\nwant=%v", got.RatingRubric, in.RatingRubric)
	}
	if !reflect.DeepEqual(got.RubricDetail, in.RubricDetail) {
		t.Errorf("RubricDetail round-trip:\n got=%+v\nwant=%+v", got.RubricDetail, in.RubricDetail)
	}
}

// --- G4: contentHash sensitivity to every authored spec field ----------------
func TestContentHashSensitivePerField(t *testing.T) {
	base := &MetricTemplate{
		ID:                   "acme/thing",
		Kind:                 KindPairwise,
		MetricPromptTemplate: "compare {{candidate}} vs {{baseline}}",
		CandidateFieldName:   "candidate",
		BaselineFieldName:    "baseline",
		RubricGroups:         map[string][]string{"g": {"c"}},
		RatingRubric:         map[string]map[string]string{"g": {"1": "a"}},
		RubricDetail:         &RubricDetail{Scale: &RubricScale{Min: 1, Max: 5}},
		AutoraterModel:       "gemini-2.5-pro",
		SamplingCount:        4,
	}
	h0 := contentHash(base)
	mutate := map[string]func(*MetricTemplate){
		"CandidateFieldName": func(t *MetricTemplate) { t.CandidateFieldName = "other" },
		"BaselineFieldName":  func(t *MetricTemplate) { t.BaselineFieldName = "other" },
		"RubricGroups":       func(t *MetricTemplate) { t.RubricGroups = map[string][]string{"g": {"c", "d"}} },
		"RatingRubric":       func(t *MetricTemplate) { t.RatingRubric = map[string]map[string]string{"g": {"1": "b"}} },
		"RubricDetail":       func(t *MetricTemplate) { t.RubricDetail = &RubricDetail{Scale: &RubricScale{Min: 1, Max: 9}} },
		"AutoraterModel":     func(t *MetricTemplate) { t.AutoraterModel = "gemini-2.5-flash" },
		"SamplingCount":      func(t *MetricTemplate) { t.SamplingCount = 8 },
	}
	for name, fn := range mutate {
		t.Run(name, func(t *testing.T) {
			cp := *base
			fn(&cp)
			if h := contentHash(&cp); h == h0 {
				t.Errorf("contentHash did not change after mutating %s", name)
			}
		})
	}
}

// --- G5: Marshal drops authored comments -------------------------------------
func TestYAMLCodecMarshalStripsComments(t *testing.T) {
	c := NewYAMLCodec()
	orig, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := c.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(out)
	for _, frag := range []string{"STABLE global id", "COMPUTED by Mizan", "double-brace", "publisher-relative"} {
		if strings.Contains(s, frag) {
			t.Errorf("marshaled YAML leaked authored comment fragment %q:\n%s", frag, s)
		}
	}
	if strings.Contains(s, "# ") {
		t.Errorf("marshaled YAML contains a comment marker:\n%s", s)
	}
}

// --- G6: malformed (syntactically invalid) YAML errors -----------------------
func TestYAMLCodecMalformedYAML(t *testing.T) {
	c := NewYAMLCodec()
	src := []byte("apiVersion: mizan.dev/v1alpha1\nkind: MetricTemplate\nmetadata:\n  id: [unterminated\nspec:\n  kind: pointwise\n")
	if _, err := c.Unmarshal(src); err == nil {
		t.Fatal("expected parse error for malformed YAML, got nil")
	}
}

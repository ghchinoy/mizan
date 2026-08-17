package registry

// rubric_provenance_test.go covers the Phase-2 additive RubricProvenance field
// (design §4.4, Decisions 2 & 3): it is persisted (codec round-trip), included in
// the content hash (editing it shifts the hash) WITHOUT disturbing the exclusion
// of the registry-level lifecycle provenance block, and strict-schema-valid.

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// provTemplate is a minimal generated KindRubric template carrying a fully
// populated RubricProvenance (the shape both CUJ 7 and CUJ 8 stamp).
func provTemplate() MetricTemplate {
	return MetricTemplate{
		ID:                   "acme/quality",
		Name:                 "Adaptive rubric: general_quality",
		Version:              "0.1.0",
		Kind:                 KindRubric,
		Modalities:           []Modality{ModalityText},
		MetricPromptTemplate: "Assess the response: {{response}}",
		Inputs:               []InputSpec{{Name: "response", Modality: ModalityText, Required: true}},
		RubricGroups: map[string][]string{
			"general_quality": {"Answers the question directly", "Is free of jargon"},
		},
		RubricProvenance: &RubricProvenance{
			Method:         "adaptive-generated",
			GeneratorModel: "gemini-2.5-flash",
			Recipe:         "general_quality_v1",
			SampleInputRef: `inline:"Explain the offer" sha256:abc123`,
			GeneratedAt:    time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
			APIVersion:     "v1beta1:generateInstanceRubrics",
			RubricMeta: []RubricMeta{
				{Group: "general_quality", Criterion: "Answers the question directly", Type: "CONTENT", Importance: "HIGH"},
				{Group: "general_quality", Criterion: "Is free of jargon", Type: "STYLE", Importance: "MEDIUM"},
			},
		},
	}
}

// TestRubricProvenanceCodecRoundTrip proves the codec marshals and re-parses the
// full provenance record losslessly, so a draft/frozen template's provenance
// survives pack export→import.
func TestRubricProvenanceCodecRoundTrip(t *testing.T) {
	c := NewYAMLCodec()
	orig := provTemplate()

	data, err := c.Marshal(&orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), "rubricProvenance") {
		t.Fatalf("marshaled YAML missing rubricProvenance:\n%s", data)
	}

	round, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round.RubricProvenance == nil {
		t.Fatalf("provenance dropped on round-trip:\n%s", data)
	}
	got, want := *round.RubricProvenance, *orig.RubricProvenance
	if !got.GeneratedAt.Equal(want.GeneratedAt) {
		t.Errorf("GeneratedAt round-trip: got %v, want %v", got.GeneratedAt, want.GeneratedAt)
	}
	// Compare the non-time fields structurally (time.Time equality is via .Equal).
	got.GeneratedAt, want.GeneratedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("provenance round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

// TestRubricProvenanceOmittedWhenNil proves a hand-authored template (nil
// provenance) emits no rubricProvenance key — no migration, no spurious keys.
func TestRubricProvenanceOmittedWhenNil(t *testing.T) {
	c := NewYAMLCodec()
	tmpl := provTemplate()
	tmpl.RubricProvenance = nil
	data, err := c.Marshal(&tmpl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "rubricProvenance") {
		t.Errorf("nil provenance should be omitted, got:\n%s", data)
	}
}

// TestContentHashIncludesRubricProvenance proves Decision 3: adding provenance
// shifts the hash, and editing ANY provenance field shifts it again.
func TestContentHashIncludesRubricProvenance(t *testing.T) {
	base := provTemplate()
	base.RubricProvenance = nil
	hNil := contentHash(&base)

	withProv := provTemplate()
	hProv := contentHash(&withProv)
	if hNil == hProv {
		t.Fatal("adding RubricProvenance did not change the content hash (Decision 3)")
	}

	// Editing a scalar provenance field shifts the hash.
	edited := provTemplate()
	edited.RubricProvenance.Recipe = "general_quality_v2"
	if h := contentHash(&edited); h == hProv {
		t.Error("editing RubricProvenance.Recipe did not change the hash")
	}

	// Editing the preserved per-criterion meta shifts the hash.
	editedMeta := provTemplate()
	editedMeta.RubricProvenance.RubricMeta[0].Importance = "LOW"
	if h := contentHash(&editedMeta); h == hProv {
		t.Error("editing RubricProvenance.RubricMeta did not change the hash")
	}

	// Editing the generation timestamp shifts the hash (it is generation content).
	editedTime := provTemplate()
	editedTime.RubricProvenance.GeneratedAt = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if h := contentHash(&editedTime); h == hProv {
		t.Error("editing RubricProvenance.GeneratedAt did not change the hash")
	}
}

// TestContentHashStillExcludesLifecycleProvenanceWithRubricProvenance proves that
// including RubricProvenance in the hash did NOT accidentally start hashing the
// registry-level lifecycle block (Source/Dirty/timestamps) — those stay excluded.
func TestContentHashStillExcludesLifecycleProvenanceWithRubricProvenance(t *testing.T) {
	a := provTemplate()
	b := provTemplate()
	b.Source = "pack:acme@/some/path"
	b.ContentHash = "sha256:whatever"
	b.Dirty = true
	b.CreatedAt = b.CreatedAt.AddDate(1, 0, 0)
	b.UpdatedAt = b.UpdatedAt.AddDate(1, 0, 0)
	b.ImportedAt = b.ImportedAt.AddDate(1, 0, 0)
	if contentHash(&a) != contentHash(&b) {
		t.Error("registry-level lifecycle provenance leaked into the content hash")
	}
}

// TestRubricProvenanceSchemaValid proves a marshaled provenance-bearing template
// satisfies the strict pack schema (additive optional object), and that an
// unknown key under rubricProvenance is rejected (additionalProperties:false).
func TestRubricProvenanceSchemaValid(t *testing.T) {
	c := NewYAMLCodec()
	tmpl := provTemplate()
	data, err := c.Marshal(&tmpl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := ValidateTemplateSchema(data); err != nil {
		t.Fatalf("provenance-bearing template violated schema: %v\n%s", err, data)
	}

	bad := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/quality
  version: 0.1.0
spec:
  kind: rubric
  rubricGroups:
    general_quality:
      - Answers the question directly
  rubricProvenance:
    method: adaptive-generated
    bogusKey: nope
`
	if err := ValidateTemplateSchema([]byte(bad)); err == nil {
		t.Error("unknown key under rubricProvenance should fail strict schema (additionalProperties:false)")
	}
}

// TestContentHashIncludesRubricMetaOrigin proves the additive per-criterion Origin
// (CUJ 9 union provenance, design §6.5 Option A) is part of the content hash WHEN
// PRESENT: two templates identical but for a RubricMeta.Origin value hash
// differently, so a mixed-origin audit record cannot be silently altered. (Because
// Origin is omitempty and the hand-authored golden fixture carries no rubricMeta,
// the golden content hash is UNSHIFTED — see hash_test.go's TestContentHashGolden.)
func TestContentHashIncludesRubricMetaOrigin(t *testing.T) {
	base := provTemplate()
	hBase := contentHash(&base)

	withOrigin := provTemplate()
	withOrigin.RubricProvenance.RubricMeta[0].Origin = OriginHandAuthored
	if contentHash(&withOrigin) == hBase {
		t.Error("setting RubricMeta.Origin did not change the content hash (it must be hashed when present)")
	}
}

// TestRubricMetaOriginSchema proves the additive origin enum is accepted for the
// two valid values and rejected for anything else (strict schema discipline).
func TestRubricMetaOriginSchema(t *testing.T) {
	valid := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/quality
  version: 0.1.0
spec:
  kind: rubric
  rubricGroups:
    general_quality:
      - Answers the question directly
  rubricProvenance:
    method: adaptive-generated
    rubricMeta:
      - group: general_quality
        criterion: Answers the question directly
        origin: adaptive-generated
      - group: general_quality
        criterion: Uses the brand palette
        origin: hand-authored
`
	if err := ValidateTemplateSchema([]byte(valid)); err != nil {
		t.Fatalf("valid origin values rejected by schema: %v", err)
	}
	bad := strings.Replace(valid, "origin: hand-authored", "origin: bogus-origin", 1)
	if err := ValidateTemplateSchema([]byte(bad)); err == nil {
		t.Error("invalid origin value should fail the strict schema enum")
	}
}

// TestRubricProvenanceMaxLengthEnforced proves the pragmatic maxLength bounds
// (audit LOW / CWE-770) reject an over-length provenance string: neither an
// attacker-influenceable sampleInputRef nor a rubricMeta scalar can smuggle an
// unbounded blob into persisted, hashed YAML.
func TestRubricProvenanceMaxLengthEnforced(t *testing.T) {
	header := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/quality
  version: 0.1.0
spec:
  kind: rubric
  rubricGroups:
    general_quality:
      - Answers the question directly
  rubricProvenance:
`
	cases := []struct {
		name string
		body string
	}{
		{"method>256", "    method: " + strings.Repeat("m", 257) + "\n"},
		{"sampleInputRef>4096", "    sampleInputRef: " + strings.Repeat("s", 4097) + "\n"},
		{"rubricMeta.type>128", "    rubricMeta:\n      - type: " + strings.Repeat("t", 129) + "\n"},
		{"rubricMeta.importance>128", "    rubricMeta:\n      - importance: " + strings.Repeat("i", 129) + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := header + tc.body
			if err := ValidateTemplateSchema([]byte(doc)); err == nil {
				t.Errorf("%s: over-length value should fail strict schema (maxLength)", tc.name)
			}
		})
	}
}

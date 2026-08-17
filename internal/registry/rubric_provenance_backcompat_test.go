package registry

// rubric_provenance_backcompat_test.go is the FOCUSED back-compat regression for
// the additive RubricProvenance field (design §4.4, Decision 3), asserted against
// the REAL hand-authored on-disk pack fixture (video-brand-alignment.yaml) rather
// than a synthetic struct. It nails the two properties an existing template must
// keep after Phase 2 lands:
//
//   - a hand-authored template (nil provenance) round-trips through the codec
//     WITHOUT gaining a spurious `rubricProvenance: null` (or any rubricProvenance
//     key) on disk — no migration, no format churn; and
//   - the round-tripped bytes still satisfy the strict pack schema.
//
// The provenance-bearing round-trip / hash / schema behavior is covered by
// rubric_provenance_test.go; this file guards the untouched-existing-template path.

import (
	"strings"
	"testing"
	"time"
)

// TestHandAuthoredFixtureNoSpuriousProvenanceKey proves the real committed
// hand-authored fixture Unmarshals with nil RubricProvenance, re-Marshals WITHOUT
// emitting a rubricProvenance key, and the re-marshaled bytes remain schema-valid.
func TestHandAuthoredFixtureNoSpuriousProvenanceKey(t *testing.T) {
	c := NewYAMLCodec()

	tmpl, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal fixture: %v", err)
	}
	if tmpl.RubricProvenance != nil {
		t.Fatalf("hand-authored fixture parsed with non-nil RubricProvenance: %+v", tmpl.RubricProvenance)
	}

	data, err := c.Marshal(tmpl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "rubricProvenance") {
		t.Errorf("nil provenance leaked a rubricProvenance key onto disk (no migration expected):\n%s", data)
	}

	// The re-marshaled hand-authored template still satisfies the strict schema.
	if err := ValidateTemplateSchema(data); err != nil {
		t.Fatalf("round-tripped hand-authored template violated schema: %v\n%s", err, data)
	}
}

// TestRubricProvenanceCustomPromptAndEmptyMetaRoundTrip covers the additive
// object's remaining fields not exercised by provTemplate(): a populated
// PromptTemplate (the future custom-generation-prompt path persists here) and an
// EMPTY RubricMeta (omitempty must drop it, not emit `rubricMeta: []`). The record
// must round-trip losslessly and stay schema-valid.
func TestRubricProvenanceCustomPromptAndEmptyMetaRoundTrip(t *testing.T) {
	c := NewYAMLCodec()
	tmpl := provTemplate()
	tmpl.RubricProvenance.PromptTemplate = "Draft criteria for: {{prompt}}"
	tmpl.RubricProvenance.RubricMeta = nil // empty meta -> omitted, not `[]`

	data, err := c.Marshal(&tmpl)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(data), "promptTemplate") {
		t.Errorf("populated PromptTemplate did not serialize:\n%s", data)
	}
	if strings.Contains(string(data), "rubricMeta") {
		t.Errorf("empty RubricMeta should be omitted (omitempty), got:\n%s", data)
	}
	if err := ValidateTemplateSchema(data); err != nil {
		t.Fatalf("custom-prompt provenance violated schema: %v\n%s", err, data)
	}

	round, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if round.RubricProvenance == nil {
		t.Fatalf("provenance dropped on round-trip:\n%s", data)
	}
	if round.RubricProvenance.PromptTemplate != "Draft criteria for: {{prompt}}" {
		t.Errorf("PromptTemplate round-trip = %q, want the custom prompt", round.RubricProvenance.PromptTemplate)
	}
	if len(round.RubricProvenance.RubricMeta) != 0 {
		t.Errorf("RubricMeta round-trip = %+v, want empty", round.RubricProvenance.RubricMeta)
	}
}

// TestContentHashDeterministicForFixedProvenance guards that the content hash is a
// PURE function of the template: with a FIXED GeneratedAt (as a frozen/persisted
// template carries), repeated hashing yields the identical value. The Phase-2
// golden shift (adding the hashed rubricProvenance field) is therefore one-time,
// not wall-clock-dependent — GeneratedAt only moves the hash when it actually
// changes, never nondeterministically between calls on the same template.
func TestContentHashDeterministicForFixedProvenance(t *testing.T) {
	tmpl := provTemplate()
	tmpl.RubricProvenance.GeneratedAt = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

	h1 := contentHash(&tmpl)
	h2 := contentHash(&tmpl)
	if h1 != h2 {
		t.Fatalf("contentHash nondeterministic for a fixed-provenance template: %q != %q", h1, h2)
	}
}

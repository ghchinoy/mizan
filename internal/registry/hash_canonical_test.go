package registry

// hash_canonical_test.go covers the P2.1 review #2 fold-in: contentHash
// canonicalizes a custom_schema template's ResponseSchema.JSON (sorts keys)
// before hashing, so two templates that differ ONLY in JSON key order hash
// identically and a CLI-authored template's hash does not drift on a later
// export→import.

import "testing"

func TestContentHashCanonicalizesResponseSchema(t *testing.T) {
	base := MetricTemplate{
		ID:         "acme/schema-metric",
		Kind:       KindCustomSchema,
		Modalities: []Modality{ModalityText},
	}

	unsorted := base
	unsorted.ResponseSchema = &Schema{JSON: `{"zeta":1,"alpha":2,"mid":{"y":1,"x":2}}`}

	sorted := base
	sorted.ResponseSchema = &Schema{JSON: `{"alpha":2,"mid":{"x":2,"y":1},"zeta":1}`}

	if got, want := contentHash(&unsorted), contentHash(&sorted); got != want {
		t.Fatalf("contentHash differs on key order alone:\n unsorted=%s\n sorted  =%s", got, want)
	}
}

func TestCanonicalizeJSONInvalidPassthrough(t *testing.T) {
	// A non-JSON value degrades to the raw string rather than being dropped.
	raw := "not json at all"
	if got := canonicalizeJSON(raw); got != raw {
		t.Fatalf("canonicalizeJSON(%q) = %q, want passthrough", raw, got)
	}
}

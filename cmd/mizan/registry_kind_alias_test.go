package main

// registry_kind_alias_test.go covers the vernacular kind-VALUE aliases at the
// registry authoring flag boundary (ITEM C): `--kind single` == `--kind
// pointwise` and `--kind compare` == `--kind pairwise`, an unknown kind still
// errors clearly, and the canonical spellings are unchanged. Normalizing at
// apply() means t.Kind is always canonical, so the eval engine and the Vertex
// request specs it builds (PointwiseMetricSpec / PairwiseMetricSpec) never see
// an alias.

import (
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

func TestKindFlagAliasesNormalize(t *testing.T) {
	cases := []struct {
		spelling string
		want     registry.MetricKind
	}{
		{"single", registry.KindPointwise},
		{"pointwise", registry.KindPointwise},
		{"compare", registry.KindPairwise},
		{"pairwise", registry.KindPairwise},
	}
	for _, tc := range cases {
		got, err := applyFromArgs(t, false, nil, "--kind", tc.spelling)
		if err != nil {
			t.Fatalf("apply --kind %q: %v", tc.spelling, err)
		}
		if got.Kind != tc.want {
			t.Errorf("--kind %q -> Kind %q, want %q", tc.spelling, got.Kind, tc.want)
		}
	}
}

// TestKindFlagSingleEqualsPointwise pins the equivalence explicitly: the alias
// and its canonical spelling produce byte-identical stored kinds.
func TestKindFlagSingleEqualsPointwise(t *testing.T) {
	single, err := applyFromArgs(t, false, nil, "--kind", "single")
	if err != nil {
		t.Fatalf("apply --kind single: %v", err)
	}
	pointwise, err := applyFromArgs(t, false, nil, "--kind", "pointwise")
	if err != nil {
		t.Fatalf("apply --kind pointwise: %v", err)
	}
	if single.Kind != pointwise.Kind {
		t.Errorf("--kind single (%q) != --kind pointwise (%q)", single.Kind, pointwise.Kind)
	}

	compare, err := applyFromArgs(t, false, nil, "--kind", "compare")
	if err != nil {
		t.Fatalf("apply --kind compare: %v", err)
	}
	pairwise, err := applyFromArgs(t, false, nil, "--kind", "pairwise")
	if err != nil {
		t.Fatalf("apply --kind pairwise: %v", err)
	}
	if compare.Kind != pairwise.Kind {
		t.Errorf("--kind compare (%q) != --kind pairwise (%q)", compare.Kind, pairwise.Kind)
	}
}

func TestKindFlagUnknownErrors(t *testing.T) {
	_, err := applyFromArgs(t, false, nil, "--kind", "bogus")
	if err == nil {
		t.Fatal("apply --kind bogus: nil error, want an error")
	}
	if !strings.Contains(err.Error(), "unknown metric kind") {
		t.Errorf("error = %v, want it to mention unknown metric kind", err)
	}
	// The message must enumerate the accepted spellings (including the aliases)
	// so a typo teaches the user the full menu at the parse boundary. Pin the
	// enumeration so the guidance can't silently regress.
	for _, want := range []string{"single|pointwise", "compare|pairwise", "rubric", "custom_schema"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing enumerated option %q", err.Error(), want)
		}
	}
}

// TestKindFlagAliasNormalizesOnUpdate proves the vernacular aliases are also
// folded on the UPDATE path — not just create. apply() gates the kind assignment
// by hand (`!update || changed("kind")`) instead of the plain set() helper
// because it can error, so the update branch needs its own coverage: an update
// that sets `--kind compare` must store the canonical KindPairwise.
func TestKindFlagAliasNormalizesOnUpdate(t *testing.T) {
	base := &registry.MetricTemplate{ID: "test/p", Kind: registry.KindPointwise}
	got, err := applyFromArgs(t, true, base, "--kind", "compare")
	if err != nil {
		t.Fatalf("update --kind compare: %v", err)
	}
	if got.Kind != registry.KindPairwise {
		t.Errorf("update --kind compare -> Kind %q, want %q", got.Kind, registry.KindPairwise)
	}
}

// TestKindFlagUpdatePreservesExistingKind proves an update that does NOT pass
// --kind leaves the stored canonical kind untouched (rather than resetting it to
// the flag default, pointwise). This guards the `!update || changed("kind")`
// gating: without it, every metadata-only update would silently rewrite a
// pairwise/rubric/custom_schema template back to pointwise.
func TestKindFlagUpdatePreservesExistingKind(t *testing.T) {
	for _, kind := range []registry.MetricKind{
		registry.KindPairwise, registry.KindRubric, registry.KindCustomSchema,
	} {
		base := &registry.MetricTemplate{ID: "test/x", Kind: kind}
		got, err := applyFromArgs(t, true, base, "--name", "Renamed")
		if err != nil {
			t.Fatalf("update (kind=%s) without --kind: %v", kind, err)
		}
		if got.Kind != kind {
			t.Errorf("update without --kind changed Kind %q -> %q; want it preserved", kind, got.Kind)
		}
	}
}

// TestKindFlagAliasEqualsCanonicalOnUpdate pins the update-path equivalence: an
// update via the alias and via the canonical spelling store byte-identical kinds.
func TestKindFlagAliasEqualsCanonicalOnUpdate(t *testing.T) {
	base := &registry.MetricTemplate{ID: "test/p", Kind: registry.KindPointwise}
	viaAlias, err := applyFromArgs(t, true, base, "--kind", "compare")
	if err != nil {
		t.Fatalf("update --kind compare: %v", err)
	}
	viaCanonical, err := applyFromArgs(t, true, base, "--kind", "pairwise")
	if err != nil {
		t.Fatalf("update --kind pairwise: %v", err)
	}
	if viaAlias.Kind != viaCanonical.Kind {
		t.Errorf("update --kind compare (%q) != --kind pairwise (%q)", viaAlias.Kind, viaCanonical.Kind)
	}
}

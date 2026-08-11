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
}

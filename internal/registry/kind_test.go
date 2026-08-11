package registry

// kind_test.go covers NormalizeKind — the single parse-boundary normalization
// that folds the vernacular aliases (single/compare) to the canonical
// MetricKind and rejects unknown spellings (ITEM C).

import "testing"

func TestNormalizeKindAliases(t *testing.T) {
	cases := []struct {
		in   string
		want MetricKind
	}{
		// Vernacular aliases fold to their canonical kind.
		{"single", KindPointwise},
		{"compare", KindPairwise},
		// Canonical kinds pass through unchanged (backward compatible).
		{"pointwise", KindPointwise},
		{"pairwise", KindPairwise},
		{"rubric", KindRubric},
		{"custom_schema", KindCustomSchema},
	}
	for _, tc := range cases {
		got, err := NormalizeKind(tc.in)
		if err != nil {
			t.Errorf("NormalizeKind(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeKindUnknownErrors(t *testing.T) {
	for _, bad := range []string{"", "bogus", "Pointwise", "SINGLE", "compare "} {
		if got, err := NormalizeKind(bad); err == nil {
			t.Errorf("NormalizeKind(%q) = %q, nil error; want an error", bad, got)
		}
	}
}

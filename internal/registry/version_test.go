package registry

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b    string
		wantCmp int
		wantOK  bool
	}{
		{"1.0.0", "1.0.0", 0, true},
		{"2.0.0", "1.0.0", 1, true},
		{"1.0.0", "2.0.0", -1, true},
		{"1.2.0", "1.1.9", 1, true},
		{"1.0.1", "1.0.0", 1, true},
		{"v1.0.0", "1.0.0", 0, true},      // leading v tolerated
		{"1.0.0", "1.0.0+build", 0, true}, // build metadata ignored
		{"1.0.0-rc1", "1.0.0", -1, true},  // pre-release < release
		{"1.0.0-rc1", "1.0.0-rc2", -1, true},
		{"1.0.0-rc2", "1.0.0-rc1", 1, true},
		{"1.2", "1.2.0", 0, true}, // missing patch treated as 0
		{"", "1.0.0", 0, false},   // unparseable -> not comparable
		{"1.0.0", "not-semver", 0, false},
		{"abc", "def", 0, false},

		// --- adversarial pre-release ordering (semver §11) ---
		// numeric identifiers rank BELOW alphanumeric ones.
		{"1.0.0-1", "1.0.0-alpha", -1, true},
		{"1.0.0-alpha", "1.0.0-1", 1, true},
		// a longer pre-release list wins the tie when the common prefix is equal.
		{"1.0.0-alpha", "1.0.0-alpha.1", -1, true},
		{"1.0.0-alpha.1", "1.0.0-alpha", 1, true},
		// multi-identifier lexical comparison ("1" numeric < "beta" alphanumeric).
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1, true},
		{"1.0.0-alpha.beta", "1.0.0-alpha.1", 1, true},
		// two numeric identifiers compared numerically, not lexically (2 < 10).
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1, true},
		{"1.0.0-alpha.10", "1.0.0-alpha.2", 1, true}, // and the reverse direction
		{"1.0.0-2", "1.0.0-2", 0, true},              // equal numeric identifiers
		// pre-release + build metadata: build stripped, so equal to the bare pre.
		{"1.0.0-rc1+build.99", "1.0.0-rc1", 0, true},

		// --- lenient-but-valid parsing ---
		{"1", "1.0.0", 0, true},       // missing minor AND patch treated as 0
		{"V1.0.0", "1.0.0", 0, true},  // uppercase leading V tolerated
		{" 1.0.0 ", "1.0.0", 0, true}, // surrounding whitespace trimmed
		{"01.0.0", "1.0.0", 0, true},  // leading zeros accepted (advisory, D1)

		// --- rejection: unparseable -> not comparable (never guess an order) ---
		{"1.2.3.4", "1.0.0", 0, false},                    // too many components
		{"1.x.0", "1.0.0", 0, false},                      // non-numeric core
		{"99999999999999999999999999", "1.0.0", 0, false}, // integer overflow, no panic
		{"1.0.0-", "1.0.0", 0, false},                     // empty pre-release
		{"1.0.0", "1.0.0-", 0, false},                     // empty pre-release (rhs)
	}
	for _, c := range cases {
		gotCmp, gotOK := compareVersions(c.a, c.b)
		if gotOK != c.wantOK {
			t.Errorf("compareVersions(%q,%q) ok = %v, want %v", c.a, c.b, gotOK, c.wantOK)
			continue
		}
		if c.wantOK && gotCmp != c.wantCmp {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, gotCmp, c.wantCmp)
		}
	}
}

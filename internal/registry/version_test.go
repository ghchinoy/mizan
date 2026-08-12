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

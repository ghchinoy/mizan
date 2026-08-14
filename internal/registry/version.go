package registry

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateSemver reports whether s parses as a semantic version, reusing the
// SAME parser (parseSemver) that import reconciliation uses to order versions
// (design/collaboration-design.md §3.8). It is the exported guard the CLI's
// `registry create`/`update` --version flag calls so an authored version is
// rejected with a clear error at authoring time rather than only being flagged
// later by `pack validate` or silently treated as incomparable at import.
func ValidateSemver(s string) error {
	if _, ok := parseSemver(s); !ok {
		return fmt.Errorf("%q is not valid semver (want MAJOR.MINOR.PATCH, e.g. 0.1.0)", s)
	}
	return nil
}

// compareVersions orders two template semver strings for import reconciliation
// (design/collaboration-design.md §3.8). It returns (cmp, ok) where cmp is -1, 0,
// or +1 for a < b, a == b, a > b, and ok reports whether BOTH strings parsed as
// semver and were therefore comparable.
//
// A leading "v" is tolerated. Comparison follows semver precedence on the numeric
// MAJOR.MINOR.PATCH core and then on pre-release identifiers (a version WITH a
// pre-release has LOWER precedence than the same core without one; per D1 version
// enforcement is advisory, so build metadata after "+" is ignored).
//
// When either string is not valid semver, ok is false: the caller treats an
// incomparable pair with differing content as a conflict rather than guessing an
// order, which is the safe, never-silently-clobber posture (D2).
func compareVersions(a, b string) (cmp int, ok bool) {
	ca, oka := parseSemver(a)
	cb, okb := parseSemver(b)
	if !oka || !okb {
		return 0, false
	}
	return ca.compare(cb), true
}

// semver is a parsed semantic version: the numeric core plus optional
// pre-release identifiers. Build metadata is discarded (it does not affect
// precedence).
type semver struct {
	core [3]int
	pre  []string
}

// parseSemver parses "[v]MAJOR.MINOR.PATCH[-pre][+build]". Missing MINOR/PATCH are
// treated as 0 so a bare "1" or "1.2" still compares; any non-numeric core
// component makes it unparseable (ok=false).
func parseSemver(s string) (semver, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return semver{}, false
	}
	if s[0] == 'v' || s[0] == 'V' {
		s = s[1:]
	}
	// Strip build metadata (ignored for precedence).
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre []string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		preStr := s[i+1:]
		s = s[:i]
		if preStr == "" {
			return semver{}, false
		}
		pre = strings.Split(preStr, ".")
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return semver{}, false
	}
	var out semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		out.core[i] = n
	}
	out.pre = pre
	return out, true
}

// compare returns -1, 0, or +1 comparing v to o by semver precedence.
func (v semver) compare(o semver) int {
	for i := 0; i < 3; i++ {
		if v.core[i] != o.core[i] {
			if v.core[i] < o.core[i] {
				return -1
			}
			return 1
		}
	}
	return comparePre(v.pre, o.pre)
}

// comparePre compares two pre-release identifier lists per semver §11: a version
// with NO pre-release ranks higher than one with a pre-release; otherwise
// identifiers are compared left to right, numeric ones numerically and others
// lexically, numeric ranking below non-numeric, longer lists winning ties.
func comparePre(a, b []string) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 { // a is a release, b is a pre-release
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePreIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func comparePreIdent(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil: // both numeric
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	case aErr == nil: // numeric identifiers rank below non-numeric
		return -1
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

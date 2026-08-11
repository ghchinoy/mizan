package main

// registry_kind_alias_e2e_test.go drives the vernacular kind aliases through the
// REAL cobra commands end-to-end against a throwaway SQLite registry (no project
// or network needed — registry create/list only touch the local DB). It closes
// the two boundaries the in-process apply()/NormalizeKind unit tests don't reach
// on their own: `registry create --kind <alias>` persists the canonical kind,
// and the `registry list --kind <alias>` FILTER folds the alias before querying.
// These are cgo-free (CGO_ENABLED=0 SQLite driver) and hermetic.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// useIsolatedRegistry blanks the config environment (so no real .env leaks in)
// and points the registry at a fresh SQLite file for the test.
func useIsolatedRegistry(t *testing.T) {
	t.Helper()
	cleanConfigEnv(t)
	t.Setenv("MIZAN_REGISTRY_DB", filepath.Join(t.TempDir(), "registry.db"))
}

// listKinds runs `registry list --kind <spelling> --output json` and returns the
// stored kinds of the matched templates.
func listKinds(t *testing.T, spelling string) []registry.MetricKind {
	t.Helper()
	out, err := executeRoot(t, "--output", "json", "registry", "list", "--kind", spelling)
	if err != nil {
		t.Fatalf("registry list --kind %s: %v (out=%q)", spelling, err, out)
	}
	var got []registry.MetricTemplate
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode list output %q: %v", out, err)
	}
	kinds := make([]registry.MetricKind, 0, len(got))
	for _, tmpl := range got {
		kinds = append(kinds, tmpl.Kind)
	}
	return kinds
}

// TestRegistryCreateWithKindAliasPersistsCanonical proves `registry create
// --kind single|compare` stores the canonical kind in the registry, so anything
// reading the template later (including the eval engine) sees only the canonical
// spelling.
func TestRegistryCreateWithKindAliasPersistsCanonical(t *testing.T) {
	cases := []struct {
		id       string
		spelling string
		want     registry.MetricKind
	}{
		{"test/single-metric", "single", registry.KindPointwise},
		{"test/compare-metric", "compare", registry.KindPairwise},
	}
	for _, tc := range cases {
		t.Run(tc.spelling, func(t *testing.T) {
			useIsolatedRegistry(t)
			out, err := executeRoot(t, "--output", "json",
				"registry", "create",
				"--id", tc.id,
				"--kind", tc.spelling,
				"--prompt", "Judge {{response}}",
			)
			if err != nil {
				t.Fatalf("registry create --kind %s: %v (out=%q)", tc.spelling, err, out)
			}
			var created registry.MetricTemplate
			if err := json.Unmarshal([]byte(out), &created); err != nil {
				t.Fatalf("decode create output %q: %v", out, err)
			}
			if created.Kind != tc.want {
				t.Errorf("created template Kind = %q, want canonical %q", created.Kind, tc.want)
			}
		})
	}
}

// TestRegistryListKindAliasFiltersSameAsCanonical proves the list --kind filter
// accepts the aliases and folds them to the canonical kind: `--kind single`
// returns exactly the pointwise templates (same as `--kind pointwise`) and
// `--kind compare` returns exactly the pairwise templates (same as `--kind
// pairwise`).
func TestRegistryListKindAliasFiltersSameAsCanonical(t *testing.T) {
	useIsolatedRegistry(t)

	// Seed one pointwise and one pairwise template (authored via canonical
	// spellings so the filter — not the create path — is what's under test).
	if out, err := executeRoot(t, "registry", "create",
		"--id", "test/pw", "--kind", "pointwise", "--prompt", "Judge {{response}}"); err != nil {
		t.Fatalf("seed pointwise: %v (out=%q)", err, out)
	}
	if out, err := executeRoot(t, "registry", "create",
		"--id", "test/pair", "--kind", "pairwise", "--prompt", "Compare {{baseline}} {{candidate}}"); err != nil {
		t.Fatalf("seed pairwise: %v (out=%q)", err, out)
	}

	single := listKinds(t, "single")
	pointwise := listKinds(t, "pointwise")
	if len(single) != 1 || single[0] != registry.KindPointwise {
		t.Errorf("list --kind single = %v, want exactly [pointwise]", single)
	}
	if !equalKinds(single, pointwise) {
		t.Errorf("list --kind single (%v) != list --kind pointwise (%v)", single, pointwise)
	}

	compare := listKinds(t, "compare")
	pairwise := listKinds(t, "pairwise")
	if len(compare) != 1 || compare[0] != registry.KindPairwise {
		t.Errorf("list --kind compare = %v, want exactly [pairwise]", compare)
	}
	if !equalKinds(compare, pairwise) {
		t.Errorf("list --kind compare (%v) != list --kind pairwise (%v)", compare, pairwise)
	}
}

// TestRegistryListKindUnknownErrors proves an unknown --kind filter value is
// rejected with the enumerated message (the same NormalizeKind guard as create).
func TestRegistryListKindUnknownErrors(t *testing.T) {
	useIsolatedRegistry(t)
	out, err := executeRoot(t, "registry", "list", "--kind", "bogus")
	if err == nil {
		t.Fatalf("registry list --kind bogus: nil error, want an error (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "unknown metric kind") {
		t.Errorf("error = %v, want it to mention unknown metric kind", err)
	}
}

func equalKinds(a, b []registry.MetricKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package main

import (
	"strings"
	"testing"
)

// TestConfigShowSurfacesAuthorAndLicense proves `config show` surfaces the two
// authoring metadata keys and reflects their exported env values verbatim,
// driven by the same config.Fields single source of truth as every other row.
func TestConfigShowSurfacesAuthorAndLicense(t *testing.T) {
	cleanConfigEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_AUTHOR_NAME", "Jane Doe")
	t.Setenv("MIZAN_DEFAULT_LICENSE", "Apache-2.0")

	out, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v (out=%q)", err, out)
	}
	for _, want := range []string{"author-name", "Jane Doe", "default-license", "Apache-2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show missing %q; got:\n%s", want, out)
		}
	}
}

// TestConfigSetAcceptsAuthorAndLicense proves the two new keys round-trip through
// `config set` (they are accepted keys, derived from Fields).
func TestConfigSetAcceptsAuthorAndLicense(t *testing.T) {
	cleanConfigEnv(t)
	for _, key := range []string{"author-name", "default-license"} {
		if _, err := executeRoot(t, "config", "set", key, "x"); err != nil {
			t.Errorf("config set %q rejected: %v", key, err)
		}
	}
}

package main

import (
	"sort"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
)

// cleanConfigEnv blanks every environment variable LoadConfig consults and points
// XDG_CONFIG_HOME at a fresh temp dir so neither a stray CWD .env nor a real
// <UserConfigDir>/mizan/.env can influence `config show`. t.Setenv restores the
// prior values when the test ends.
func cleanConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"MIZAN_PROJECT_ID", "PROJECT_ID",
		"MIZAN_LOCATION", "LOCATION",
		"MIZAN_STAGING_BUCKET", "GENMEDIA_BUCKET",
		"MIZAN_API_ENDPOINT", "VERTEX_API_ENDPOINT",
		"MIZAN_TEMPLATES_REPO", "MIZAN_DEFAULT_MODEL",
		"MIZAN_REGISTRY_DB", "MIZAN_PACK_CACHE",
		"MIZAN_ENV_FILE", "MIZAN_ALLOW_CUSTOM_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
}

// TestOrBuiltinModel covers both branches of the `config show` DefaultModel
// renderer (WI-F3): a configured value passes through verbatim, while an empty
// value renders the built-in id annotated as "(built-in)" so the user sees
// exactly what an eval will use when no flag or template pins a model.
func TestOrBuiltinModel(t *testing.T) {
	if got := orBuiltinModel("gemini-3.5-flash"); got != "gemini-3.5-flash" {
		t.Errorf("orBuiltinModel(configured) = %q, want the value verbatim", got)
	}
	got := orBuiltinModel("")
	if !strings.Contains(got, "gemini-2.5-flash") || !strings.Contains(got, "built-in") {
		t.Errorf("orBuiltinModel(\"\") = %q, want the built-in id annotated (built-in)", got)
	}
}

// TestConfigShowSurfacesDefaultModel proves `config show` surfaces the configured
// default-model (WI-F3): with MIZAN_DEFAULT_MODEL set, the DefaultModel line
// reflects it verbatim (no "(built-in)" annotation).
func TestConfigShowSurfacesDefaultModel(t *testing.T) {
	cleanConfigEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_DEFAULT_MODEL", "gemini-3.5-flash")

	out, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "default-model") {
		t.Fatalf("config show missing default-model line: %q", out)
	}
	if !strings.Contains(out, "gemini-3.5-flash") {
		t.Errorf("config show DefaultModel does not reflect MIZAN_DEFAULT_MODEL: %q", out)
	}
	if strings.Contains(out, "built-in") {
		t.Errorf("config show annotated a configured model as built-in: %q", out)
	}
}

// TestConfigShowDefaultModelBuiltinFallback proves `config show` surfaces the
// built-in fallback (annotated) when no default-model is configured, so the user
// still sees the resolved default (WI-F3).
func TestConfigShowDefaultModelBuiltinFallback(t *testing.T) {
	cleanConfigEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123") // no MIZAN_DEFAULT_MODEL

	out, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "gemini-2.5-flash") || !strings.Contains(out, "built-in") {
		t.Errorf("config show did not surface the annotated built-in default: %q", out)
	}
}

// TestConfigShowLabelsEveryConfigSetKey proves `config show` labels each row with
// a valid `config set` key so its output round-trips into `config set` (the
// FIX-CONFIG ask): running `config set <key>` for any key printed by show must be
// accepted.
func TestConfigShowLabelsEveryConfigSetKey(t *testing.T) {
	cleanConfigEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123")

	out, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v (out=%q)", err, out)
	}
	for key := range configKeys {
		if !strings.Contains(out, key) {
			t.Errorf("config show output is missing `config set` key %q:\n%s", key, out)
		}
	}
}

// TestConfigShowKeysExactlyMatchConfigSetKeys is the DRIFT GUARD: the set of keys
// `config show` prints must EXACTLY equal the set `config set` accepts. Both are
// derived from the single source of truth (config.Fields), so this can only fail
// if that invariant is broken.
func TestConfigShowKeysExactlyMatchConfigSetKeys(t *testing.T) {
	// Keys `config show` renders (from config.Fields).
	var shown []string
	for _, f := range config.Fields() {
		shown = append(shown, f.Key)
	}
	// Keys `config set` accepts (from configKeys, itself derived from Fields).
	var accepted []string
	for k := range configKeys {
		accepted = append(accepted, k)
	}
	sort.Strings(shown)
	sort.Strings(accepted)
	if strings.Join(shown, ",") != strings.Join(accepted, ",") {
		t.Errorf("config show keys %v != config set keys %v (drift!)", shown, accepted)
	}
}

// TestConfigListAliasInvokesShow proves `config list` is an alias that runs the
// exact same command as `config show` (parity with `registry list`): identical
// output for identical input.
func TestConfigListAliasInvokesShow(t *testing.T) {
	// Run both in the SAME environment (one cleanConfigEnv) so any difference is
	// the command wiring, not the temp-dir-derived default paths.
	cleanConfigEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123")
	showOut, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	listOut, err := executeRoot(t, "config", "list")
	if err != nil {
		t.Fatalf("config list: %v", err)
	}

	if showOut != listOut {
		t.Errorf("config list output differs from config show:\n--- list ---\n%s\n--- show ---\n%s", listOut, showOut)
	}
}

// TestConfigShowSourceHints proves each row carries a source hint and that a
// value set via a real (exported) env var is attributed to env, while a field
// with only its built-in default is attributed to default.
func TestConfigShowSourceHints(t *testing.T) {
	cleanConfigEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123") // exported => env

	out, err := executeRoot(t, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "SOURCE") {
		t.Errorf("config show missing SOURCE column: %q", out)
	}
	// project-id came from an exported var; templates-repo is a built-in default.
	for _, want := range []string{"project-id", "env", "templates-repo", "default"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show missing %q; got:\n%s", want, out)
		}
	}
}

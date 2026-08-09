package main

import (
	"strings"
	"testing"
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
	if !strings.Contains(out, "DefaultModel:") {
		t.Fatalf("config show missing DefaultModel line: %q", out)
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

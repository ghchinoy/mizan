package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// writePackFiles writes rel->content under root for the pack validate CLI tests.
func writePackFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

const cliValidTemplate = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/good
  name: Good
  description: A fine template.
  version: 1.0.0
  license: Apache-2.0
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "Rate {{response}}"
  autorater: {model: gemini-2.5-pro, samplingCount: 4}
`

// pack validate on a clean pack exits zero (no error) and reports OK.
func TestPackValidateCleanExitsZero(t *testing.T) {
	root := t.TempDir()
	writePackFiles(t, root, map[string]string{"templates/t.yaml": cliValidTemplate})

	out, err := executeRoot(t, "pack", "validate", root)
	if err != nil {
		t.Fatalf("pack validate clean should succeed, got err=%v\n%s", err, out)
	}
	if !strings.Contains(out, "0 error(s)") {
		t.Fatalf("want a 0-error summary, got:\n%s", out)
	}
}

// pack validate on a defective pack returns a non-nil error (non-zero exit).
func TestPackValidateDefectExitsNonZero(t *testing.T) {
	root := t.TempDir()
	writePackFiles(t, root, map[string]string{
		"templates/t.yaml": strings.Replace(cliValidTemplate, "kind: pointwise", "kind: bogus", 1),
	})

	out, err := executeRoot(t, "pack", "validate", root)
	if err == nil {
		t.Fatalf("pack validate with a bad kind should fail (non-zero exit), got nil\n%s", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Fatalf("want an ERROR line in the report, got:\n%s", out)
	}
}

// a warnings-only pack (lint warnings, no errors) exits ZERO at the CLI
// boundary and still lists the warning (test MEDIUM).
func TestPackValidateWarningsOnlyExitsZero(t *testing.T) {
	// A valid pointwise template missing description/license/model -> lint warns.
	warnOnly := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/warnish
  version: 1.0.0
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "Rate {{response}}"
`
	root := t.TempDir()
	writePackFiles(t, root, map[string]string{"templates/t.yaml": warnOnly})

	out, err := executeRoot(t, "pack", "validate", root)
	if err != nil {
		t.Fatalf("warnings-only pack must exit zero, got err=%v\n%s", err, out)
	}
	if !strings.Contains(out, "0 error(s)") {
		t.Fatalf("want a 0-error summary, got:\n%s", out)
	}
	if !strings.Contains(out, "warn") {
		t.Fatalf("want a warn line in the report, got:\n%s", out)
	}
}

// --dry-run is short-circuited when steps 1-5 already found errors: no live
// call is attempted (creds-free branch), the skip message is printed, and the
// exit is non-zero from the pre-existing errors (test HIGH / B2).
func TestPackValidateDryRunSkippedWhenErrors(t *testing.T) {
	root := t.TempDir()
	writePackFiles(t, root, map[string]string{
		"templates/t.yaml": strings.Replace(cliValidTemplate, "kind: pointwise", "kind: bogus", 1),
	})

	out, err := executeRoot(t, "pack", "validate", "--dry-run", root)
	if err == nil {
		t.Fatalf("defective pack with --dry-run should still fail, got nil\n%s", out)
	}
	if !strings.Contains(out, "--dry-run skipped") {
		t.Fatalf("want a '--dry-run skipped' message, got:\n%s", out)
	}
	// The live probe header must NOT appear (no engine build / live call).
	if strings.Contains(out, "live API acceptance probe") {
		t.Fatalf("dry-run must not run the live probe when errors exist, got:\n%s", out)
	}
}

// syntheticInstance is a pure function: text-only inputs -> ok with synthetic
// fields; any non-text input -> ok=false (skip). Unit-testable, no creds (B2).
func TestSyntheticInstance(t *testing.T) {
	textOnly := registry.MetricTemplate{
		ID: "acme/text",
		Inputs: []registry.InputSpec{
			{Name: "response", Modality: registry.ModalityText, Required: true},
			{Name: "extra"}, // empty modality is treated as text
		},
	}
	inst, ok := syntheticInstance(textOnly)
	if !ok {
		t.Fatalf("text-only template should be probeable (ok=true)")
	}
	if len(inst.Fields) != 2 {
		t.Fatalf("want a synthetic field per input, got %d: %+v", len(inst.Fields), inst.Fields)
	}
	for name, ref := range inst.Fields {
		if ref.Modality != registry.ModalityText || ref.Text == "" {
			t.Fatalf("field %q should be a non-empty synthetic text asset, got %+v", name, ref)
		}
	}

	withImage := registry.MetricTemplate{
		ID: "acme/img",
		Inputs: []registry.InputSpec{
			{Name: "shot", Modality: registry.ModalityImage, Required: true},
		},
	}
	if _, ok := syntheticInstance(withImage); ok {
		t.Fatalf("a non-text input should make the template unprobeable (ok=false)")
	}
}

// the pack command is registered under the registry group.
func TestPackCommandRegistered(t *testing.T) {
	root := newRootCmd()
	var found *bool
	for _, c := range root.Commands() {
		if c.Name() == "pack" {
			ok := c.GroupID == groupRegistry
			found = &ok
		}
	}
	if found == nil {
		t.Fatal("root missing 'pack' subcommand")
	}
	if !*found {
		t.Fatalf("'pack' command GroupID should be %q", groupRegistry)
	}
}

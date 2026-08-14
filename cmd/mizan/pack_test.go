package main

// pack_test.go drives the `mizan pack` command family through the REAL cobra
// commands against a throwaway, cgo-free SQLite registry — no project or network.
// P2.2 covers `pack validate` (the creds-free PR gate, incl. --dry-run
// short-circuit and the synthetic-instance probe helper); P2.4 covers the
// authoring commands (`registry export`, `pack init`, `pack add`), the
// export→PR→import round-trip entry points, and that authored files are
// schema-valid and re-importable.

import (
	"encoding/json"
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

// schemaRequiredTopLevel reads the top-level "required" list from the embedded
// MetricTemplate JSON Schema so schema-validity assertions track the schema
// itself rather than a copy of it.
func schemaRequiredTopLevel(t *testing.T) []string {
	t.Helper()
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(registry.MetricTemplateSchemaJSON, &schema); err != nil {
		t.Fatalf("parse embedded schema: %v", err)
	}
	if len(schema.Required) == 0 {
		t.Fatal("embedded schema has no top-level required keys")
	}
	return schema.Required
}

// createPointwise seeds one pointwise template into the isolated registry.
func createPointwise(t *testing.T, id string) {
	t.Helper()
	out, err := executeRoot(t,
		"registry", "create",
		"--id", id,
		"--name", "Seed "+id,
		"--kind", "pointwise",
		"--prompt", "Rate the response: {{response}}",
		"--sampling-count", "1",
	)
	if err != nil {
		t.Fatalf("registry create %s: %v (out=%q)", id, err, out)
	}
}

func TestRegistryExportThenImportRoundTrip(t *testing.T) {
	useIsolatedRegistry(t)
	createPointwise(t, "acme/quality")
	createPointwise(t, "acme/tone")

	pack := filepath.Join(t.TempDir(), "packs", "acme")
	out, err := executeRoot(t, "registry", "export", "--out", pack, "--all")
	if err != nil {
		t.Fatalf("registry export: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "2 written") {
		t.Fatalf("export summary = %q, want '2 written'", out)
	}
	for _, f := range []string{"quality.yaml", "tone.yaml"} {
		if _, err := os.Stat(filepath.Join(pack, "templates", f)); err != nil {
			t.Fatalf("expected exported file templates/%s: %v", f, err)
		}
	}

	// Re-import the exported pack into a SECOND isolated registry; both should land.
	t.Setenv("MIZAN_REGISTRY_DB", filepath.Join(t.TempDir(), "registry2.db"))
	impOut, err := executeRoot(t, "--output", "json", "registry", "import", pack)
	if err != nil {
		t.Fatalf("registry import: %v (out=%q)", err, impOut)
	}
	var rep registry.ImportReport
	if err := json.Unmarshal([]byte(impOut), &rep); err != nil {
		t.Fatalf("decode import report %q: %v", impOut, err)
	}
	if rep.Inserted != 2 {
		t.Fatalf("re-import inserted = %d, want 2", rep.Inserted)
	}
}

// TestRegistryExportSelectorWiring exercises the cobra flag→Selector mapping for
// --id and --namespace at the command layer (test G3): the service-level selector
// logic is unit-tested in the registry package, but the CLI wiring for these two
// modes was previously only driven via --all.
func TestRegistryExportSelectorWiring(t *testing.T) {
	useIsolatedRegistry(t)
	createPointwise(t, "acme/quality")
	createPointwise(t, "acme/tone")
	createPointwise(t, "other/thing")

	// --id: exactly one file, the selected template.
	byID := filepath.Join(t.TempDir(), "by-id")
	out, err := executeRoot(t, "registry", "export", "--out", byID, "--id", "acme/quality")
	if err != nil {
		t.Fatalf("export --id: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "1 written") {
		t.Fatalf("export --id summary = %q, want '1 written'", out)
	}
	if _, err := os.Stat(filepath.Join(byID, "templates", "quality.yaml")); err != nil {
		t.Fatalf("export --id did not write quality.yaml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(byID, "templates", "tone.yaml")); !os.IsNotExist(err) {
		t.Fatalf("export --id leaked tone.yaml: err=%v", err)
	}

	// --namespace: only the acme/* templates, no cross-namespace leak.
	byNS := filepath.Join(t.TempDir(), "by-ns")
	out, err = executeRoot(t, "registry", "export", "--out", byNS, "--namespace", "acme")
	if err != nil {
		t.Fatalf("export --namespace: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "2 written") {
		t.Fatalf("export --namespace summary = %q, want '2 written'", out)
	}
	if _, err := os.Stat(filepath.Join(byNS, "templates", "thing.yaml")); !os.IsNotExist(err) {
		t.Fatalf("export --namespace leaked other/thing: err=%v", err)
	}
}

func TestRegistryExportRequiresSelector(t *testing.T) {
	useIsolatedRegistry(t)
	pack := filepath.Join(t.TempDir(), "pack")
	if _, err := executeRoot(t, "registry", "export", "--out", pack); err == nil {
		t.Fatal("export with no selector = nil error, want exactly-one-selector error")
	}
}

func TestPackInitScaffold(t *testing.T) {
	useIsolatedRegistry(t)
	dir := filepath.Join(t.TempDir(), "packs", "google-brand")
	out, err := executeRoot(t, "pack", "init", dir, "--name", "google-brand")
	if err != nil {
		t.Fatalf("pack init: %v (out=%q)", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "mizan-pack.yaml")); err != nil {
		t.Fatalf("manifest not created: %v", err)
	}
	for _, sub := range []string{"templates", "evalsets"} {
		fi, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s/ not scaffolded: %v", sub, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".github")); !os.IsNotExist(err) {
		t.Fatalf("pack init emitted CI workflow (.github) — it must not")
	}
}

func TestPackInitRequiresName(t *testing.T) {
	useIsolatedRegistry(t)
	dir := filepath.Join(t.TempDir(), "p")
	if _, err := executeRoot(t, "pack", "init", dir); err == nil {
		t.Fatal("pack init without --name = nil error, want required-name error")
	}
}

func TestPackAddWritesSchemaValidFile(t *testing.T) {
	useIsolatedRegistry(t)
	createPointwise(t, "acme/quality")

	dir := filepath.Join(t.TempDir(), "packs", "acme")
	if _, err := executeRoot(t, "pack", "init", dir, "--name", "acme"); err != nil {
		t.Fatalf("pack init: %v", err)
	}
	out, err := executeRoot(t, "pack", "add", dir, "--from", "acme/quality")
	if err != nil {
		t.Fatalf("pack add: %v (out=%q)", err, out)
	}
	file := filepath.Join(dir, "templates", "quality.yaml")
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("pack add did not write %s: %v", file, err)
	}

	// The written file is schema-valid: it carries every top-level key the
	// embedded schema/metrictemplate.json marks REQUIRED (derived from the schema
	// itself, not hardcoded — test G1), the `kind` const, and the id.
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read written template: %v", err)
	}
	for _, key := range schemaRequiredTopLevel(t) {
		if !strings.Contains(string(b), key+":") {
			t.Errorf("written template missing schema-required key %q:\n%s", key, b)
		}
	}
	for _, key := range []string{"kind: MetricTemplate", "id: acme/quality"} {
		if !strings.Contains(string(b), key) {
			t.Errorf("written template missing %q:\n%s", key, b)
		}
	}

	// Validate the pack-add output against the strict JSON Schema
	// (schema/metrictemplate.json) DIRECTLY — the structural JSON-Schema step,
	// now that the P2.2 schema is on main (test G1) — rather than only proving it
	// re-imports. This is the JSON-Schema layer that the full ValidatePack builds
	// its identity/semantic checks on top of.
	if err := registry.ValidateTemplateSchema(b); err != nil {
		t.Fatalf("pack-add output is not schema-valid: %v\n%s", err, b)
	}

	t.Setenv("MIZAN_REGISTRY_DB", filepath.Join(t.TempDir(), "registry2.db"))
	if _, err := executeRoot(t, "registry", "import", dir); err != nil {
		t.Fatalf("re-import of pack-add output failed: %v", err)
	}
}

func TestPackAddRequiresFrom(t *testing.T) {
	useIsolatedRegistry(t)
	dir := filepath.Join(t.TempDir(), "p")
	if _, err := executeRoot(t, "pack", "add", dir); err == nil {
		t.Fatal("pack add without --from = nil error, want required-from error")
	}
}

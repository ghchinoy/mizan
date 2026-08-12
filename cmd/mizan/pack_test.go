package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

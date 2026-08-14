package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

func TestRenderImportReportText(t *testing.T) {
	prev := outputFormat
	outputFormat = outputTable
	defer func() { outputFormat = prev }()
	r := registry.ImportReport{
		Source:     registry.SourceInfo{Type: "local", Origin: "testdata"},
		Inserted:   1,
		Updated:    1,
		Skipped:    1,
		Conflicted: 1,
		Entries: []registry.ImportEntry{
			{ID: "ns/a", Action: registry.ActionInserted},
			{ID: "ns/b", Action: registry.ActionUpdated, Reason: "updated to newer upstream version"},
			{ID: "ns/c", Action: registry.ActionSkipped, Reason: "already exists"},
			{ID: "ns/d", Action: registry.ActionConflicted, Reason: "same version, different content"},
		},
	}
	var buf bytes.Buffer
	if err := renderImportReport(&buf, r); err != nil {
		t.Fatalf("renderImportReport: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"1 inserted, 1 updated, 1 skipped, 1 conflicted", "testdata", "inserted: ns/a", "updated: ns/b", "skipped: ns/c", "conflicted: ns/d", "same version, different content"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderImportReportJSON(t *testing.T) {
	prev := outputFormat
	outputFormat = outputJSON
	defer func() { outputFormat = prev }()
	r := registry.ImportReport{
		Source:   registry.SourceInfo{Type: "local", Origin: "testdata"},
		Inserted: 1,
		Entries:  []registry.ImportEntry{{ID: "ns/a", Action: registry.ActionInserted}},
	}
	var buf bytes.Buffer
	if err := renderImportReport(&buf, r); err != nil {
		t.Fatalf("renderImportReport: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"Inserted": 1`, `"ns/a"`, `"inserted"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json output missing %q:\n%s", want, out)
		}
	}
}

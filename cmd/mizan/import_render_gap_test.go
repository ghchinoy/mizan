// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

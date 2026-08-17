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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/registry"
)

// applyFromArgs binds templateFlags to a throwaway cobra command, parses the
// given flag args, then overlays them onto base (or a zero template) via
// apply(). It exercises the full flag-parse -> build -> apply path without
// opening a DB or touching the network.
func applyFromArgs(t *testing.T, update bool, base *registry.MetricTemplate, args ...string) (*registry.MetricTemplate, error) {
	t.Helper()
	var f templateFlags
	cmd := &cobra.Command{
		Use:           "x",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
	f.bind(cmd)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return nil, err
	}
	var tmpl registry.MetricTemplate
	if base != nil {
		tmpl = *base
	}
	if err := f.apply(cmd, &tmpl, update); err != nil {
		return nil, err
	}
	return &tmpl, nil
}

func TestRubricGroupSingle(t *testing.T) {
	got, err := applyFromArgs(t, false, nil,
		"--kind", "rubric",
		"--rubric-group", "clarity=The offer is clear;Free of jargon")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := map[string][]string{"clarity": {"The offer is clear", "Free of jargon"}}
	if !reflect.DeepEqual(got.RubricGroups, want) {
		t.Fatalf("RubricGroups = %#v, want %#v", got.RubricGroups, want)
	}
}

func TestRubricGroupMultipleNames(t *testing.T) {
	got, err := applyFromArgs(t, false, nil,
		"--rubric-group", "clarity=a;b",
		"--rubric-group", "tone=c")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := map[string][]string{"clarity": {"a", "b"}, "tone": {"c"}}
	if !reflect.DeepEqual(got.RubricGroups, want) {
		t.Fatalf("RubricGroups = %#v, want %#v", got.RubricGroups, want)
	}
}

func TestRubricGroupSameNameAccumulates(t *testing.T) {
	got, err := applyFromArgs(t, false, nil,
		"--rubric-group", "clarity=a;b",
		"--rubric-group", "clarity=c")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := map[string][]string{"clarity": {"a", "b", "c"}}
	if !reflect.DeepEqual(got.RubricGroups, want) {
		t.Fatalf("RubricGroups = %#v, want %#v", got.RubricGroups, want)
	}
}

func TestRubricGroupSplitTrimSkipEmpty(t *testing.T) {
	got, err := applyFromArgs(t, false, nil,
		"--rubric-group", "clarity=  a  ; ;b ;;  ")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := map[string][]string{"clarity": {"a", "b"}}
	if !reflect.DeepEqual(got.RubricGroups, want) {
		t.Fatalf("RubricGroups = %#v, want %#v", got.RubricGroups, want)
	}
}

func TestRubricGroupMalformedSpec(t *testing.T) {
	for _, bad := range []string{"noequals", "=missingname"} {
		if _, err := applyFromArgs(t, false, nil, "--rubric-group", bad); err == nil {
			t.Errorf("--rubric-group %q = nil error, want error", bad)
		}
	}
}

func TestRubricGroupsFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "groups.json")
	if err := os.WriteFile(path, []byte(`{"clarity":["a","b"],"tone":["c"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := applyFromArgs(t, false, nil, "--rubric-groups-file", path)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := map[string][]string{"clarity": {"a", "b"}, "tone": {"c"}}
	if !reflect.DeepEqual(got.RubricGroups, want) {
		t.Fatalf("RubricGroups = %#v, want %#v", got.RubricGroups, want)
	}
}

func TestRubricGroupsFileMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "groups.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := applyFromArgs(t, false, nil, "--rubric-groups-file", path)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v, want invalid JSON", err)
	}
}

func TestRubricGroupsFileNonRegular(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	_, err := applyFromArgs(t, false, nil, "--rubric-groups-file", dir)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want 'not a regular file'", err)
	}
}

func TestRubricGroupsFileMissing(t *testing.T) {
	_, err := applyFromArgs(t, false, nil, "--rubric-groups-file", filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// TestRubricGroupsFileOversize: a file larger than registry.MaxTemplateFileBytes is
// rejected by the size cap before any JSON parse.
func TestRubricGroupsFileOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "groups.json")
	if err := os.WriteFile(path, make([]byte, registry.MaxTemplateFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := applyFromArgs(t, false, nil, "--rubric-groups-file", path)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want size-cap 'exceeds' error", err)
	}
}

// TestRubricPrecedenceFileThenFlags: file seeds the map; a --rubric-group flag
// overrides the file's entry for that group name, and a new name is added.
func TestRubricPrecedenceFileThenFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "groups.json")
	if err := os.WriteFile(path, []byte(`{"clarity":["file-a","file-b"],"tone":["file-c"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := applyFromArgs(t, false, nil,
		"--rubric-groups-file", path,
		"--rubric-group", "clarity=flag-a", // overrides file's clarity
		"--rubric-group", "accuracy=flag-x") // new name
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := map[string][]string{
		"clarity":  {"flag-a"},
		"tone":     {"file-c"},
		"accuracy": {"flag-x"},
	}
	if !reflect.DeepEqual(got.RubricGroups, want) {
		t.Fatalf("RubricGroups = %#v, want %#v", got.RubricGroups, want)
	}
}

func TestResponseSchemaInlineValid(t *testing.T) {
	raw := `{"type":"object","properties":{"score":{"type":"number"}}}`
	got, err := applyFromArgs(t, false, nil, "--response-schema", raw)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.ResponseSchema == nil || got.ResponseSchema.JSON != raw {
		t.Fatalf("ResponseSchema = %#v, want JSON %q", got.ResponseSchema, raw)
	}
}

func TestResponseSchemaInlineInvalid(t *testing.T) {
	_, err := applyFromArgs(t, false, nil, "--response-schema", `{not json`)
	if err == nil || !strings.Contains(err.Error(), "well-formed JSON") {
		t.Fatalf("err = %v, want well-formed JSON error", err)
	}
}

func TestResponseSchemaFileValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	raw := `{"type":"object","properties":{"ok":{"type":"boolean"}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := applyFromArgs(t, false, nil, "--response-schema-file", path)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.ResponseSchema == nil || got.ResponseSchema.JSON != raw {
		t.Fatalf("ResponseSchema = %#v, want JSON %q", got.ResponseSchema, raw)
	}
}

func TestResponseSchemaFileInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(`{bad`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := applyFromArgs(t, false, nil, "--response-schema-file", path)
	if err == nil || !strings.Contains(err.Error(), "well-formed JSON") {
		t.Fatalf("err = %v, want well-formed JSON error", err)
	}
}

// TestResponseSchemaFileNonRegular: a non-regular file (a directory) is rejected
// by the same guard used for rubric files.
func TestResponseSchemaFileNonRegular(t *testing.T) {
	dir := t.TempDir() // a directory, not a regular file
	_, err := applyFromArgs(t, false, nil, "--response-schema-file", dir)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want 'not a regular file'", err)
	}
}

// TestResponseSchemaFileOversize: a file larger than registry.MaxTemplateFileBytes is
// rejected by the size cap before any JSON validation.
func TestResponseSchemaFileOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, make([]byte, registry.MaxTemplateFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := applyFromArgs(t, false, nil, "--response-schema-file", path)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want size-cap 'exceeds' error", err)
	}
}

// TestResponseSchemaFilePrecedence: when both flags are given, the file wins.
func TestResponseSchemaFilePrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	fileRaw := `{"type":"object","properties":{"fromfile":{"type":"string"}}}`
	if err := os.WriteFile(path, []byte(fileRaw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := applyFromArgs(t, false, nil,
		"--response-schema", `{"type":"object","properties":{"inline":{"type":"string"}}}`,
		"--response-schema-file", path)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.ResponseSchema == nil || got.ResponseSchema.JSON != fileRaw {
		t.Fatalf("ResponseSchema.JSON = %q, want file content %q", got.ResponseSchema, fileRaw)
	}
}

// TestApplyUpdateSafetyRubric: update without a rubric flag preserves existing
// groups; update with the flag replaces them.
func TestApplyUpdateSafetyRubric(t *testing.T) {
	base := &registry.MetricTemplate{
		ID:           "test/r",
		Kind:         registry.KindRubric,
		RubricGroups: map[string][]string{"clarity": {"old"}},
	}
	// No rubric flag -> preserved.
	got, err := applyFromArgs(t, true, base, "--name", "New Name")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(got.RubricGroups, map[string][]string{"clarity": {"old"}}) {
		t.Fatalf("groups not preserved on update: %#v", got.RubricGroups)
	}
	if got.Name != "New Name" {
		t.Fatalf("Name = %q, want New Name", got.Name)
	}
	// With rubric flag -> replaced.
	got, err = applyFromArgs(t, true, base, "--rubric-group", "clarity=new")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(got.RubricGroups, map[string][]string{"clarity": {"new"}}) {
		t.Fatalf("groups not replaced on update: %#v", got.RubricGroups)
	}
}

// TestApplyUpdateSafetySchema: update without a schema flag preserves the
// existing schema; update with a flag replaces it.
func TestApplyUpdateSafetySchema(t *testing.T) {
	base := &registry.MetricTemplate{
		ID:             "test/c",
		Kind:           registry.KindCustomSchema,
		ResponseSchema: &registry.Schema{JSON: `{"type":"object"}`},
	}
	got, err := applyFromArgs(t, true, base, "--name", "New")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.ResponseSchema == nil || got.ResponseSchema.JSON != `{"type":"object"}` {
		t.Fatalf("schema not preserved on update: %#v", got.ResponseSchema)
	}
	got, err = applyFromArgs(t, true, base, "--response-schema", `{"type":"array"}`)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.ResponseSchema == nil || got.ResponseSchema.JSON != `{"type":"array"}` {
		t.Fatalf("schema not replaced on update: %#v", got.ResponseSchema)
	}
}

// TestFlipEnabledCreateDefaultAndOverride proves the --flip-enabled create flag
// is honored end-to-end through the flag-parse -> build -> apply path (the
// counterpart to the eval-time honoring in internal/eval): a pairwise template
// created WITHOUT the flag keeps flip=true (default unchanged, eval-triage #4),
// and an explicit --flip-enabled=false is persisted as false so a user can turn
// flip off. Guards the create default against regression now that the eval path
// actually reads tmpl.FlipEnabled.
func TestFlipEnabledCreateDefaultAndOverride(t *testing.T) {
	// Default create path (no --flip-enabled): flip stays true.
	got, err := applyFromArgs(t, false, nil, "--kind", "pairwise")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !got.FlipEnabled {
		t.Errorf("FlipEnabled = false on default create, want true (default must remain unchanged)")
	}

	// Explicit opt-out: --flip-enabled=false is persisted.
	got, err = applyFromArgs(t, false, nil, "--kind", "pairwise", "--flip-enabled=false")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.FlipEnabled {
		t.Errorf("FlipEnabled = true after --flip-enabled=false, want false (opt-out must persist)")
	}
}

// TestApplyUpdateSafetyFlipEnabled proves flip is update-safe: an update that
// does NOT set --flip-enabled preserves the stored value (rather than clobbering
// it back to the flag default true), while an explicit --flip-enabled=true flips
// a previously-disabled template back on.
func TestApplyUpdateSafetyFlipEnabled(t *testing.T) {
	base := &registry.MetricTemplate{ID: "test/p", Kind: registry.KindPairwise, FlipEnabled: false}

	// No flip flag on update -> preserved (stays false, not reset to default true).
	got, err := applyFromArgs(t, true, base, "--name", "New Name")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.FlipEnabled {
		t.Errorf("FlipEnabled = true after update without the flag, want false preserved")
	}

	// Explicit flag on update -> replaced.
	got, err = applyFromArgs(t, true, base, "--flip-enabled=true")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !got.FlipEnabled {
		t.Errorf("FlipEnabled = false after --flip-enabled=true on update, want true")
	}
}

func TestValidateTemplate(t *testing.T) {
	cases := []struct {
		name    string
		tmpl    registry.MetricTemplate
		wantErr string
	}{
		{"rubric-missing", registry.MetricTemplate{Kind: registry.KindRubric}, "requires rubric groups"},
		{"rubric-ok", registry.MetricTemplate{Kind: registry.KindRubric, RubricGroups: map[string][]string{"g": {"c"}}}, ""},
		{"custom-missing", registry.MetricTemplate{Kind: registry.KindCustomSchema}, "requires a response schema"},
		{"custom-empty", registry.MetricTemplate{Kind: registry.KindCustomSchema, ResponseSchema: &registry.Schema{JSON: "  "}}, "requires a response schema"},
		{"custom-ok", registry.MetricTemplate{Kind: registry.KindCustomSchema, ResponseSchema: &registry.Schema{JSON: "{}"}}, ""},
		{"pointwise-ok", registry.MetricTemplate{Kind: registry.KindPointwise}, ""},
		{"pairwise-ok", registry.MetricTemplate{Kind: registry.KindPairwise}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTemplate(&tc.tmpl)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateTemplate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateTemplate = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestCreateRubricMissingGroupsFails drives the full create command path far
// enough to hit early validation (before any DB open) by omitting rubric groups.
func TestCreateRubricMissingGroupsFails(t *testing.T) {
	out, err := executeRoot(t, "registry", "create", "--id", "test/r", "--kind", "rubric")
	if err == nil || !strings.Contains(err.Error(), "requires rubric groups") {
		t.Fatalf("err = %v (out=%q), want 'requires rubric groups'", err, out)
	}
}

func TestCreateCustomSchemaMissingFails(t *testing.T) {
	out, err := executeRoot(t, "registry", "create", "--id", "test/c", "--kind", "custom_schema")
	if err == nil || !strings.Contains(err.Error(), "requires a response schema") {
		t.Fatalf("err = %v (out=%q), want 'requires a response schema'", err, out)
	}
}

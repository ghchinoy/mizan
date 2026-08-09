package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// These smoke tests cover the WI-P1-6 CLI glue (cobra command construction, arg
// and flag parsing, and output-format selection). They are cgo-free and
// network-free: every case fails fast on validation before any DB open or live
// API call, so no real backend is touched.

// executeRoot builds a fresh root command, runs it with the given args, and
// returns combined stdout/stderr plus the execution error.
func executeRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestRootCommandConstruction(t *testing.T) {
	root := newRootCmd()
	if root.Use != "mizan" {
		t.Fatalf("root Use = %q, want mizan", root.Use)
	}
	want := map[string]bool{"registry": false, "eval": false, "config": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("root missing subcommand %q", name)
		}
	}
}

func TestSubcommandGroups(t *testing.T) {
	root := newRootCmd()
	groups := map[string]string{
		"registry": groupRegistry,
		"eval":     groupEval,
		"config":   groupConfig,
	}
	for _, c := range root.Commands() {
		if want, ok := groups[c.Name()]; ok && c.GroupID != want {
			t.Errorf("command %q GroupID = %q, want %q", c.Name(), c.GroupID, want)
		}
	}
}

func TestPersistentOutputFlagRejectsInvalid(t *testing.T) {
	// PersistentPreRunE validates --output before any RunE, so an invalid value
	// fails without reaching a backend.
	out, err := executeRoot(t, "--output", "yaml", "registry", "list")
	if err == nil {
		t.Fatalf("expected error for invalid --output, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "invalid --output") {
		t.Errorf("error = %v, want it to mention invalid --output", err)
	}
}

func TestValidOutput(t *testing.T) {
	for _, ok := range []string{outputTable, outputJSON} {
		if err := validOutput(ok); err != nil {
			t.Errorf("validOutput(%q) = %v, want nil", ok, err)
		}
	}
	if err := validOutput("xml"); err == nil {
		t.Error("validOutput(\"xml\") = nil, want error")
	}
}

func TestEvalRunRequiresMetric(t *testing.T) {
	// Fails on the --metric check before config/DB/API.
	out, err := executeRoot(t, "eval", "run")
	if err == nil {
		t.Fatalf("expected error for missing --metric, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "--metric is required") {
		t.Errorf("error = %v, want --metric is required", err)
	}
}

func TestRegistryCreateRequiresID(t *testing.T) {
	// Fails on the --id check before config/DB.
	out, err := executeRoot(t, "registry", "create")
	if err == nil {
		t.Fatalf("expected error for missing --id, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "--id is required") {
		t.Errorf("error = %v, want --id is required", err)
	}
}

func TestConfigSetUnknownKey(t *testing.T) {
	// Unknown key is rejected before any file is written.
	out, err := executeRoot(t, "config", "set", "not-a-key", "v")
	if err == nil {
		t.Fatalf("expected error for unknown key, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("error = %v, want unknown config key", err)
	}
}

func TestParseFields(t *testing.T) {
	inst, err := parseFields([]string{"response=hi", "context=there"})
	if err != nil {
		t.Fatalf("parseFields: %v", err)
	}
	if got := inst.Fields["response"].Text; got != "hi" {
		t.Errorf("response = %q, want hi", got)
	}
	if got := inst.Fields["context"].Modality; got != registry.ModalityText {
		t.Errorf("modality = %q, want text", got)
	}
	// A value may itself contain '='.
	inst, err = parseFields([]string{"expr=a=b"})
	if err != nil {
		t.Fatalf("parseFields with '=': %v", err)
	}
	if got := inst.Fields["expr"].Text; got != "a=b" {
		t.Errorf("expr = %q, want a=b", got)
	}
}

func TestParseFieldsInvalid(t *testing.T) {
	for _, bad := range []string{"noequals", "=noname"} {
		if _, err := parseFields([]string{bad}); err == nil {
			t.Errorf("parseFields(%q) = nil error, want error", bad)
		}
	}
}

func TestConfigSetKeysSorted(t *testing.T) {
	keys := sortedKeys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("sortedKeys not sorted: %v", keys)
		}
	}
	if len(keys) != len(configKeys) {
		t.Errorf("sortedKeys len = %d, want %d", len(keys), len(configKeys))
	}
}

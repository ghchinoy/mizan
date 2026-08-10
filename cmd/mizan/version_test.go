package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestVersionCommandRegistered guards the root wiring: `mizan version` must be
// reachable as a subcommand.
func TestVersionCommandRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "version" {
			found = true
			break
		}
	}
	if !found {
		t.Error("root missing subcommand \"version\"")
	}
}

// TestVersionCommandDefaultOutput asserts the human-readable one-line form. A
// plain `go test` build injects no ldflags, so the defaults (dev/none/unknown)
// appear — this pins the exact printed string.
func TestVersionCommandDefaultOutput(t *testing.T) {
	out, err := executeRoot(t, "version")
	if err != nil {
		t.Fatalf("version: %v (out=%q)", err, out)
	}
	want := "mizan dev (commit none, built unknown)\n"
	if out != want {
		t.Errorf("version output = %q, want %q", out, want)
	}
}

// TestVersionCommandJSONOutput asserts the --output json shape parses and
// carries the expected keys.
func TestVersionCommandJSONOutput(t *testing.T) {
	out, err := executeRoot(t, "--output", "json", "version")
	if err != nil {
		t.Fatalf("version --output json: %v (out=%q)", err, out)
	}
	var got struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (out=%q)", err, out)
	}
	if got.Version != "dev" || got.Commit != "none" || got.Date != "unknown" {
		t.Errorf("version JSON = %+v, want {dev none unknown}", got)
	}
}

// TestVersionCommandJSONExactShape pins the exact bytes of the --output json
// form: 2-space indentation, field order, and the trailing newline emitted by
// printJSON (the repo's JSON convention). A plain `go test` build injects no
// ldflags, so the defaults appear. This complements the round-trip test above
// by catching indentation/ordering/convention regressions that still parse as
// valid JSON.
func TestVersionCommandJSONExactShape(t *testing.T) {
	out, err := executeRoot(t, "--output", "json", "version")
	if err != nil {
		t.Fatalf("version --output json: %v (out=%q)", err, out)
	}
	want := "{\n" +
		"  \"version\": \"dev\",\n" +
		"  \"commit\": \"none\",\n" +
		"  \"date\": \"unknown\"\n" +
		"}\n"
	if out != want {
		t.Errorf("version --output json =\n%q\nwant\n%q", out, want)
	}
}

// TestVersionCommandRejectsArgs guards cobra.NoArgs.
func TestVersionCommandRejectsArgs(t *testing.T) {
	out, err := executeRoot(t, "version", "extra")
	if err == nil {
		t.Fatalf("expected error for extra arg, got nil (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "unknown command") &&
		!strings.Contains(err.Error(), "arg") {
		t.Errorf("error = %v, want an args/unknown-command rejection", err)
	}
}

package main

import (
	"bytes"
	"testing"
)

// TestEmitWarningsWritesEachToStderrWriter proves the shared emission helper
// (used by both the eval-run and pairwise RunE paths) writes every warning to the
// provided writer, one per line in the returned order, and formatted exactly as
// before (verbatim + trailing newline). This closes the previously-untested glue
// that carried Result.Warnings to stderr, without needing creds or network.
func TestEmitWarningsWritesEachToStderrWriter(t *testing.T) {
	warnings := []string{
		"pairwise flip is enabled: the Choice is the de-biased, authoritative verdict.",
		"second warning line",
	}

	var stderr bytes.Buffer
	emitWarnings(&stderr, warnings)

	want := warnings[0] + "\n" + warnings[1] + "\n"
	if got := stderr.String(); got != want {
		t.Errorf("emitWarnings stderr\n got: %q\nwant: %q", got, want)
	}
}

// TestEmitWarningsNeverWritesToStdout proves the helper is a stderr-only sink: it
// writes solely to the writer it is handed and never leaks warnings onto a
// separate stdout stream (the human table must stay clean on stdout).
func TestEmitWarningsNeverWritesToStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	emitWarnings(&stderr, []string{"a warning"})

	if stdout.Len() != 0 {
		t.Errorf("emitWarnings wrote to stdout, want nothing: %q", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Error("emitWarnings wrote nothing to the provided (stderr) writer, want the warning")
	}
}

// TestEmitWarningsEmptyIsNoOp proves that with no warnings nothing is written,
// matching the prior loop's behavior on an empty/nil slice.
func TestEmitWarningsEmptyIsNoOp(t *testing.T) {
	var stderr bytes.Buffer
	emitWarnings(&stderr, nil)
	if stderr.Len() != 0 {
		t.Errorf("emitWarnings(nil) wrote output, want none: %q", stderr.String())
	}
}

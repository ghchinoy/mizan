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
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestEvalProjectFlagIsPersistent guards the FEAT-PROJECT command wiring: the
// --project flag must be a PERSISTENT flag on the `eval` command so it is shared
// by both `eval run` and `eval pairwise` (mirroring --model), rather than a local
// flag on a single subcommand. The applyProjectOverride/preflightSources unit
// tests would all still pass if this StringVar registration regressed to a local
// flag or was dropped, so this test is the guard that keeps the flag reachable
// from the CLI.
func TestEvalProjectFlagIsPersistent(t *testing.T) {
	evalCmd := newEvalCmd()

	// The flag must live on the command's PERSISTENT flag set (inherited by
	// subcommands), not its local flag set.
	if f := evalCmd.PersistentFlags().Lookup("project"); f == nil {
		t.Fatalf("eval command has no persistent --project flag; FEAT-PROJECT wiring missing")
	}
	if f := evalCmd.LocalNonPersistentFlags().Lookup("project"); f != nil {
		t.Errorf("--project is registered as a LOCAL flag; it must be persistent so run+pairwise both inherit it")
	}

	// Both subcommands must inherit --project through the persistent flag set.
	for _, name := range []string{"run", "pairwise"} {
		sub := findSubcommand(t, evalCmd, name)
		if f := sub.InheritedFlags().Lookup("project"); f == nil {
			t.Errorf("eval %s does not inherit --project; persistent flag not shared with this subcommand", name)
		}
	}
}

// TestEvalRunAcceptsProjectFlag proves `eval run --project <p>` parses the flag
// (the run reaches the --metric validation, NOT a cobra "unknown flag" error).
// It is network- and cgo-free: the run fails on the required --metric check
// before any config load, DB open, or live API call.
func TestEvalRunAcceptsProjectFlag(t *testing.T) {
	out, err := executeRoot(t, "eval", "run", "--project", "flag-project")
	assertFlagParsedButMetricMissing(t, out, err)
}

// TestEvalPairwiseAcceptsProjectFlag proves `eval pairwise --project <p>` parses
// the flag too — the same persistent flag reaches the sibling subcommand. It
// stops at the required --metric check, so it touches no backend.
func TestEvalPairwiseAcceptsProjectFlag(t *testing.T) {
	out, err := executeRoot(t, "eval", "pairwise", "--project", "flag-project")
	assertFlagParsedButMetricMissing(t, out, err)
}

// assertFlagParsedButMetricMissing asserts a run stopped at the --metric
// validation rather than a flag-parsing error: proof that --project was accepted
// as a known flag before any backend work.
func assertFlagParsedButMetricMissing(t *testing.T, out string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected --metric required error, got nil (out=%q)", out)
	}
	if strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("--project was rejected as unknown; persistent flag not wired: %v", err)
	}
	if !strings.Contains(err.Error(), "--metric is required") {
		t.Errorf("error = %v, want it to reach the --metric required check", err)
	}
}

// findSubcommand returns the named direct subcommand of parent, failing the test
// when it is absent.
func findSubcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("subcommand %q not found", name)
	return nil
}

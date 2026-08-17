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

// seam_test.go is the executable proof of the architecture seam (design §3.1 /
// §3.11, WI-P2-7). The command frontends — cmd/mizan AND cmd/mizan-desktop, and
// any cmd/<x> binary added later — must depend on the registry.Service /
// eval.Engine / config / wire façades ONLY. They must import NONE of:
//
//   - internal/registry/sqlite      (the concrete Store backend — wire's job)
//   - internal/registry/sync        (the SyncBackend/GitPackBackend internals)
//   - internal/registry/codec       (the pack codec internals)
//   - gopkg.in/yaml*                (YAML lib — pack encoding is behind the Service)
//   - any git library               (Mizan shells out to git, never embeds it)
//
// and must never construct the pack codec / sync backend directly. Keeping cmd/*
// free of those is exactly what makes a future model-B backend (Firestore/GCS) a
// one-line change in internal/wire and nowhere else — see design §3.1 and the
// §9.6 seam acceptance check.
//
// WI-P2-7 hardening over the interim P2.1 guard:
//   1. ENUMERATION IS A TREE-WALK. Instead of a hard-coded package list, this
//      walks the whole cmd/ tree so a newly added cmd/<x> binary is covered
//      automatically and cannot silently escape the seam.
//   2. IMPORTS ARE READ VIA go/build WITH UseAllFiles. We inspect the package's
//      direct (non-test) imports regardless of build constraints, so a
//      build-tagged cmd file can't hide a forbidden import from the current GOOS.
//   3. IT HAS TEETH. TestForbiddenImportRuleHasTeeth (matcher table) and
//      TestScanCmdSeamCatchesPlantedImport (a full walk over a temp package that
//      plants a forbidden import + a forbidden constructor reference) prove the
//      check fails loudly on a regression rather than passing vacuously.
//
// This test uses only the standard library (go/build, io/fs) — no cgo, no heavy
// dependency — so it runs anywhere `go test` runs.

import (
	"fmt"
	"go/build"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenCmdImports are import-path substrings a cmd/* package must never
// import directly. Substring match keeps the rule robust to version suffixes
// (gopkg.in/yaml.v3) and subpackages (github.com/go-git/go-git/v5/plumbing).
//
// NOTE on sync/codec: today the pack codec and the sync backend are TYPES inside
// package internal/registry (codec.go, sync.go), not separate importable
// packages, so the "internal/registry/sync" and "internal/registry/codec"
// substrings match no real import path yet. They are kept here deliberately as
// future-proofing: if either is ever extracted into its own subpackage, cmd/*
// is already forbidden from importing it. The spirit they guard TODAY —
// cmd/* must not reach past the Service to build a codec/backend — is enforced
// concretely by forbiddenCmdReferences below.
var forbiddenCmdImports = []string{
	"internal/registry/sqlite", // concrete Store backend — wire injects it
	"internal/registry/sync",   // sync backend subpackage (future-proofing)
	"internal/registry/codec",  // pack codec subpackage (future-proofing)
	"gopkg.in/yaml",            // YAML lib — pack encoding is behind the Service
	"github.com/go-git",        // git libs — Mizan shells out, never embeds git
	"gopkg.in/src-d/go-git",
	"github.com/libgit2",
}

// forbiddenCmdReferences are registry constructors that live in package registry
// but are backend/codec internals: cmd/* must go through registry.Service
// (Import/Export construct the backend internally) and wire, never build a
// GitPackBackend or a YAMLCodec itself. A source-level substring scan of the
// non-test cmd sources catches these even though no forbidden IMPORT does.
var forbiddenCmdReferences = []string{
	"registry.NewGitPackBackend", // sync backend — Service.Import/Export own it
	"registry.NewYAMLCodec",      // pack codec — wire injects it
}

// seamViolation is one detected breach of the seam, carrying enough context to
// name the offender loudly in the test failure.
type seamViolation struct {
	pkgDir string // the offending cmd package directory
	file   string // the specific source file (reference scan); "" for imports
	detail string // the forbidden import path or constructor reference
	rule   string // the matched forbidden rule
}

func (v seamViolation) String() string {
	if v.file != "" {
		return fmt.Sprintf("%s references %q (matched forbidden rule %q)",
			filepath.Join(v.pkgDir, v.file), v.detail, v.rule)
	}
	return fmt.Sprintf("package %s directly imports %q (matched forbidden rule %q)",
		v.pkgDir, v.detail, v.rule)
}

// forbiddenImportRule returns the forbidden pattern importPath matches, or "" if
// the import is allowed. It is the single matcher both the real scan and the
// teeth table exercise, so a green teeth test is proof the real scan has bite.
func forbiddenImportRule(importPath string) string {
	for _, bad := range forbiddenCmdImports {
		if strings.Contains(importPath, bad) {
			return bad
		}
	}
	return ""
}

// scanCmdSeam walks cmdRoot and enumerates EVERY buildable Go package beneath it
// (the tree-walk that makes a new cmd/<x> covered automatically). For each
// package it returns a violation per direct non-test import matching the
// forbidden set, plus one per forbidden constructor reference found in a non-test
// source file. It returns the list of package dirs it scanned so the caller can
// assert the walk actually found the cmd packages (never passes vacuously).
func scanCmdSeam(cmdRoot string) (scanned []string, violations []seamViolation, err error) {
	// UseAllFiles ignores build constraints so a build-tagged cmd file cannot
	// hide a forbidden import from the current GOOS/GOARCH. All cmd/* files are
	// package main, so this never trips the multiple-package guard.
	ctx := build.Default
	ctx.UseAllFiles = true

	walkErr := filepath.WalkDir(cmdRoot, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if !d.IsDir() {
			return nil
		}
		// Skip tool-ignored dirs (testdata, and names starting with "." or "_"),
		// but never skip the root itself.
		if path != cmdRoot {
			name := d.Name()
			if name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
		}
		pkg, ierr := ctx.ImportDir(path, 0)
		if ierr != nil {
			// A dir with no buildable Go files is not a package — keep walking.
			if _, ok := ierr.(*build.NoGoError); ok {
				return nil
			}
			return fmt.Errorf("import dir %q: %w", path, ierr)
		}
		scanned = append(scanned, path)

		// (a) forbidden direct imports of the shipping (non-test) package.
		for _, imp := range pkg.Imports {
			if rule := forbiddenImportRule(imp); rule != "" {
				violations = append(violations, seamViolation{pkgDir: path, detail: imp, rule: rule})
			}
		}
		// (b) forbidden constructor references in non-test source files.
		//
		// This is a deliberately CONSERVATIVE, fail-safe check: it does a raw
		// whole-file substring match, so it also matches a reference inside a
		// comment or string literal (it favors a false positive over letting a
		// real construction slip through). It scans only pkg.GoFiles — go/build
		// classifies _test.go into TestGoFiles, so test files are correctly
		// excluded. A cmd/* file that must legitimately mention one of these names
		// in prose should reword it rather than weaken this guard.
		for _, f := range pkg.GoFiles {
			src, rerr := os.ReadFile(filepath.Join(path, f))
			if rerr != nil {
				return rerr
			}
			for _, ref := range forbiddenCmdReferences {
				if strings.Contains(string(src), ref) {
					violations = append(violations, seamViolation{pkgDir: path, file: f, detail: ref, rule: ref})
				}
			}
		}
		return nil
	})
	return scanned, violations, walkErr
}

// TestCmdImportSeam is WI-P2-7: it fails loudly (naming the offending cmd
// package and the forbidden import/reference) if any package under cmd/*
// regresses the seam. It passes on current main, which respects it.
func TestCmdImportSeam(t *testing.T) {
	cmdRoot, err := filepath.Abs("..") // this test runs in cmd/mizan; ".." is cmd/
	if err != nil {
		t.Fatalf("abs cmd root: %v", err)
	}

	scanned, violations, err := scanCmdSeam(cmdRoot)
	if err != nil {
		t.Fatalf("scan cmd seam: %v", err)
	}

	// Guard against a vacuous pass: EVERY top-level cmd/<x> dir that contains a
	// main.go must appear in the scanned set. This derives the expected packages
	// independently of the walk (a plain readdir for main.go), so it can't be
	// fooled by the same bug — and unlike a single hard-coded "mizan" name it
	// notices if any cmd package (e.g. cmd/mizan-desktop) is ever dropped or
	// silently skipped (emptied of Go files → NoGoError). If enumeration returned
	// nothing, this also fails.
	scannedSet := make(map[string]bool, len(scanned))
	for _, p := range scanned {
		scannedSet[filepath.Base(p)] = true
	}
	entries, err := os.ReadDir(cmdRoot)
	if err != nil {
		t.Fatalf("read cmd root %q: %v", cmdRoot, err)
	}
	var wantPkgs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(cmdRoot, e.Name(), "main.go")); err == nil {
			wantPkgs = append(wantPkgs, e.Name())
		}
	}
	if len(wantPkgs) == 0 {
		t.Fatal("no cmd/<x> package with a main.go found — enumeration expectation is broken")
	}
	for _, w := range wantPkgs {
		if !scannedSet[w] {
			t.Fatalf("seam scan did not enumerate cmd/%s; scanned=%v, want at least %v", w, scanned, wantPkgs)
		}
	}
	t.Logf("seam scan enumerated %d cmd package(s): %v (required: %v)", len(scanned), scanned, wantPkgs)

	for _, v := range violations {
		t.Errorf("SEAM VIOLATION: %s — cmd/* must depend on registry.Service/eval.Engine/config/wire ONLY (design §3.1/§3.11)", v)
	}
}

// TestForbiddenImportRuleHasTeeth proves the matcher catches every forbidden
// import shape and does NOT flag the allowed façade/stdlib imports — the "teeth"
// half of the acceptance (the real scan reuses this exact matcher).
func TestForbiddenImportRuleHasTeeth(t *testing.T) {
	planted := map[string]string{ // import path -> expected matched rule
		"github.com/ghchinoy/mizan/internal/registry/sqlite": "internal/registry/sqlite",
		"github.com/ghchinoy/mizan/internal/registry/sync":   "internal/registry/sync",
		"github.com/ghchinoy/mizan/internal/registry/codec":  "internal/registry/codec",
		"gopkg.in/yaml.v3":            "gopkg.in/yaml",
		"gopkg.in/yaml.v2":            "gopkg.in/yaml",
		"github.com/go-git/go-git/v5": "github.com/go-git",
		"github.com/go-git/go-git/v5/plumbing/transport/http": "github.com/go-git",
		"gopkg.in/src-d/go-git.v4":                            "gopkg.in/src-d/go-git",
		"github.com/libgit2/git2go/v34":                       "github.com/libgit2",
	}
	for imp, wantRule := range planted {
		if got := forbiddenImportRule(imp); got != wantRule {
			t.Errorf("forbiddenImportRule(%q) = %q, want %q — a forbidden import would slip through!", imp, got, wantRule)
		}
	}

	allowed := []string{
		"github.com/ghchinoy/mizan/internal/registry", // registry.Service façade
		"github.com/ghchinoy/mizan/internal/eval",     // eval.Engine façade
		"github.com/ghchinoy/mizan/internal/config",   // config
		"github.com/ghchinoy/mizan/internal/wire",     // composition root
		"context",
		"fmt",
		"os",
		"os/exec", // shelling out to git is allowed; embedding a git lib is not
		"github.com/spf13/cobra",
	}
	for _, imp := range allowed {
		if got := forbiddenImportRule(imp); got != "" {
			t.Errorf("forbiddenImportRule(%q) = %q, want \"\" — an allowed import is being flagged!", imp, got)
		}
	}
}

// TestScanCmdSeamCatchesPlantedImport is the end-to-end teeth demonstration: it
// runs the SAME walk+go/build+matcher pipeline the real test uses over a temp
// cmd tree that plants a forbidden YAML import AND a forbidden constructor
// reference, and asserts both are caught and named. This proves the guard has
// bite without touching (and breaking) the real cmd/* sources.
func TestScanCmdSeamCatchesPlantedImport(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "mizan-rogue")
	if err := os.MkdirAll(pkgDir, 0o750); err != nil {
		t.Fatalf("mkdir rogue pkg: %v", err)
	}
	// A rogue cmd binary that both imports a forbidden YAML lib and constructs a
	// pack backend directly — exactly the two seam breaches the guard forbids.
	src := "package main\n\n" +
		"import _ \"gopkg.in/yaml.v3\"\n\n" +
		"func main() {\n" +
		"\t// pretend to reach past the Service into the sync backend:\n" +
		"\t_ = \"registry.NewGitPackBackend\"\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write rogue main.go: %v", err)
	}

	scanned, violations, err := scanCmdSeam(root)
	if err != nil {
		t.Fatalf("scan planted tree: %v", err)
	}
	if len(scanned) != 1 {
		t.Fatalf("planted scan enumerated %d packages, want 1 (%v)", len(scanned), scanned)
	}

	var gotYAMLImport, gotBackendRef bool
	for _, v := range violations {
		if v.file == "" && v.rule == "gopkg.in/yaml" {
			gotYAMLImport = true
		}
		if v.detail == "registry.NewGitPackBackend" {
			gotBackendRef = true
		}
	}
	if !gotYAMLImport {
		t.Errorf("planted forbidden YAML import was NOT caught; violations=%v", violations)
	}
	if !gotBackendRef {
		t.Errorf("planted forbidden backend reference was NOT caught; violations=%v", violations)
	}
}

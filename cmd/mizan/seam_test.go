package main

// seam_test.go is the P2.1 guard for the architecture seam (design §3.1/§3.11):
// the command frontends (cmd/mizan, cmd/mizan-desktop) must depend on the
// registry.Service / eval.Engine / config / wire façades ONLY, never directly on
// a backend implementation (sqlite), the pack codec/sync internals, a YAML
// library, or a git library. Keeping cmd/* free of those direct imports is what
// makes a future model-B backend a one-line change in internal/wire.
//
// This is the lightweight, source-level version; the fuller automated
// seam-proof (WI-P2-7, incl. transitive analysis and mizan-desktop) lands in
// P2.6. It parses the non-test .go files under each cmd/* package and fails if
// any imports a forbidden path. It must not REGRESS the seam now.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenCmdImports are import-path substrings a cmd/* file must never import
// directly. Substring match keeps it robust to version suffixes and subpackages.
var forbiddenCmdImports = []string{
	"internal/registry/sqlite", // concrete Store backend — wire's job
	"internal/registry/codec",  // pack codec (should it ever be split out)
	"internal/registry/sync",   // sync backend (should it ever be split out)
	"gopkg.in/yaml",            // YAML lib — pack encoding is behind the Service
	"github.com/go-git",        // git libs — Mizan shells out, never embeds git
	"gopkg.in/src-d/go-git",
}

func TestCmdImportSeam(t *testing.T) {
	// Each cmd/* package relative to this test file (cmd/mizan).
	cmdDirs := []string{".", "../mizan-desktop"}

	for _, dir := range cmdDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			t.Fatalf("abs %q: %v", dir, err)
		}
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			continue // cmd/mizan-desktop may not exist in every checkout
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			t.Fatalf("read dir %q: %v", abs, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(abs, name)
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %q: %v", path, err)
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbiddenCmdImports {
					if strings.Contains(p, bad) {
						t.Errorf("%s imports forbidden package %q (seam violation): cmd/* must use registry.Service/eval.Engine/config/wire only", path, p)
					}
				}
			}
		}
	}
}

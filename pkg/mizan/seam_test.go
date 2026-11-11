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

package mizan

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

const modulePrefix = "github.com/ghchinoy/mizan"

var forbiddenSDKTransitiveImports = []string{
	"modernc.org/sqlite",
	"github.com/ghchinoy/mizan/internal/sqliteutil",
	"github.com/ghchinoy/mizan/internal/registry/sqlite",
	"github.com/ghchinoy/mizan/internal/results/sqlite",
	"cloud.google.com/go/storage",
	"github.com/ghchinoy/mizan/internal/asset/gcs",
	"github.com/ghchinoy/mizan/internal/wire",
	"github.com/ghchinoy/mizan/internal/config",
	"github.com/joho/godotenv",
	"github.com/ghchinoy/mizan/internal/mcpserver",
	"github.com/spf13/cobra",
	"github.com/modelcontextprotocol/go-sdk",
	"github.com/ghchinoy/mizan/cmd/",
}

func matchForbiddenSDKImport(imp string) string {
	for _, rule := range forbiddenSDKTransitiveImports {
		if strings.Contains(imp, rule) {
			return rule
		}
	}
	return ""
}

func TestPkgMizanImportSeam(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("filepath.Abs(repoRoot) error = %v", err)
	}

	bctx := build.Default
	bctx.UseAllFiles = true

	// 1. Walk the full transitive closure of in-module packages reachable from pkg/mizan
	// and inspect every direct and transitive import path.
	visited := make(map[string]bool)
	var walkPkg func(importPath, chain string)
	walkPkg = func(importPath, chain string) {
		if rule := matchForbiddenSDKImport(importPath); rule != "" {
			t.Errorf("FORBIDDEN SDK DEPENDENCY: %s -> %s (matched %q)", chain, importPath, rule)
			return
		}
		if visited[importPath] {
			return
		}
		visited[importPath] = true

		if importPath != modulePrefix && !strings.HasPrefix(importPath, modulePrefix+"/") {
			return
		}
		rel := strings.TrimPrefix(importPath, modulePrefix+"/")
		pkgDir := filepath.Join(repoRoot, filepath.FromSlash(rel))
		pkg, ierr := bctx.ImportDir(pkgDir, 0)
		if ierr != nil {
			t.Fatalf("ImportDir(%q) error = %v", pkgDir, ierr)
		}
		if importPath == modulePrefix+"/pkg/mizan" {
			for _, direct := range pkg.Imports {
				if direct == "os/exec" {
					t.Errorf("pkg/mizan directly imports os/exec (SDK must never shell out)")
				}
			}
		}
		for _, imp := range pkg.Imports {
			walkPkg(imp, chain+" -> "+imp)
		}
	}

	startPkg := modulePrefix + "/pkg/mizan"
	walkPkg(startPkg, startPkg)
	if !visited[modulePrefix+"/internal/eval"] || !visited[modulePrefix+"/internal/registry"] || !visited[modulePrefix+"/internal/evalset"] {
		t.Fatalf("transitive walk did not visit expected internal packages; visited=%v", visited)
	}

	// 2. Parse non-test AST of pkg/mizan and ensure no exported type, function, or
	// struct field leaks unstable v1beta1 aiplatformpb types.
	pkgMizanDir := filepath.Join(repoRoot, "pkg", "mizan")
	pkgInfo, err := bctx.ImportDir(pkgMizanDir, 0)
	if err != nil {
		t.Fatalf("ImportDir(pkg/mizan) error = %v", err)
	}
	fset := token.NewFileSet()
	for _, fname := range pkgInfo.GoFiles {
		fpath := filepath.Join(pkgMizanDir, fname)
		fileAST, perr := parser.ParseFile(fset, fpath, nil, 0)
		if perr != nil {
			t.Fatalf("ParseFile(%q) error = %v", fpath, perr)
		}
		for _, decl := range fileAST.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() {
					assertNoAIPlatformPB(t, fname, d.Name.Name, d.Type)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					assertNoAIPlatformPB(t, fname, ts.Name.Name, ts.Type)
				}
			}
		}
	}
}

func assertNoAIPlatformPB(t *testing.T, file, symbol string, node ast.Node) {
	t.Helper()
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && strings.Contains(strings.ToLower(id.Name), "aiplatform") {
			t.Errorf("%s: exported symbol %s leaks %s.%s in public SDK surface", file, symbol, id.Name, sel.Sel.Name)
		}
		return true
	})
}

func TestForbiddenSDKImportRule_HasTeeth(t *testing.T) {
	cases := map[string]string{
		"modernc.org/sqlite":                                 "modernc.org/sqlite",
		"github.com/ghchinoy/mizan/internal/sqliteutil":      "github.com/ghchinoy/mizan/internal/sqliteutil",
		"github.com/ghchinoy/mizan/internal/registry/sqlite": "github.com/ghchinoy/mizan/internal/registry/sqlite",
		"github.com/ghchinoy/mizan/internal/results/sqlite":  "github.com/ghchinoy/mizan/internal/results/sqlite",
		"cloud.google.com/go/storage":                        "cloud.google.com/go/storage",
		"github.com/ghchinoy/mizan/internal/asset/gcs":       "github.com/ghchinoy/mizan/internal/asset/gcs",
		"github.com/ghchinoy/mizan/internal/wire":            "github.com/ghchinoy/mizan/internal/wire",
		"github.com/ghchinoy/mizan/internal/config":          "github.com/ghchinoy/mizan/internal/config",
		"github.com/joho/godotenv":                           "github.com/joho/godotenv",
		"github.com/ghchinoy/mizan/internal/mcpserver":       "github.com/ghchinoy/mizan/internal/mcpserver",
		"github.com/spf13/cobra":                             "github.com/spf13/cobra",
		"github.com/modelcontextprotocol/go-sdk/mcp":         "github.com/modelcontextprotocol/go-sdk",
		"github.com/ghchinoy/mizan/cmd/mizan":                "github.com/ghchinoy/mizan/cmd/",
	}
	for imp, want := range cases {
		if got := matchForbiddenSDKImport(imp); got != want {
			t.Errorf("matchForbiddenSDKImport(%q) = %q, want %q", imp, got, want)
		}
	}
	for _, allowed := range []string{
		"google.golang.org/genai",
		"github.com/ghchinoy/mizan/internal/eval",
		"github.com/ghchinoy/mizan/internal/registry",
		"github.com/ghchinoy/mizan/internal/evalset",
		"github.com/ghchinoy/mizan/internal/asset",
	} {
		if got := matchForbiddenSDKImport(allowed); got != "" {
			t.Errorf("matchForbiddenSDKImport(%q) = %q, want empty", allowed, got)
		}
	}
}

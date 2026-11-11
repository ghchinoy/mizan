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
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/evalset"
)

func TestPublicTypeParity(t *testing.T) {
	structPairs := []struct {
		name      string
		internalT reflect.Type
		publicT   reflect.Type
		allowOnly map[string]bool
	}{
		{name: "Result", internalT: reflect.TypeFor[eval.Result](), publicT: reflect.TypeFor[Result]()},
		{name: "Stats", internalT: reflect.TypeFor[eval.Stats](), publicT: reflect.TypeFor[Stats]()},
		{name: "TokenUsage", internalT: reflect.TypeFor[eval.TokenUsage](), publicT: reflect.TypeFor[TokenUsage]()},
		{name: "AppliedJudge", internalT: reflect.TypeFor[eval.AppliedAutorater](), publicT: reflect.TypeFor[AppliedJudge]()},
		{name: "FinishError", internalT: reflect.TypeFor[eval.FinishError](), publicT: reflect.TypeFor[FinishError]()},
		{name: "EvalSetResult", internalT: reflect.TypeFor[evalset.EvalSetResult](), publicT: reflect.TypeFor[EvalSetResult]()},
		{name: "MemberResult", internalT: reflect.TypeFor[evalset.MemberResult](), publicT: reflect.TypeFor[MemberResult]()},
		{name: "Aggregate", internalT: reflect.TypeFor[evalset.Aggregate](), publicT: reflect.TypeFor[Aggregate]()},
	}

	for _, sp := range structPairs {
		t.Run(sp.name, func(t *testing.T) {
			pubFields := make(map[string]bool, sp.publicT.NumField())
			for i := 0; i < sp.publicT.NumField(); i++ {
				f := sp.publicT.Field(i)
				if f.IsExported() {
					pubFields[f.Name] = true
				}
			}
			for i := 0; i < sp.internalT.NumField(); i++ {
				f := sp.internalT.Field(i)
				if !f.IsExported() {
					continue
				}
				if sp.allowOnly[f.Name] {
					continue
				}
				if !pubFields[f.Name] {
					t.Errorf("internal %s.%s is not mapped on mizan.%s (map it in pkg/mizan/types.go or explicitly allowlist)", sp.internalT, f.Name, sp.publicT.Name())
				}
			}
		})
	}

	// Verify 1:1 constant parity between internal/registry/model.go (MetricKind &
	// Modality) and pkg/mizan/types.go (Kind & Modality).
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("filepath.Abs(repoRoot) error = %v", err)
	}
	internalConsts := parseTypedStringConsts(t, filepath.Join(repoRoot, "internal", "registry", "model.go"))
	publicKinds := map[string]Kind{
		"KindPointwise":    KindPointwise,
		"KindPairwise":     KindPairwise,
		"KindRubric":       KindRubric,
		"KindCustomSchema": KindCustomSchema,
		"KindHeuristic":    KindHeuristic,
		"KindBoul":         KindBoul,
		"KindChoice":       KindChoice,
		"KindScore":        KindScore,
		"KindComputation":  KindComputation,
		"KindPrebuilt":     KindPrebuilt,
	}
	for name, val := range internalConsts["MetricKind"] {
		got, ok := publicKinds[name]
		if !ok {
			t.Errorf("internal registry.%s (%q) is missing from pkg/mizan Kind constants", name, val)
			continue
		}
		if string(got) != val {
			t.Errorf("mizan.%s = %q, want %q", name, got, val)
		}
	}

	publicModalities := map[string]Modality{
		"ModalityText":  ModalityText,
		"ModalityImage": ModalityImage,
		"ModalityAudio": ModalityAudio,
		"ModalityVideo": ModalityVideo,
		"ModalityMusic": ModalityMusic,
	}
	for name, val := range internalConsts["Modality"] {
		got, ok := publicModalities[name]
		if !ok {
			t.Errorf("internal registry.%s (%q) is missing from pkg/mizan Modality constants", name, val)
			continue
		}
		if string(got) != val {
			t.Errorf("mizan.%s = %q, want %q", name, got, val)
		}
	}
}

func parseTypedStringConsts(t *testing.T, filePath string) map[string]map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(%q) error = %v", filePath, err)
	}
	out := map[string]map[string]string{
		"MetricKind": {},
		"Modality":   {},
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			typeIdent, ok := vs.Type.(*ast.Ident)
			if !ok {
				continue
			}
			bucket, tracked := out[typeIdent.Name]
			if !tracked {
				continue
			}
			for i, ident := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unq, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("Unquote(%s) error = %v", lit.Value, err)
				}
				bucket[ident.Name] = unq
			}
		}
	}
	return out
}

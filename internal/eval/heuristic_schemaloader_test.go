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

package eval

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestRunHeuristic_JSONSchema_RejectsExternalRefs is the eval-run half of audit
// §5 cases 1, 3, 4: a json-schema-valid heuristic whose schema carries an
// external $ref must fail the run with a compile error (never a silent os.Open),
// even with all client seams nil. This mirrors the pack-validate coverage in the
// registry package so BOTH the validate-time and run-time compile sites are
// guarded.
func TestRunHeuristic_JSONSchema_RejectsExternalRefs(t *testing.T) {
	eng := NewEngine(nil, "", "")
	schemas := map[string]string{
		"file-absolute": `{"$ref":"file:///etc/hostname"}`,
		"relative":      `{"$ref":"secrets.json"}`,
		"http":          `{"$ref":"http://example.com/s.json"}`,
		"https":         `{"$ref":"https://example.com/s.json"}`,
	}
	for name, schema := range schemas {
		t.Run(name, func(t *testing.T) {
			tmpl := heuristicTemplate(registry.HeuristicSpec{
				Type:   registry.HeuristicJSONSchemaValid,
				Target: "out",
				Schema: schema,
			})
			_, err := eng.Run(context.Background(), tmpl, textInstance("out", `{"any":"doc"}`))
			if err == nil {
				t.Fatalf("Run with external $ref schema %s = nil error, want compile error", schema)
			}
		})
	}
}

// TestCompileHeuristicSchema_DoSRegression is the eval-side audit §5 case-5
// guard: a $ref to /dev/zero must be refused immediately, never triggering an
// unbounded read. Gated to non-Windows.
func TestCompileHeuristicSchema_DoSRegression(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/dev/zero is not available on Windows")
	}
	spec := &registry.HeuristicSpec{
		Type:   registry.HeuristicJSONSchemaValid,
		Target: "out",
		Schema: `{"$ref":"file:///dev/zero"}`,
	}
	done := make(chan error, 1)
	go func() {
		_, err := compileHeuristicSchema(spec)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("compileHeuristicSchema(file:///dev/zero) = nil, want immediate refusal (no unbounded read)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("compileHeuristicSchema(file:///dev/zero) did not return quickly — possible unbounded read/OOM regression")
	}
}

// TestRunHeuristic_JSONSchema_AllowsInlineRef is the eval-run half of audit §5
// case 6: a legitimate inline same-document "#/$defs/..." ref must still compile
// and validate — a conforming doc passes (score 1) and a non-conforming doc
// fails (score 0).
func TestRunHeuristic_JSONSchema_AllowsInlineRef(t *testing.T) {
	eng := NewEngine(nil, "", "")
	const schema = `{"type":"object","$defs":{"pos":{"type":"integer","minimum":0}},` +
		`"properties":{"age":{"$ref":"#/$defs/pos"}},"required":["age"]}`
	tmpl := heuristicTemplate(registry.HeuristicSpec{
		Type:   registry.HeuristicJSONSchemaValid,
		Target: "out",
		Schema: schema,
	})

	res, err := eng.Run(context.Background(), tmpl, textInstance("out", `{"age":5}`))
	if err != nil {
		t.Fatalf("Run(inline ref, conforming) = %v, want nil", err)
	}
	if res.Score == nil || *res.Score != 1.0 {
		t.Errorf("conforming doc score = %v, want 1.0", res.Score)
	}

	res, err = eng.Run(context.Background(), tmpl, textInstance("out", `{"age":-1}`))
	if err != nil {
		t.Fatalf("Run(inline ref, non-conforming) = %v, want nil", err)
	}
	if res.Score == nil || *res.Score != 0.0 {
		t.Errorf("non-conforming doc score = %v, want 0.0", res.Score)
	}
}

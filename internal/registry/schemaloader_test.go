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

package registry

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// externalRefSchemas enumerates the external-$ref shapes that MUST be refused at
// every compile site: file://, relative, and http(s)://. Each is a valid JSON
// document — the ONLY reason compilation must fail is the inline-only loader
// refusing to resolve the external reference (audit §5 cases 1, 3, 4).
var externalRefSchemas = map[string]string{
	"file-absolute": `{"$ref":"file:///etc/hostname"}`,
	"relative":      `{"$ref":"secrets.json"}`,
	"http":          `{"$ref":"http://example.com/s.json"}`,
	"https":         `{"$ref":"https://example.com/s.json"}`,
}

// TestValidateHeuristicSchemaRejectsExternalRefs covers audit §5 cases 1, 3, 4
// for the pack-validate heuristic-schema helper.
func TestValidateHeuristicSchemaRejectsExternalRefs(t *testing.T) {
	for name, schema := range externalRefSchemas {
		t.Run(name, func(t *testing.T) {
			err := validateHeuristicSchema(schema)
			if err == nil {
				t.Fatalf("validateHeuristicSchema(%s) = nil, want error refusing external $ref", schema)
			}
		})
	}
}

// TestValidateResponseSchemaRejectsExternalRefs covers audit §5 cases 1, 3, 4
// for the pack-validate custom_schema responseSchema helper (site #3).
func TestValidateResponseSchemaRejectsExternalRefs(t *testing.T) {
	cases := map[string]map[string]any{
		"file-absolute": {"$ref": "file:///etc/hostname"},
		"relative":      {"$ref": "secrets.json"},
		"http":          {"$ref": "http://example.com/s.json"},
		"https":         {"$ref": "https://example.com/s.json"},
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateResponseSchema(schema); err == nil {
				t.Fatalf("validateResponseSchema(%v) = nil, want error refusing external $ref", schema)
			}
		})
	}
}

// TestInlineOnlyCompilerDoSRegression is the audit §5 case-5 guard: a $ref to an
// unbounded special file must fail IMMEDIATELY with an error instead of provoking
// an unbounded read (memory exhaustion / hang) on the CI runner. Gated to
// non-Windows because /dev/zero is a POSIX device node.
func TestInlineOnlyCompilerDoSRegression(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/dev/zero is not available on Windows")
	}
	done := make(chan error, 1)
	go func() {
		done <- validateHeuristicSchema(`{"$ref":"file:///dev/zero"}`)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("validateHeuristicSchema(file:///dev/zero) = nil, want immediate refusal (no unbounded read)")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("validateHeuristicSchema(file:///dev/zero) did not return quickly — possible unbounded read/OOM regression")
	}
}

// TestInlineOnlyCompilerAllowsSameDocumentRef is the audit §5 case-6 guard: a
// legitimate inline same-document "#/$defs/..." ref MUST still compile and
// validate correctly (pass for a conforming doc, fail for a non-conforming one),
// proving the hardening does not regress inline $ref usage.
func TestInlineOnlyCompilerAllowsSameDocumentRef(t *testing.T) {
	const schema = `{"type":"object","$defs":{"pos":{"type":"integer","minimum":0}},` +
		`"properties":{"age":{"$ref":"#/$defs/pos"}},"required":["age"]}`

	if err := validateHeuristicSchema(schema); err != nil {
		t.Fatalf("validateHeuristicSchema(inline #/$defs ref) = %v, want nil", err)
	}

	c := NewInlineOnlyCompiler()
	if err := c.AddResource("inline.json", strings.NewReader(schema)); err != nil {
		t.Fatalf("AddResource: %v", err)
	}
	compiled, err := c.Compile("inline.json")
	if err != nil {
		t.Fatalf("Compile(inline #/$defs ref) = %v, want nil", err)
	}
	if err := compiled.Validate(map[string]any{"age": 5}); err != nil {
		t.Errorf("Validate({age:5}) = %v, want pass", err)
	}
	if err := compiled.Validate(map[string]any{"age": -1}); err == nil {
		t.Error("Validate({age:-1}) = nil, want validation failure")
	}
}

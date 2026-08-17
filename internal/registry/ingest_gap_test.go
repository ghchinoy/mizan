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
	"fmt"
	"strings"
	"testing"
)

// packYAML builds a minimal valid pack template document with the given id and
// autorater model. An empty model omits the autorater block.
func packYAML(id, model string) []byte {
	var sb strings.Builder
	sb.WriteString("apiVersion: mizan.dev/v1alpha1\n")
	sb.WriteString("kind: MetricTemplate\n")
	fmt.Fprintf(&sb, "metadata:\n  id: %s\n", id)
	sb.WriteString("spec:\n  kind: pointwise\n  metricPromptTemplate: \"rate {{response}}\"\n")
	if model != "" {
		fmt.Fprintf(&sb, "  autorater:\n    model: %q\n", model)
	}
	return []byte(sb.String())
}

// --- HIGH-1: autorater.model ingest validation -------------------------------

func TestUnmarshalRejectsProjectScopedAutoraterModel(t *testing.T) {
	c := NewYAMLCodec()
	m := "projects/attacker/locations/us-central1/publishers/google/models/gemini-2.5-flash"
	_, err := c.Unmarshal(packYAML("evil/x", m))
	if err == nil {
		t.Fatal("expected project-scoped autorater.model to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "project-scoped") {
		t.Errorf("error = %q, want it to mention 'project-scoped'", err)
	}
}

func TestUnmarshalRejectsDotDotAutoraterModel(t *testing.T) {
	c := NewYAMLCodec()
	if _, err := c.Unmarshal(packYAML("acme/x", "../../etc/passwd")); err == nil {
		t.Fatal("expected '..'-bearing autorater.model to be rejected, got nil")
	}
}

func TestUnmarshalRejectsMalformedBareAutoraterModel(t *testing.T) {
	c := NewYAMLCodec()
	if _, err := c.Unmarshal(packYAML("acme/x", "bad model!\ninjected")); err == nil {
		t.Fatal("expected malformed bare autorater.model to be rejected, got nil")
	}
}

func TestUnmarshalAcceptsBareAutoraterModel(t *testing.T) {
	c := NewYAMLCodec()
	got, err := c.Unmarshal(packYAML("acme/x", "gemini-2.5-pro"))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.AutoraterModel != "gemini-2.5-pro" {
		t.Errorf("AutoraterModel = %q, want %q", got.AutoraterModel, "gemini-2.5-pro")
	}
}

// A publishers/.../<id> form is accepted but stored as the BARE id only, so the
// value is clean at rest.
func TestUnmarshalStoresPublisherFormAsBare(t *testing.T) {
	c := NewYAMLCodec()
	got, err := c.Unmarshal(packYAML("acme/x", "publishers/google/models/gemini-2.5-pro"))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.AutoraterModel != "gemini-2.5-pro" {
		t.Errorf("AutoraterModel = %q, want bare %q", got.AutoraterModel, "gemini-2.5-pro")
	}
}

// Regression: the shipped google-brand fixture still imports and keeps its bare
// publisher-relative model.
func TestUnmarshalFixtureAutoraterStaysBare(t *testing.T) {
	c := NewYAMLCodec()
	got, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal fixture: %v", err)
	}
	if got.AutoraterModel != "gemini-2.5-pro" {
		t.Errorf("fixture AutoraterModel = %q, want %q", got.AutoraterModel, "gemini-2.5-pro")
	}
}

// --- LOW-1: template id shape guard ------------------------------------------

func TestUnmarshalRejectsBadTemplateID(t *testing.T) {
	c := NewYAMLCodec()
	cases := map[string]string{
		"traversal":       "../../foo",
		"nested-slash":    "a/b/c",
		"control-char":    "ns/a\x1b[31m",
		"uppercase":       "NS/Slug",
		"missing-slug":    "ns",
		"space":           "ns/a b",
		"leading-slash":   "/ns",
		"empty-namespace": "/slug",
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Unmarshal(packYAML(id, "")); err == nil {
				t.Errorf("expected id %q to be rejected, got nil", id)
			}
		})
	}
}

func TestUnmarshalAcceptsFixtureTemplateID(t *testing.T) {
	c := NewYAMLCodec()
	if _, err := c.Unmarshal(packYAML("google-brand/video-brand-alignment", "")); err != nil {
		t.Errorf("valid namespaced id rejected: %v", err)
	}
}

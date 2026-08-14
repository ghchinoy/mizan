package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// TestRenderTemplateSurfacesMetadata proves renderTemplate emits the authoring
// metadata rows (Version, License, Authors, Input[..]) added alongside the new
// registry create/update flags, and that joinAuthors formats a Name+Email author
// as "Name <email>". This closes the coverage gap where sampleTemplate() leaves
// these fields empty, so the metadata branches never render under TestGolden.
func TestRenderTemplateSurfacesMetadata(t *testing.T) {
	outputFormat = outputTable
	tmpl := &registry.MetricTemplate{
		ID:             "team/quality",
		Name:           "Quality",
		Kind:           registry.KindPointwise,
		Modalities:     []registry.Modality{registry.ModalityText},
		AutoraterModel: "gemini-2.5-flash",
		Version:        "1.2.3",
		License:        "Apache-2.0",
		Authors: []registry.Author{
			{Name: "Jane Doe", Email: "jane@example.com"},
			{Name: "No Email"},
		},
		Inputs: []registry.InputSpec{
			{Name: "response", Modality: registry.ModalityText, Required: true},
			{Name: "image", Modality: registry.ModalityImage, Required: false},
		},
	}

	var buf bytes.Buffer
	if err := renderTemplate(&buf, tmpl); err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Version:",
		"1.2.3",
		"License:",
		"Apache-2.0",
		"Authors:",
		"Jane Doe <jane@example.com>", // Name+Email formatting via joinAuthors
		"No Email",                    // Name-only author still surfaced
		"Input[response]:",
		"text (required=true)",
		"Input[image]:",
		"image (required=false)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("renderTemplate output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// TestJoinAuthorsFormats exercises joinAuthors directly across the Name+Email,
// Name-only, and Email-only branches (0% coverage before this).
func TestJoinAuthorsFormats(t *testing.T) {
	cases := []struct {
		name    string
		authors []registry.Author
		want    string
	}{
		{"name and email", []registry.Author{{Name: "Jane Doe", Email: "jane@example.com"}}, "Jane Doe <jane@example.com>"},
		{"name only", []registry.Author{{Name: "Jane Doe"}}, "Jane Doe"},
		{"email only", []registry.Author{{Email: "jane@example.com"}}, "jane@example.com"},
		{"multiple joined", []registry.Author{{Name: "A", Email: "a@x.com"}, {Name: "B"}}, "A <a@x.com>, B"},
		{"empty author skipped", []registry.Author{{}, {Name: "B"}}, "B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinAuthors(tc.authors); got != tc.want {
				t.Errorf("joinAuthors = %q, want %q", got, tc.want)
			}
		})
	}
}

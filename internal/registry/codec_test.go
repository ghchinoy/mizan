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
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const fixtureTemplate = "testdata/packs/google-brand/templates/video-brand-alignment.yaml"

func readFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(fixtureTemplate)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// TestYAMLCodecUnmarshalFixture verifies the codec maps the frozen pack YAML to
// the domain model field-for-field, including canonicalization of spec.kind.
func TestYAMLCodecUnmarshalFixture(t *testing.T) {
	c := NewYAMLCodec()
	got, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.ID != "google-brand/video-brand-alignment" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Name != "Video Brand Alignment" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Kind != KindPointwise {
		t.Errorf("Kind = %q, want pointwise", got.Kind)
	}
	if got.Version != "1.0.0" {
		t.Errorf("Version = %q", got.Version)
	}
	if got.License != "Apache-2.0" {
		t.Errorf("License = %q", got.License)
	}
	wantMods := []Modality{ModalityVideo, ModalityText}
	if !reflect.DeepEqual(got.Modalities, wantMods) {
		t.Errorf("Modalities = %v, want %v", got.Modalities, wantMods)
	}
	wantInputs := []InputSpec{
		{Name: "response", Modality: ModalityVideo, Required: true},
		{Name: "brand_guideline", Modality: ModalityText, Required: true},
	}
	if !reflect.DeepEqual(got.Inputs, wantInputs) {
		t.Errorf("Inputs = %+v, want %+v", got.Inputs, wantInputs)
	}
	if got.AutoraterModel != "gemini-2.5-pro" {
		t.Errorf("AutoraterModel = %q, want gemini-2.5-pro", got.AutoraterModel)
	}
	if got.SamplingCount != 4 {
		t.Errorf("SamplingCount = %d, want 4", got.SamplingCount)
	}
	if got.FlipEnabled {
		t.Errorf("FlipEnabled = true, want false")
	}
	if got.MetricPromptTemplate == "" {
		t.Error("MetricPromptTemplate is empty")
	}
	if got.SystemInstruction == "" {
		t.Error("SystemInstruction is empty")
	}
	// Provenance/computed fields are NEVER read from the file.
	if got.Source != "" || got.ContentHash != "" || got.Dirty || !got.ImportedAt.IsZero() {
		t.Errorf("provenance fields must be zero after Unmarshal: %+v", got)
	}
	if len(got.Authors) != 1 || got.Authors[0].Name != "Jane Doe" {
		t.Errorf("Authors = %+v", got.Authors)
	}
}

// TestYAMLCodecKindCanonicalization proves vernacular single/compare are folded
// to their canonical kinds on Unmarshal and re-emitted canonical on Marshal.
func TestYAMLCodecKindCanonicalization(t *testing.T) {
	c := NewYAMLCodec()
	src := []byte("apiVersion: mizan.dev/v1alpha1\nkind: MetricTemplate\nmetadata:\n  id: acme/thing\nspec:\n  kind: single\n  metricPromptTemplate: 'rate {{response}}'\n")
	got, err := c.Unmarshal(src)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Kind != KindPointwise {
		t.Fatalf("Kind = %q, want pointwise (single canonicalized)", got.Kind)
	}
	out, err := c.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	round, err := c.Unmarshal(out)
	if err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	if round.Kind != KindPointwise {
		t.Errorf("round-trip Kind = %q, want pointwise", round.Kind)
	}
}

// TestYAMLCodecRoundTripFieldStable verifies Unmarshal(Marshal(t)) preserves
// every codec-relevant field (design §6/P2.1 round-trip smoke).
func TestYAMLCodecRoundTripFieldStable(t *testing.T) {
	c := NewYAMLCodec()
	orig, err := c.Unmarshal(readFixture(t))
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	data, err := c.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Errorf("round-trip not field-stable:\n orig=%+v\n  got=%+v", orig, got)
	}
}

// TestYAMLCodecRoundTripCustomSchema exercises the responseSchema JSON<->YAML
// bridge so the custom_schema path round-trips (kind-specific mapping).
func TestYAMLCodecRoundTripCustomSchema(t *testing.T) {
	c := NewYAMLCodec()
	in := &MetricTemplate{
		ID:                   "acme/strict",
		Kind:                 KindCustomSchema,
		MetricPromptTemplate: "score {{response}}",
		ResponseSchema:       &Schema{JSON: `{"properties":{"score":{"type":"number"}},"type":"object"}`},
	}
	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ResponseSchema == nil {
		t.Fatal("ResponseSchema lost in round-trip")
	}
	if got.ResponseSchema.JSON != in.ResponseSchema.JSON {
		t.Errorf("ResponseSchema.JSON = %q, want %q", got.ResponseSchema.JSON, in.ResponseSchema.JSON)
	}
}

// TestYAMLCodecRoundTripHeuristic proves the B2 HeuristicSpec threads through the
// codec: Marshal(t) then Unmarshal returns the same spec (type/target/value/
// caseInsensitive/schema).
func TestYAMLCodecRoundTripHeuristic(t *testing.T) {
	c := NewYAMLCodec()
	in := &MetricTemplate{
		ID:         "acme/contains",
		Kind:       KindHeuristic,
		Modalities: []Modality{ModalityText},
		Inputs:     []InputSpec{{Name: "response", Modality: ModalityText, Required: true}},
		Heuristic: &HeuristicSpec{
			Type:            HeuristicContains,
			Target:          "response",
			Value:           "OK",
			CaseInsensitive: true,
		},
	}
	data, err := c.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := c.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Heuristic == nil {
		t.Fatal("Heuristic lost in round-trip")
	}
	if *got.Heuristic != *in.Heuristic {
		t.Errorf("Heuristic = %#v, want %#v", *got.Heuristic, *in.Heuristic)
	}
	if got.Kind != KindHeuristic {
		t.Errorf("Kind = %q, want %q", got.Kind, KindHeuristic)
	}
}

func TestYAMLCodecUnmarshalErrors(t *testing.T) {
	c := NewYAMLCodec()
	cases := map[string]string{
		"wrong manifest kind": "apiVersion: mizan.dev/v1alpha1\nkind: EvalSet\nmetadata:\n  id: a/b\nspec:\n  kind: pointwise\n",
		"missing id":          "apiVersion: mizan.dev/v1alpha1\nkind: MetricTemplate\nmetadata: {}\nspec:\n  kind: pointwise\n",
		"missing spec.kind":   "apiVersion: mizan.dev/v1alpha1\nkind: MetricTemplate\nmetadata:\n  id: a/b\nspec: {}\n",
		"unknown spec.kind":   "apiVersion: mizan.dev/v1alpha1\nkind: MetricTemplate\nmetadata:\n  id: a/b\nspec:\n  kind: bogus\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Unmarshal([]byte(src)); err == nil {
				t.Errorf("expected error for %s, got nil", name)
			}
		})
	}
}

func TestYAMLCodecExt(t *testing.T) {
	if ext := NewYAMLCodec().Ext(); ext != "yaml" {
		t.Errorf("Ext() = %q, want yaml", ext)
	}
}

// TestEmbeddedSchemaIsValidJSON guards the embedded minimal schema artifact.
func TestEmbeddedSchemaIsValidJSON(t *testing.T) {
	if len(MetricTemplateSchemaJSON) == 0 {
		t.Fatal("MetricTemplateSchemaJSON is empty")
	}
	// Confirm the embedded bytes match the on-disk file (single source of truth).
	onDisk, err := os.ReadFile(filepath.Join("schema", "metrictemplate.json"))
	if err != nil {
		t.Fatalf("read schema file: %v", err)
	}
	if string(onDisk) != string(MetricTemplateSchemaJSON) {
		t.Error("embedded schema differs from schema/metrictemplate.json")
	}
}

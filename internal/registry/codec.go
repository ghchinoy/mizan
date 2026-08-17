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
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	yaml "gopkg.in/yaml.v3"
)

// packAPIVersion is the frozen pack format version (design §3.2). The codec
// writes it on Marshal and accepts it on Unmarshal.
const packAPIVersion = "mizan.dev/v1alpha1"

// packKindMetricTemplate is the manifest `kind:` for a metric-template document.
// (A pack may also carry `kind: EvalSet` manifests — those are handled by a
// separate P2.2 path and are rejected here.)
const packKindMetricTemplate = "MetricTemplate"

// Codec converts a MetricTemplate to and from the on-disk pack encoding. The
// YAML implementation targets the frozen pack file format (design §3.2/§3.4); a
// future JSONCodec (Firestore/API wire) targets the same struct with no engine
// impact. It is the second of the three registry seams (Store, Codec,
// SyncBackend — design §3.1).
type Codec interface {
	Marshal(t *MetricTemplate) ([]byte, error)
	Unmarshal(data []byte) (*MetricTemplate, error)
	Ext() string // file extension without the dot, e.g. "yaml"
}

// YAMLCodec maps the frozen pack YAML (design §3.2/§3.4) 1:1 to a
// MetricTemplate. It is pure (no I/O, no clock, no creds) so it is trivially
// unit-testable and safe to run in the creds-free CI validator.
type YAMLCodec struct{}

// NewYAMLCodec returns the default YAML codec.
func NewYAMLCodec() YAMLCodec { return YAMLCodec{} }

// MarshalTemplate serializes t to canonical pack YAML using the default codec.
// It is the seam-friendly entry point for frontends (cmd/*) that need to write a
// pack manifest — e.g. `rubric generate`'s draft template — WITHOUT constructing
// the codec themselves (the codec is an internal detail wire injects into the
// Service). The output round-trips through Unmarshal / registry import unchanged.
func MarshalTemplate(t *MetricTemplate) ([]byte, error) {
	return YAMLCodec{}.Marshal(t)
}

// Ext returns the file extension the codec writes ("yaml").
func (YAMLCodec) Ext() string { return "yaml" }

// --- wire structs (the on-disk shape) ---------------------------------------
//
// These deliberately mirror the frozen pack YAML, NOT the domain model: the
// mapping between the two lives in Marshal/Unmarshal so the domain model and the
// file format can evolve independently. Comments in the source YAML are dropped
// on Unmarshal (yaml.v3 does not decode them into typed fields).

type packAuthor struct {
	Name  string `yaml:"name,omitempty"`
	Email string `yaml:"email,omitempty"`
}

type packMetadata struct {
	ID          string       `yaml:"id"`
	Name        string       `yaml:"name,omitempty"`
	Description string       `yaml:"description,omitempty"`
	Version     string       `yaml:"version,omitempty"`
	Authors     []packAuthor `yaml:"authors,omitempty"`
	Maintainers []string     `yaml:"maintainers,omitempty"`
	License     string       `yaml:"license,omitempty"`
	Tags        []string     `yaml:"tags,omitempty"`
}

type packInput struct {
	Name     string `yaml:"name"`
	Modality string `yaml:"modality,omitempty"`
	Required bool   `yaml:"required,omitempty"`
}

type packAutorater struct {
	// Model is the publisher-relative id ONLY (e.g. gemini-2.5-pro); the engine
	// expands it to the full resource name at eval time. A project-scoped
	// resource name must never be authored here (design §3.4).
	Model         string `yaml:"model,omitempty"`
	SamplingCount int32  `yaml:"samplingCount,omitempty"`
	FlipEnabled   bool   `yaml:"flipEnabled,omitempty"`
}

type packSpec struct {
	// Kind accepts the vernacular spellings single/compare in addition to the
	// canonical pointwise/pairwise; it is routed through NormalizeKind on
	// Unmarshal and always emitted canonical on Marshal (design §3.2).
	Kind                 string                       `yaml:"kind"`
	Modalities           []string                     `yaml:"modalities,omitempty"`
	Inputs               []packInput                  `yaml:"inputs,omitempty"`
	MetricPromptTemplate string                       `yaml:"metricPromptTemplate,omitempty"`
	SystemInstruction    string                       `yaml:"systemInstruction,omitempty"`
	CandidateFieldName   string                       `yaml:"candidateFieldName,omitempty"` // pairwise
	BaselineFieldName    string                       `yaml:"baselineFieldName,omitempty"`  // pairwise
	RubricGroups         map[string][]string          `yaml:"rubricGroups,omitempty"`       // rubric
	ResponseSchema       map[string]any               `yaml:"responseSchema,omitempty"`     // custom_schema (JSON-Schema object)
	RatingRubric         map[string]map[string]string `yaml:"ratingRubric,omitempty"`       // RFC-0001 §4.4
	RubricDetail         *RubricDetail                `yaml:"rubricDetail,omitempty"`       // RFC-0001 §4.4
	Autorater            packAutorater                `yaml:"autorater,omitempty"`
}

type packFile struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   packMetadata `yaml:"metadata"`
	Spec       packSpec     `yaml:"spec"`
}

// Unmarshal parses one pack YAML document into a MetricTemplate. It performs
// minimal structural validation (the full JSON-Schema pipeline lands in P2.2):
// the manifest kind must be MetricTemplate, metadata.id and spec.kind must be
// present, and spec.kind must normalize. Provenance/computed fields
// (Source/Dirty/ImportedAt/ContentHash) are NOT read from the file — they are
// owned by Import and the store (design §3.4).
func (c YAMLCodec) Unmarshal(data []byte) (*MetricTemplate, error) {
	var pf packFile
	// Decode via a LimitReader bounded by MaxTemplateFileBytes so the in-memory
	// decode is capped even when Unmarshal is handed a large []byte directly
	// (defense-in-depth for the size cap the pack reader already applies). This
	// bounds the INPUT bytes read, not the anchor/alias-expanded graph — yaml.v3
	// caps neither natively, so a billion-laughs document can still amplify in
	// memory; the size cap is the primary mitigation and full alias-bound
	// enforcement is a residual noted for a later phase (audit LOW-2). io.EOF (an
	// empty document) is not an error here — it falls through to the id-required
	// check below, preserving the prior yaml.Unmarshal("") behavior.
	//
	// KnownFields stays off BY DESIGN: the import codec is tolerant of unknown
	// keys (forward-compat — a newer pack may carry fields an older mizan does
	// not yet know, and import must not hard-fail on them or silently corrupt a
	// round-trip). Strict rejection of unknown keys is enforced instead at
	// `pack validate` (schema additionalProperties:false) BEFORE publish, where
	// an author gets an actionable error. This tolerant-import vs strict-validate
	// split is intentional (EM decision, P2.2 INFO-1): do not flip this to
	// KnownFields(true).
	dec := yaml.NewDecoder(io.LimitReader(bytes.NewReader(data), MaxTemplateFileBytes+1))
	if err := dec.Decode(&pf); err != nil && err != io.EOF {
		return nil, fmt.Errorf("registry: parse pack yaml: %w", err)
	}

	if pf.Kind != "" && pf.Kind != packKindMetricTemplate {
		return nil, fmt.Errorf("registry: unsupported manifest kind %q (want %q)", pf.Kind, packKindMetricTemplate)
	}
	if err := validateTemplateID(pf.Metadata.ID); err != nil {
		return nil, err
	}
	if pf.Spec.Kind == "" {
		return nil, fmt.Errorf("registry: template %q: spec.kind is required", pf.Metadata.ID)
	}
	kind, err := NormalizeKind(pf.Spec.Kind)
	if err != nil {
		return nil, fmt.Errorf("registry: template %q: %w", pf.Metadata.ID, err)
	}
	// Ingest-boundary autorater guard (design §3.4): reject a project-scoped or
	// ".."-bearing model and store only the cleaned bare/publisher-relative id, so
	// an untrusted pack can never smuggle a trusted call target into the store.
	autoraterModel, err := validateAutoraterModel(pf.Metadata.ID, pf.Spec.Autorater.Model)
	if err != nil {
		return nil, err
	}

	t := &MetricTemplate{
		ID:                   pf.Metadata.ID,
		Name:                 pf.Metadata.Name,
		Description:          pf.Metadata.Description,
		Version:              pf.Metadata.Version,
		Maintainers:          pf.Metadata.Maintainers,
		License:              pf.Metadata.License,
		Tags:                 pf.Metadata.Tags,
		Kind:                 kind,
		MetricPromptTemplate: pf.Spec.MetricPromptTemplate,
		SystemInstruction:    pf.Spec.SystemInstruction,
		CandidateFieldName:   pf.Spec.CandidateFieldName,
		BaselineFieldName:    pf.Spec.BaselineFieldName,
		RubricGroups:         pf.Spec.RubricGroups,
		RatingRubric:         pf.Spec.RatingRubric,
		RubricDetail:         pf.Spec.RubricDetail,
		AutoraterModel:       autoraterModel,
		SamplingCount:        pf.Spec.Autorater.SamplingCount,
		FlipEnabled:          pf.Spec.Autorater.FlipEnabled,
	}
	for _, a := range pf.Metadata.Authors {
		t.Authors = append(t.Authors, Author(a))
	}
	for _, m := range pf.Spec.Modalities {
		t.Modalities = append(t.Modalities, Modality(m))
	}
	for _, in := range pf.Spec.Inputs {
		t.Inputs = append(t.Inputs, InputSpec{
			Name:     in.Name,
			Modality: Modality(in.Modality),
			Required: in.Required,
		})
	}
	if len(pf.Spec.ResponseSchema) > 0 {
		// json.Marshal sorts map keys at every level → a deterministic, canonical
		// JSON string that hashes and round-trips stably.
		js, err := json.Marshal(pf.Spec.ResponseSchema)
		if err != nil {
			return nil, fmt.Errorf("registry: template %q: responseSchema: %w", pf.Metadata.ID, err)
		}
		t.ResponseSchema = &Schema{JSON: string(js)}
	}
	return t, nil
}

// Marshal serializes a MetricTemplate to canonical pack YAML. It always writes
// the canonical kind (so an authored `single` round-trips as `pointwise`),
// stamps the frozen apiVersion + manifest kind, and OMITS computed/provenance
// fields (contentHash, source, dirty, timestamps) — those never live in a pack
// file (design §3.4/§3.6).
func (c YAMLCodec) Marshal(t *MetricTemplate) ([]byte, error) {
	if t == nil {
		return nil, fmt.Errorf("registry: cannot marshal nil template")
	}
	pf := packFile{
		APIVersion: packAPIVersion,
		Kind:       packKindMetricTemplate,
		Metadata: packMetadata{
			ID:          t.ID,
			Name:        t.Name,
			Description: t.Description,
			Version:     t.Version,
			Maintainers: t.Maintainers,
			License:     t.License,
			Tags:        t.Tags,
		},
		Spec: packSpec{
			Kind:                 string(t.Kind),
			MetricPromptTemplate: t.MetricPromptTemplate,
			SystemInstruction:    t.SystemInstruction,
			CandidateFieldName:   t.CandidateFieldName,
			BaselineFieldName:    t.BaselineFieldName,
			RubricGroups:         t.RubricGroups,
			RatingRubric:         t.RatingRubric,
			RubricDetail:         t.RubricDetail,
			Autorater: packAutorater{
				Model:         t.AutoraterModel,
				SamplingCount: t.SamplingCount,
				FlipEnabled:   t.FlipEnabled,
			},
		},
	}
	for _, a := range t.Authors {
		pf.Metadata.Authors = append(pf.Metadata.Authors, packAuthor(a))
	}
	for _, m := range t.Modalities {
		pf.Spec.Modalities = append(pf.Spec.Modalities, string(m))
	}
	for _, in := range t.Inputs {
		pf.Spec.Inputs = append(pf.Spec.Inputs, packInput{
			Name:     in.Name,
			Modality: string(in.Modality),
			Required: in.Required,
		})
	}
	if t.ResponseSchema != nil && t.ResponseSchema.JSON != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(t.ResponseSchema.JSON), &m); err != nil {
			return nil, fmt.Errorf("registry: template %q: responseSchema is not a JSON object: %w", t.ID, err)
		}
		pf.Spec.ResponseSchema = m
	}
	return yaml.Marshal(pf)
}

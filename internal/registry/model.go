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

// Package registry defines Mizan's Metric Registry domain model and storage
// interface. There is no GCP-native resource for named judge templates, so
// Mizan owns this construct. A registry entry is materialized at eval time
// into the appropriate Vertex AI proto (see internal/eval).
package registry

import (
	"fmt"
	"time"
)

// Modality is the asset type a metric template accepts.
type Modality string

const (
	ModalityText  Modality = "text"
	ModalityImage Modality = "image"
	ModalityAudio Modality = "audio"
	ModalityVideo Modality = "video"
	// ModalityMusic is an audio subtype; may alias ModalityAudio in practice.
	ModalityMusic Modality = "music"
)

// validModalities is the accepted asset-modality set, the single source of truth
// for both ValidModality and ParseModality.
var validModalities = map[Modality]bool{
	ModalityText:  true,
	ModalityImage: true,
	ModalityAudio: true,
	ModalityVideo: true,
	ModalityMusic: true,
}

// ValidModality reports whether m is one of the accepted modalities.
func ValidModality(m Modality) bool { return validModalities[m] }

// ParseModality validates a user-supplied modality string and returns it as a
// Modality, or an error naming the allowed set. It is the parse-boundary guard
// the CLI's --input flag calls so a typo (e.g. "txt") fails clearly at authoring
// time instead of producing an input the eval/validation path later rejects.
func ParseModality(s string) (Modality, error) {
	m := Modality(s)
	if !ValidModality(m) {
		return "", fmt.Errorf("unknown modality %q (want one of: text, image, audio, video, music)", s)
	}
	return m, nil
}

// MetricKind selects the evaluation path used for a template.
type MetricKind string

const (
	KindPointwise MetricKind = "pointwise"
	KindPairwise  MetricKind = "pairwise"
	KindRubric    MetricKind = "rubric"
	// KindCustomSchema forces the direct genai fallback path (strict schema).
	KindCustomSchema MetricKind = "custom_schema"
)

// Vernacular kind aliases (ITEM C). These are plain-English spellings accepted
// only at the CLI/parse boundary and immediately normalized to a canonical
// MetricKind by NormalizeKind; they are NEVER stored or threaded downstream, so
// the eval engine and the Vertex request specs (PointwiseMetricSpec /
// PairwiseMetricSpec) only ever see the canonical kinds above.
//
//	single  -> pointwise  (score ONE response)
//	compare -> pairwise   (compare TWO responses, pick the better)
const (
	KindAliasSingle  = "single"  // a.k.a. pointwise
	KindAliasCompare = "compare" // a.k.a. pairwise
)

// NormalizeKind maps a user-supplied kind spelling to its canonical MetricKind.
// The canonical kinds (pointwise/pairwise/rubric/custom_schema) pass through
// unchanged and the vernacular aliases are folded to their canonical kind
// (single -> pointwise, compare -> pairwise). Any other value is rejected so a
// typo fails clearly at the parse boundary instead of deep in the eval path.
// Normalizing here — and only here — keeps all downstream logic and the Vertex
// request specs unchanged regardless of which spelling the caller used.
func NormalizeKind(s string) (MetricKind, error) {
	switch s {
	case KindAliasSingle:
		return KindPointwise, nil
	case KindAliasCompare:
		return KindPairwise, nil
	case string(KindPointwise), string(KindPairwise), string(KindRubric), string(KindCustomSchema):
		return MetricKind(s), nil
	default:
		return "", fmt.Errorf("unknown metric kind %q (want one of: single|pointwise, compare|pairwise, rubric, custom_schema)", s)
	}
}

// Author is an asserted contributor of a template (corroborated by git blame
// in the pack repo).
type Author struct {
	Name  string
	Email string
}

// InputSpec is a declared placeholder the template references, with its
// modality. Validation cross-checks these against the template body.
type InputSpec struct {
	Name     string
	Modality Modality
	Required bool
}

// Schema is a strict custom output schema (KindCustomSchema only). The concrete
// wiring to genai's ResponseSchema is a later work item (WI-P1-5); the field and
// type exist now so the model is complete and does not churn.
type Schema struct {
	// JSON is the raw JSON-schema definition of the desired response shape.
	JSON string
}

// RubricDetail is the OPTIONAL per-template rubric-detail configuration
// (RFC-0001 §4.4). Today it carries only Scale; it is a struct (not an inlined
// field) so future rubric-detail knobs are additive without a second template
// field. A nil *RubricDetail means "not declared" — behavior is exactly today's.
type RubricDetail struct {
	// Scale is the OPTIONAL Likert range the genai/global structured rubric path
	// scores on. Nil means "not declared" -> the engine's default (1-5) or the
	// eval-time --rubric-scale flag, per Engine.resolveRubricScale.
	Scale *RubricScale `yaml:"scale,omitempty" json:"scale,omitempty"`
}

// RubricScale is an inclusive integer Likert range [Min,Max] (RFC-0001 §4.4).
// Min must be < Max; only non-negative bounds are meaningful (mirroring the
// --rubric-scale flag's ParseRubricScale contract).
type RubricScale struct {
	Min int `yaml:"min" json:"min"`
	Max int `yaml:"max" json:"max"`
}

// RubricProvenance records that (and how) a template's RubricGroups were
// AI-drafted via adaptive generation (design §4.4/§4.7). A nil *RubricProvenance
// on a MetricTemplate means the rubric was hand-authored — existing templates are
// unaffected, so there is no migration and no behavior change. It is persisted,
// schema-validated, and INCLUDED in the content hash (Decision 3, following the
// RatingRubric precedent): editing either a generated rubric's criteria OR its
// provenance shifts the hash, which is what auditability wants.
//
// CROSS-TEAM CONTRACT: the Method field name is read by mizan-em-resultsstore's
// RubricRef.Method — do NOT rename it.
type RubricProvenance struct {
	// Method is how the rubric was produced, e.g. "adaptive-generated".
	Method         string       `yaml:"method"                   json:"method"`
	GeneratorModel string       `yaml:"generatorModel"           json:"generatorModel"`
	Recipe         string       `yaml:"recipe,omitempty"         json:"recipe,omitempty"`
	PromptTemplate string       `yaml:"promptTemplate,omitempty" json:"promptTemplate,omitempty"`
	SampleInputRef string       `yaml:"sampleInputRef"           json:"sampleInputRef"`
	GeneratedAt    time.Time    `yaml:"generatedAt"              json:"generatedAt"`
	APIVersion     string       `yaml:"apiVersion"               json:"apiVersion"`
	RubricMeta     []RubricMeta `yaml:"rubricMeta,omitempty"     json:"rubricMeta,omitempty"`
}

// RubricMeta preserves the API's per-criterion type/importance metadata that the
// flat RubricGroups (map[string][]string) cannot carry (Decision 2). Entries are
// kept in declared order and aligned 1:1 with the RubricGroups criteria, so no
// generation fidelity is lost even though the runnable model is unchanged.
type RubricMeta struct {
	Group      string `yaml:"group"                json:"group"`
	Criterion  string `yaml:"criterion"            json:"criterion"`
	Type       string `yaml:"type,omitempty"       json:"type,omitempty"`
	Importance string `yaml:"importance,omitempty" json:"importance,omitempty"`
	// Origin records whether this criterion was AI-generated or hand-authored in a
	// union-before-freeze draft (CUJ 9 / design §6.5 Option A). Values:
	// OriginAdaptiveGenerated | OriginHandAuthored. It is additive and OPTIONAL
	// (omitempty): a single-origin generated draft (CUJ 7) and every existing
	// hand-authored/generated template leave it empty, so absent means "no
	// per-criterion origin recorded" — no migration and no behavior change. When
	// present it is part of the content hash (it lives inside the hashed
	// *RubricProvenance, Decision 3), keeping a mixed-origin audit record honest.
	Origin string `yaml:"origin,omitempty" json:"origin,omitempty"`
}

// Per-criterion origin values for RubricMeta.Origin (design §6.5 Option A). These
// are the ONLY valid values; the strict pack schema enforces the same enum.
const (
	// OriginAdaptiveGenerated marks a criterion drafted by adaptive generation.
	OriginAdaptiveGenerated = "adaptive-generated"
	// OriginHandAuthored marks a criterion supplied by the user (union-before-freeze).
	OriginHandAuthored = "hand-authored"
)

// MetricTemplate is a stored, named autorater definition. This is the single
// in-memory model that the YAML codec (P2) and a future Firestore document
// mapping both target. See design/collaboration-design.md §6 (authoritative).
type MetricTemplate struct {
	// Identity / attribution
	ID          string // "<namespace>/<slug>", stable, primary key
	Name        string
	Description string
	Version     string // semver
	Authors     []Author
	Maintainers []string
	License     string
	Tags        []string

	// Behavior
	Kind                 MetricKind
	Modalities           []Modality
	Inputs               []InputSpec         // declared placeholders + modality
	MetricPromptTemplate string              // {{placeholder}} syntax
	SystemInstruction    string              //
	CandidateFieldName   string              // pairwise only
	BaselineFieldName    string              // pairwise only
	RubricGroups         map[string][]string // rubric only
	ResponseSchema       *Schema             // custom_schema only
	AutoraterModel       string              // publisher-relative id (e.g. gemini-2.5-flash);
	//                                          engine expands to full resource name at eval time
	SamplingCount int32 // AutoraterConfig.SamplingCount, 1-32
	FlipEnabled   bool  // AutoraterConfig.FlipEnabled (pairwise)

	// RatingRubric is an OPTIONAL, Vertex-aligned rating-band -> description map
	// per rubric group (RFC-0001 §4.4): group -> {"1":"…","3":"…","5":"…"}. It
	// makes a rubric template self-describing and is the payload a future async
	// LLMBasedMetricSpec/EvaluateDataset round-trip and promptfoo/Vertex adapters
	// map to a score scale.
	//
	// CARRY-AND-RESERVE (RFC-0001 §4.4 runtime-binding note + §11 item 3): in v1
	// this field is persisted and (once the P2 codec/schema land) schema-validated,
	// but it is DELIBERATELY NOT threaded into the eval runtime — native.go /
	// custom.go / rubric_structured.go scoring never read it. It is a visible
	// reserved field, not a stub. Do not wire it into scoring.
	RatingRubric map[string]map[string]string `yaml:"ratingRubric,omitempty" json:"ratingRubric,omitempty"`

	// RubricDetail is the OPTIONAL, persisted per-template rubric-detail
	// configuration (RFC-0001 §4.4 YAML: `rubricDetail: { scale: {min,max} }`).
	// When a Scale is declared, the genai/global structured path
	// (rubric_structured.go) honors it as the Likert range; when absent, behavior
	// is exactly today's default (1-5). It coexists with the eval-time
	// --rubric-detail run flag (see internal/eval Engine.resolveRubricScale for the
	// precedence: explicit run-flag scale > template scale > default 1-5).
	RubricDetail *RubricDetail `yaml:"rubricDetail,omitempty" json:"rubricDetail,omitempty"`

	// RubricProvenance is OPTIONAL adaptive-generation provenance (design §4.4):
	// it records that (and how) RubricGroups were AI-drafted. A nil pointer means
	// hand-authored — existing templates are unaffected (no migration, no behavior
	// change). It is persisted, schema-validated, and hashed (Decision 3).
	RubricProvenance *RubricProvenance `yaml:"rubricProvenance,omitempty" json:"rubricProvenance,omitempty"`

	// Provenance / sync (see collaboration-design.md §3.9)
	Source      string
	ContentHash string
	Dirty       bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ImportedAt  time.Time
}

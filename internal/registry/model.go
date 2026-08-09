// Package registry defines Mizan's Metric Registry domain model and storage
// interface. There is no GCP-native resource for named judge templates, so
// Mizan owns this construct. A registry entry is materialized at eval time
// into the appropriate Vertex AI proto (see internal/eval).
package registry

import "time"

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

// MetricKind selects the evaluation path used for a template.
type MetricKind string

const (
	KindPointwise MetricKind = "pointwise"
	KindPairwise  MetricKind = "pairwise"
	KindRubric    MetricKind = "rubric"
	// KindCustomSchema forces the direct genai fallback path (strict schema).
	KindCustomSchema MetricKind = "custom_schema"
)

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

	// Provenance / sync (see collaboration-design.md §3.9)
	Source      string
	ContentHash string
	Dirty       bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ImportedAt  time.Time
}

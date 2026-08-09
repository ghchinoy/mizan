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

// Schema is a placeholder for a strict custom output schema (KindCustomSchema).
// The concrete representation is finalized during implementation.
type Schema struct {
	// JSON is the raw JSON-schema definition of the desired response shape.
	JSON string
}

// MetricTemplate is a stored, named autorater definition.
type MetricTemplate struct {
	ID                   string
	Name                 string
	Description          string
	Kind                 MetricKind
	Modalities           []Modality
	MetricPromptTemplate string // {{placeholder}} syntax, matches proto field
	SystemInstruction    string
	CandidateFieldName   string // pairwise only
	BaselineFieldName    string // pairwise only
	ResponseSchema       *Schema
	AutoraterModel       string // e.g. gemini-2.5-pro
	SamplingCount        int32  // AutoraterConfig.SamplingCount, default 4
	FlipEnabled          bool   // AutoraterConfig.FlipEnabled, default true (pairwise)
	RubricGroup          map[string]string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

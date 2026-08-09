// Package eval is Mizan's evaluation engine. It materializes a stored
// registry.MetricTemplate into the appropriate Vertex AI Gen AI Evaluation
// Service request (native EvaluateInstances) or, for strict custom schemas,
// a direct genai GenerateContent call.
package eval

import (
	"context"

	"github.com/ghchinoy/mizan/internal/registry"
)

// AssetRef references a single placeholder value for an evaluation instance.
// For text, Text is enough; other modalities supply a local path or a
// pre-staged gs:// URI.
type AssetRef struct {
	Modality registry.Modality
	Text     string // ModalityText
	FilePath string // local file (read inline or staged to GCS)
	GCSUri   string // pre-staged asset
	MimeType string // detected or explicit
}

// Instance is a single evaluation input: named placeholders to content.
type Instance struct {
	Fields map[string]AssetRef
}

// Result is the outcome of a single evaluation.
type Result struct {
	Score          *float32
	PairwiseChoice string // "" unless pairwise
	Explanation    string
	RawOutput      []string       // if ReturnRawOutput
	CustomOutput   map[string]any // if KindCustomSchema
}

// Engine runs a metric template against an instance.
type Engine interface {
	Run(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error)
}

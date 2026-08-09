// Package eval is Mizan's evaluation engine. It materializes a stored
// registry.MetricTemplate into the appropriate Vertex AI Gen AI Evaluation
// Service request (native EvaluateInstances) or, for strict custom schemas,
// a direct genai GenerateContent call.
//
// P1 vertical slice: only the text pointwise path is wired end to end (via
// native.go). Other metric kinds return a clear "not implemented in P1 slice"
// error; later work items (WI-P1-4/5) fill them in without reshaping the
// engine.
package eval

import (
	"context"
	"errors"
	"fmt"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	gax "github.com/googleapis/gax-go/v2"

	"github.com/ghchinoy/mizan/internal/registry"
)

// errNotImplemented marks paths that are not part of the P1 vertical slice.
var errNotImplemented = errors.New("eval: not implemented in P1 slice")

// EvaluationClient is the narrow, mockable seam over the Vertex AI
// EvaluateInstances RPC. The concrete *aiplatform.EvaluationClient satisfies
// it, and unit tests supply a fake so the engine's spec materialization and
// result mapping are testable without live API calls.
type EvaluationClient interface {
	EvaluateInstances(ctx context.Context, req *aiplatformpb.EvaluateInstancesRequest, opts ...gax.CallOption) (*aiplatformpb.EvaluateInstancesResponse, error)
}

// AssetRef references a single placeholder value for an evaluation instance.
// For text, Text is enough; other modalities supply a local path or a
// pre-staged gs:// URI (handled in WI-P1-4, not this slice).
type AssetRef struct {
	Modality registry.Modality
	Text     string // ModalityText
	FilePath string // local file (staged to GCS in WI-P1-4)
	GCSUri   string // pre-staged asset (WI-P1-4)
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

// Engine runs a metric template against an instance. It depends only on the
// registry domain model and the narrow EvaluationClient seam.
type Engine struct {
	client    EvaluationClient
	projectID string
	location  string
}

// NewEngine constructs an Engine over the given client. projectID and location
// are used to expand the template's publisher-relative autorater model id into
// the full resource name the API requires (spike-core).
func NewEngine(client EvaluationClient, projectID, location string) *Engine {
	return &Engine{client: client, projectID: projectID, location: location}
}

// Run dispatches on the template's MetricKind. Only text pointwise is wired in
// the P1 slice.
func (e *Engine) Run(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	switch tmpl.Kind {
	case registry.KindPointwise:
		return e.runPointwise(ctx, tmpl, inst)
	case registry.KindPairwise:
		return Result{}, fmt.Errorf("%w: pairwise (WI-P1-4)", errNotImplemented)
	case registry.KindRubric:
		return Result{}, fmt.Errorf("%w: rubric (WI-P1-5)", errNotImplemented)
	case registry.KindCustomSchema:
		return Result{}, fmt.Errorf("%w: custom_schema (WI-P1-5)", errNotImplemented)
	default:
		return Result{}, fmt.Errorf("eval: unknown metric kind %q", tmpl.Kind)
	}
}

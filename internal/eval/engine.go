// Package eval is Mizan's evaluation engine. It materializes a stored
// registry.MetricTemplate into the appropriate Vertex AI Gen AI Evaluation
// Service request (native EvaluateInstances) or, for strict custom schemas,
// a direct genai GenerateContent call.
//
// Wired paths: text pointwise and rubric go through the native EvaluationClient
// (native.go); custom_schema goes through the direct genai path (custom.go).
// The native regional EvaluationClient (us-central1) and the genai client
// (location=global) are DISTINCT seams — see the two content converters in
// content.go and spike-core / spike-custom.
package eval

import (
	"context"
	"errors"
	"fmt"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/genai"

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

// GenaiClient is the narrow, mockable seam over the single genai
// GenerateContent call the custom_schema path uses. It intentionally wraps only
// that one method so a fake can be supplied in unit tests without any network
// access. The concrete client (see NewGenaiClient in custom.go) targets
// location=global and is DISTINCT from the native regional EvaluationClient.
//
// Its shape mirrors google.golang.org/genai's Models.GenerateContent exactly so
// the concrete *genai.Client's Models service satisfies it via a thin adapter.
type GenaiClient interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
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
// registry domain model and the two narrow client seams (EvaluationClient for
// the native path, GenaiClient for the custom_schema path).
type Engine struct {
	client    EvaluationClient
	genai     GenaiClient
	projectID string
	location  string
	retry     retryPolicy
}

// Option configures an Engine at construction time. New optional dependencies
// (e.g. a future asset.Stager for multimodal native eval, WI-P1-4) are added as
// further Option constructors WITHOUT changing NewEngine's signature.
type Option func(*Engine)

// WithGenaiClient sets the genai client used by the custom_schema path. It is
// supplied by the composition root (internal/wire) only when a custom_schema
// eval may run, so the native-only paths never build a genai client.
func WithGenaiClient(g GenaiClient) Option {
	return func(e *Engine) { e.genai = g }
}

// NewEngine constructs an Engine over the given native EvaluationClient.
// projectID and location are used to expand the template's publisher-relative
// autorater model id into the full resource name the API requires (spike-core).
// Optional dependencies are supplied via Option (e.g. WithGenaiClient).
func NewEngine(client EvaluationClient, projectID, location string, opts ...Option) *Engine {
	e := &Engine{
		client:    client,
		projectID: projectID,
		location:  location,
		retry:     defaultRetryPolicy(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Run dispatches on the template's MetricKind. Pointwise (text) and rubric use
// the native path; custom_schema uses the genai path. Pairwise and native
// multimodal are WI-P1-4.
func (e *Engine) Run(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	switch tmpl.Kind {
	case registry.KindPointwise:
		return e.runPointwise(ctx, tmpl, inst)
	case registry.KindRubric:
		return e.runRubric(ctx, tmpl, inst)
	case registry.KindCustomSchema:
		return e.runCustomSchema(ctx, tmpl, inst)
	case registry.KindPairwise:
		return Result{}, fmt.Errorf("%w: pairwise (WI-P1-4)", errNotImplemented)
	default:
		return Result{}, fmt.Errorf("eval: unknown metric kind %q", tmpl.Kind)
	}
}

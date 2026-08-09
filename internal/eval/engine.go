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
	"fmt"
	"time"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

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
// For text, Text is enough; other modalities supply a local path (staged to GCS
// by the engine via the configured asset.Stager) or a pre-staged gs:// URI.
type AssetRef struct {
	Modality registry.Modality
	Text     string // ModalityText
	FilePath string // local file (staged to GCS by the engine's Stager)
	GCSUri   string // pre-staged gs:// asset
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
	Stats          Stats          // per-run telemetry (WI-F4)
}

// Stats holds per-run telemetry (WI-F4). Duration is ALWAYS populated with the
// wall-clock time around the dispatch (see Engine.Run). TokenUsage is populated
// ONLY on the custom_schema / genai path (from resp.UsageMetadata); the native
// EvaluateInstances response exposes no token usage, so it is nil on the native
// pointwise/rubric/pairwise paths.
type Stats struct {
	Duration   time.Duration `json:"duration_ns"`
	TokenUsage *TokenUsage   `json:"token_usage,omitempty"`
}

// TokenUsage is the genai-path token breakdown lifted from
// GenerateContentResponseUsageMetadata. It is nil on the native path, which
// carries no usage metadata.
type TokenUsage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CandidatesTokens int32 `json:"candidates_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
}

// Engine runs a metric template against an instance. It depends only on the
// registry domain model and the two narrow client seams (EvaluationClient for
// the native path, GenaiClient for the custom_schema path).
type Engine struct {
	client       EvaluationClient
	genai        GenaiClient
	stager       asset.Stager
	projectID    string
	location     string
	defaultModel string // config default-model (WI-F3); "" falls back to BuiltinDefaultModel
	retry        retryPolicy
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

// WithDefaultModel sets the config-level default autorater model (WI-F3, from
// config.DefaultModel / MIZAN_DEFAULT_MODEL). It sits BELOW the flag override
// and the template's own AutoraterModel but ABOVE the built-in default in the
// precedence chain (see Engine.resolveModel). An empty value is a no-op: the
// chain then falls through to BuiltinDefaultModel.
func WithDefaultModel(model string) Option {
	return func(e *Engine) { e.defaultModel = model }
}

// WithStager sets the asset.Stager used to materialize non-text assets into the
// gs:// FileData the native ContentMap path requires (native EvaluateInstances
// accepts gs:// FileData ONLY; inline bytes are silently dropped — spike-core).
// It is supplied by the composition root (internal/wire) ONLY when a staging
// bucket is configured. When no Stager is set, a multimodal eval that needs to
// stage a local file fails at Run time with a clear asset.ErrNoBucket-style
// error; text-only and custom_schema-inline evals are unaffected.
func WithStager(s asset.Stager) Option {
	return func(e *Engine) { e.stager = s }
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

// RunOption configures a single Run call (WI-F3). It is distinct from the
// construction-time Option so a per-run override (e.g. --model) never mutates the
// Engine.
type RunOption func(*runConfig)

type runConfig struct {
	modelOverride string
}

// WithModel supplies a per-run autorater model override (the eval-time --model
// flag). It is the HIGHEST-precedence input to the resolution chain — see
// Engine.resolveModel.
func WithModel(model string) RunOption {
	return func(rc *runConfig) { rc.modelOverride = model }
}

// Run dispatches on the template's MetricKind. Pointwise (text + multimodal) and
// rubric use the native path; pairwise uses the native pairwise path;
// custom_schema uses the genai path. Non-text native assets are staged to gs://
// FileData via the configured Stager (spike-core: native accepts gs:// only).
//
// The autorater model is resolved ONCE here, uniformly for the native and genai
// paths, via the precedence chain (flag > template > config default > built-in),
// and the wall-clock duration is always recorded in Result.Stats (WI-F3/WI-F4).
func (e *Engine) Run(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, opts ...RunOption) (Result, error) {
	var rc runConfig
	for _, opt := range opts {
		opt(&rc)
	}
	model := e.resolveModel(tmpl, rc.modelOverride)
	// Reject a clearly-malformed model id (from the flag, template, or config
	// default) here, uniformly for the native and genai paths, so it fails with a
	// crisp LOCAL error before being composed into a Vertex resource name or sent
	// to the genai SDK, rather than being bounced by the remote API.
	if err := ValidateModel(model); err != nil {
		return Result{}, err
	}

	start := time.Now()
	res, err := e.dispatch(ctx, tmpl, inst, model)
	res.Stats.Duration = time.Since(start)
	return res, err
}

// dispatch routes to the per-kind path with the already-resolved model. Keeping
// resolution and timing in Run means every path shares one model chain and one
// wall-clock measurement.
func (e *Engine) dispatch(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
	switch tmpl.Kind {
	case registry.KindPointwise:
		return e.runPointwise(ctx, tmpl, inst, model)
	case registry.KindRubric:
		return e.runRubric(ctx, tmpl, inst, model)
	case registry.KindCustomSchema:
		return e.runCustomSchema(ctx, tmpl, inst, model)
	case registry.KindPairwise:
		return e.runPairwise(ctx, tmpl, inst, model)
	default:
		return Result{}, fmt.Errorf("eval: unknown metric kind %q", tmpl.Kind)
	}
}

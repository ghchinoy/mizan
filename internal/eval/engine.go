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
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	gax "github.com/googleapis/gax-go/v2"
	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/eval/diffusion"
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
	Score           *float32
	Passed          *bool    `json:"passed,omitempty"`           // if KindBoul
	Confidence      *float32 `json:"confidence,omitempty"`       // if KindBoul
	ChoiceSelection string   `json:"choice_selection,omitempty"` // if KindChoice
	PairwiseChoice  string   // "" unless pairwise
	Explanation     string
	RawOutput       []string       // if ReturnRawOutput
	CustomOutput    map[string]any // if KindCustomSchema, KindBoul, KindChoice, KindScore
	// RubricDetail is set by the rubric per-criterion transparency path
	// (runRubricStructured) to mark that CustomOutput carries the
	// {per_criterion, overall_score, explanation} structure. The renderer routes
	// on THIS explicit signal rather than sniffing CustomOutput's shape, so a
	// custom_schema result that happens to contain a "per_criterion" array is not
	// misrendered as a rubric table.
	RubricDetail bool `json:"rubric_detail,omitempty"`
	// Warnings carries non-fatal, user-visible notes produced during a run. The
	// rubric per-criterion path (runRubricStructured -> reconcileRubricOutput)
	// populates this with one entry per EXTRA (unauthored) criterion the judge
	// returned: extras are informative, not corrupting, so they are kept in
	// CustomOutput and surfaced here rather than dropped or turned into errors
	// (R-R2). The CLI prints these to stderr after the run (WI-F7 echo style).
	Warnings []string `json:"warnings,omitempty"`
	Stats    Stats    // per-run telemetry (WI-F4)
	// Applied is the RESOLVED autorater-as-applied — the model, sampling, flip,
	// and effective host/location Engine.Run actually used after the precedence
	// chain and R-GLOBAL routing (eval-results-store-design §4.3). Run populates it
	// ONLY on a successful run (err == nil); it stays nil on every error path, so a
	// failed run has no applied autorater. The results store reads this as a Go
	// STRUCT FIELD, not via JSON, so the field is tagged json:"-" and is NEVER
	// serialized in the eval command's output — keeping that output byte-for-byte
	// unchanged (eval-results-store-design §6). The AppliedAutorater snake_case
	// json tags are kept for any direct marshaling of that value elsewhere.
	Applied *AppliedAutorater `json:"-"`
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
	globalClient EvaluationClient // GLOBAL-host native client for global-only autoraters (R-GLOBAL); nil disables auto-routing
	genai        GenaiClient
	diffusion    diffusion.Client // DiffusionGemma client for local/Cloud Run discrete block diffusion decisions
	stager       asset.Stager
	projectID    string
	location     string
	defaultModel string    // config default-model (WI-F3); "" falls back to BuiltinDefaultModel
	noticeW      io.Writer // where the "forced global" routing notice is written (R-GLOBAL); nil -> os.Stderr
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

// WithGlobalClient sets the GLOBAL-host native EvaluationClient used to auto-route
// a global-only autorater (R-GLOBAL). The composition root (internal/wire) builds
// it via eval.NewClient(ctx, "global", cfg.APIEndpoint) — a DISTINCT client from
// the regional one so the eval call can move its whole HOST to the global endpoint
// (spike-eval-region-autorater: the host, not the autorater path, is decisive).
// When unset, auto-routing is disabled and a global-only judge fails as before
// (relevant only in tests / degraded wiring; wire always supplies it).
func WithGlobalClient(c EvaluationClient) Option {
	return func(e *Engine) { e.globalClient = c }
}

// WithNoticeWriter overrides where the "forced global" routing notice is written
// (default os.Stderr). Tests use it to capture the notice; production leaves it
// unset so the notice reaches the operator's stderr alongside the WI-F7 echo.
func WithNoticeWriter(w io.Writer) Option {
	return func(e *Engine) { e.noticeW = w }
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
	modelOverride  string
	engineOverride string // "vertex", "diffusion", "genai"

	// rubricDetail, when true, routes a KindRubric template through the genai
	// structured-output path (rubric per-criterion transparency) instead of the
	// native pointwise path. scaleMin/scaleMax are the (already-parsed and
	// validated) Likert bounds the judge scores on. They are meaningful only when
	// rubricDetail is set AND scaleSet is true.
	rubricDetail bool
	// scaleSet reports whether an EXPLICIT run-flag scale (--rubric-scale) was
	// supplied. When false, the structured path falls back to the template's
	// rubricDetail.scale (if declared) and then the built-in default (1-5) — see
	// Engine.resolveRubricScale. This lets a template-declared scale take effect
	// when the user relies on the default, while an explicit --rubric-scale still
	// wins (H2, RFC-0001 §4.4).
	scaleSet bool
	scaleMin int
	scaleMax int
}

// Default Likert bounds for the genai/global structured rubric path when neither
// an explicit --rubric-scale flag nor a template-declared rubricDetail.scale is
// present (RFC-0001 §4.4: "when absent, behavior is exactly today's default").
const (
	defaultRubricScaleMin = 1
	defaultRubricScaleMax = 5
)

// WithModel supplies a per-run autorater model override (the eval-time --model
// flag). It is the HIGHEST-precedence input to the resolution chain — see
// Engine.resolveModel.
func WithModel(model string) RunOption {
	return func(rc *runConfig) { rc.modelOverride = model }
}

// WithRubricDetail opts a single run into the rubric per-criterion transparency
// path (the eval-time --rubric-detail flag). When set AND the template is
// KindRubric, the run is routed through the genai structured-output path
// (location=global) instead of the native pointwise path, and the judge scores
// each authored criterion on the [min,max] Likert scale. min/max are the
// caller-parsed, validated scale bounds (see ParseRubricScale). It is a no-op on
// non-rubric templates beyond the explicit guard in Engine.Run.
func WithRubricDetail(min, max int) RunOption {
	return func(rc *runConfig) {
		rc.rubricDetail = true
		rc.scaleSet = true
		rc.scaleMin = min
		rc.scaleMax = max
	}
}

// WithRubricDetailDefaultScale opts a single run into the rubric per-criterion
// transparency path WITHOUT an explicit Likert scale (the --rubric-detail flag
// given without --rubric-scale). The scale is then resolved from the template's
// rubricDetail.scale if it declares one, otherwise the built-in default (1-5) —
// see Engine.resolveRubricScale. Use this instead of WithRubricDetail when the
// caller did not explicitly choose a scale, so a template-declared scale can take
// effect (H2, RFC-0001 §4.4).
func WithRubricDetailDefaultScale() RunOption {
	return func(rc *runConfig) {
		rc.rubricDetail = true
		rc.scaleSet = false
	}
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
	// MODEL-BYPASS (design §4.B): a heuristic is a deterministic, credential-free
	// check — it resolves NO autorater, validates no model, and stamps no
	// AppliedAutorater. Guard the whole model-resolution/validation path (and the
	// autorater-stamping path below) so res.Applied stays nil for a heuristic run
	// and runHeuristic never receives a resolved model. This is the single most
	// important interface detail in B2.
	var model string
	if tmpl.Kind != registry.KindHeuristic {
		model = e.resolveModel(tmpl, rc.modelOverride)
		// Reject a clearly-malformed model id (from the flag, template, or config
		// default) here, uniformly for the native and genai paths, so it fails with a
		// crisp LOCAL error before being composed into a Vertex resource name or sent
		// to the genai SDK, rather than being bounced by the remote API.
		if err := ValidateModel(model); err != nil {
			return Result{}, err
		}
	}
	// The rubric-detail lever only applies to rubric templates — reject it on any
	// other kind with a crisp local error before dispatch.
	if rc.rubricDetail && tmpl.Kind != registry.KindRubric {
		return Result{}, fmt.Errorf("eval: --rubric-detail only applies to rubric templates (template %q is kind %q)", tmpl.ID, tmpl.Kind)
	}

	// Symmetric client-side field validation (FIX-1). The per-kind paths build the
	// request instance FROM the template's placeholders, not from the supplied
	// fields, so a field whose key matches no placeholder is silently dropped and
	// never reaches the judge — producing a confidently-wrong score rather than an
	// error (the owner's "silent-drop" trap). This check is the reverse of the
	// existing "instance is missing values for template variables" guard
	// (content.go): validate here, at the one chokepoint that has both tmpl and
	// inst, so it covers the native AND genai/custom_schema paths uniformly BEFORE
	// dispatch.
	//
	// An EMPTY metric prompt template is deliberately left to the per-kind path's
	// own "empty metric prompt template" guard, which is a more precise diagnosis
	// than "no placeholders"; skipping it here keeps that error's precedence.
	if tmpl.MetricPromptTemplate != "" {
		// For pairwise, the structural placeholder-parity check (baseline/candidate
		// must appear as {{...}} in the prompt) is a more specific and actionable
		// error than the generic unknown-field message, so run it first when the
		// field names are set. It is idempotent with runPairwise's own call.
		if tmpl.Kind == registry.KindPairwise && tmpl.BaselineFieldName != "" && tmpl.CandidateFieldName != "" {
			if err := validatePairwisePlaceholders(tmpl); err != nil {
				return Result{}, err
			}
		}
		if err := validateInstanceFields(expectedInstanceFields(tmpl), tmpl, inst); err != nil {
			return Result{}, err
		}
	}

	start := time.Now()
	res, err := e.dispatch(ctx, tmpl, inst, model, rc)
	res.Stats.Duration = time.Since(start)
	if err != nil {
		// A failed run has no applied autorater: leave Result.Applied nil on EVERY
		// error path (dispatch errors here, and the validation errors that returned
		// early above) so "the run did not happen" is represented uniformly.
		return res, err
	}
	// MODEL-BYPASS (design §4.B): a heuristic has no autorater, so leave
	// res.Applied nil (the results store records an empty AppliedAutorater for it).
	// Duration is still recorded above (cheap, useful for B3).
	if tmpl.Kind == registry.KindHeuristic {
		return res, nil
	}
	// Record the RESOLVED autorater-as-applied on EVERY kind/path uniformly, right
	// where Run already stamps the shared telemetry (§4.3). EffectiveHost/Location
	// reuse the SAME resolution + routing decision the run took, via Resolve — no
	// re-derivation of routing here. Populated only on success so a caller (the
	// results store) sees exactly what actually ran.
	target := e.Resolve(tmpl, rc.modelOverride, rc.rubricDetail)
	effectiveHost := "regional"
	if target.Location == globalLocation || target.Location == "" {
		effectiveHost = "global"
	}
	res.Applied = &AppliedAutorater{
		Model:         model,
		SamplingCount: tmpl.SamplingCount,
		FlipEnabled:   tmpl.FlipEnabled,
		EffectiveHost: effectiveHost,
		Location:      target.Location,
		ModelSource:   modelSource(rc.modelOverride, tmpl.AutoraterModel, e.defaultModel),
	}
	return res, nil
}

// dispatch routes to the per-kind path with the already-resolved model. Keeping
// resolution and timing in Run means every path shares one model chain and one
// wall-clock measurement.
func (e *Engine) dispatch(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string, rc runConfig) (Result, error) {
	if rc.engineOverride == "diffusion" || rc.engineOverride == "diffgemma" {
		return e.runDiffusion(ctx, tmpl, inst, model)
	}

	switch tmpl.Kind {
	case registry.KindBoul:
		return e.runBoul(ctx, tmpl, inst, model)
	case registry.KindChoice:
		return e.runChoice(ctx, tmpl, inst, model)
	case registry.KindScore:
		return e.runScore(ctx, tmpl, inst, model, rc)
	case registry.KindPointwise:
		return e.runPointwise(ctx, tmpl, inst, model)
	case registry.KindRubric:
		if rc.rubricDetail {
			min, max, scaleWarning := e.resolveRubricScale(tmpl, rc)
			res, err := e.runRubricStructured(ctx, tmpl, inst, model, min, max)
			// Surface the non-fatal template-scale fallback warning on the SAME
			// Result.Warnings channel H3 uses. Only on success — a failed run
			// returns a zero Result and the warning would be noise.
			if err == nil && scaleWarning != "" {
				res.Warnings = append(res.Warnings, scaleWarning)
			}
			return res, err
		}
		return e.runRubric(ctx, tmpl, inst, model)
	case registry.KindCustomSchema:
		return e.runCustomSchema(ctx, tmpl, inst, model)
	case registry.KindPairwise:
		return e.runPairwise(ctx, tmpl, inst, model)
	case registry.KindHeuristic:
		// Deterministic, credential-free: a FREE function (not a method), so it
		// structurally cannot reach e.client/e.globalClient/e.genai — NO client, NO
		// model, NO network (design §4.B invariant).
		return runHeuristic(tmpl, inst)
	default:
		return Result{}, fmt.Errorf("eval: unknown metric kind %q", tmpl.Kind)
	}
}

// resolveRubricScale determines the Likert [min,max] the genai/global structured
// rubric path scores on, applying the H2 precedence (RFC-0001 §4.4):
//
//  1. an EXPLICIT run-flag scale (--rubric-scale, rc.scaleSet) — highest;
//  2. the template's declared rubricDetail.scale, if any AND valid;
//  3. the built-in default (1-5).
//
// This keeps the existing --rubric-detail run flag working exactly as before
// (WithRubricDetail sets scaleSet), while letting a template-declared scale take
// effect when the caller relies on the default (WithRubricDetailDefaultScale). A
// template that declares NO scale yields the same 1-5 default as today, so this
// is additive and back-compatible.
//
// A template-declared scale is VALIDATED against the same contract the
// --rubric-scale flag is held to (rubricScaleBoundsValid: non-negative, min<max).
// Unlike the flag path — which fails fast via ParseRubricScale — a template scale
// arrives already-parsed with no CLI chokepoint to reject it, so an inverted or
// degenerate scale (min>=max, negative) would otherwise reach the judge prompt
// verbatim and let the clamp collapse every score while the run "succeeds"
// silently. When the template scale is invalid we do NOT hard-fail (consistent
// with H3's non-fatal philosophy for the genai/global path): we FALL BACK to the
// default 1-5 and return a NON-FATAL warning (surfaced on Result.Warnings) naming
// the offending field and the default now in effect. The returned warning is
// empty ("") whenever no fallback occurred.
func (e *Engine) resolveRubricScale(tmpl registry.MetricTemplate, rc runConfig) (min, max int, warning string) {
	if rc.scaleSet {
		return rc.scaleMin, rc.scaleMax, ""
	}
	if tmpl.RubricDetail != nil && tmpl.RubricDetail.Scale != nil {
		tmin, tmax := tmpl.RubricDetail.Scale.Min, tmpl.RubricDetail.Scale.Max
		if !rubricScaleBoundsValid(tmin, tmax) {
			return defaultRubricScaleMin, defaultRubricScaleMax, fmt.Sprintf(
				"mizan: template rubricDetail.scale (%d-%d) is invalid (min must be non-negative and less than max); using the default %d-%d scale instead",
				tmin, tmax, defaultRubricScaleMin, defaultRubricScaleMax)
		}
		return tmin, tmax, ""
	}
	return defaultRubricScaleMin, defaultRubricScaleMax, ""
}

// expectedInstanceFields returns the placeholder key set the given template's
// per-kind path will read from the instance when building its request. It is the
// SAME source each path already uses, so validating inst.Fields against it is
// exact (no false positives):
//
//   - pointwise / rubric / custom_schema: extractVars(MetricPromptTemplate) — the
//     rubric and rubric-detail paths append instruction blocks that carry no
//     placeholders, and renderGenaiPrompt runs varPattern over the same template.
//   - pairwise: pairwiseKeys(tmpl) — the baseline/candidate field names plus any
//     extra {{var}} placeholders in the prompt.
func expectedInstanceFields(tmpl registry.MetricTemplate) []string {
	if tmpl.Kind == registry.KindPairwise {
		return pairwiseKeys(tmpl)
	}
	return extractVars(tmpl.MetricPromptTemplate)
}

// validateInstanceFields fails LOUD, client-side, when a supplied instance field
// would never reach the judge. It is symmetric with the existing forward check
// ("instance is missing values for template variables", content.go): that guards
// a placeholder with no value; this guards a value with no placeholder.
//
// It returns an error when:
//
//	(a) inst.Fields carries a key that matches NO expected placeholder — the
//	    value would be silently dropped from the request (case D, and the owner's
//	    case A once the empty-expected branch below is factored out), or
//	(b) the template references no {{placeholders}} at all yet fields were
//	    supplied — the values cannot reach the judge; the template is almost
//	    certainly mis-authored (missing a {{...}} placeholder). This is the exact
//	    silent-drop that returned a confidently-wrong score for the owner.
//
// LIMITATION / follow-up: this guard is invoked from a single chokepoint,
// Engine.Run (before dispatch). Every current eval path funnels through Run, so
// the guard is comprehensive today. Any FUTURE code path that reaches a per-kind
// runner (runPointwise/runRubric/runRubricStructured/runCustomSchema/runPairwise)
// WITHOUT going through Run — e.g. a batch or streaming entry point added later —
// MUST call validateInstanceFields itself, or it will reintroduce the silent-drop
// defect. If a second caller appears, prefer lifting this into a shared
// pre-dispatch step rather than duplicating the call.
func validateInstanceFields(expected []string, tmpl registry.MetricTemplate, inst Instance) error {
	// (b) No placeholders but fields supplied: the strongest signal of a
	// mis-authored template. Reported first so the message points at the template,
	// not at an individual "unknown" key.
	if len(expected) == 0 {
		if len(inst.Fields) == 0 {
			return nil
		}
		msg := fmt.Sprintf("eval: template %q references no {{placeholders}} but %d field(s) were supplied (%v); the value(s) will NOT reach the judge", tmpl.ID, len(inst.Fields), fieldKeys(inst))
		// Targeted hint for the common single-brace mistake: the template DOES
		// carry {word} tokens, but Mizan only recognizes double-brace {{word}}
		// (extractVars/varPattern), so extractVars found nothing. Point the author
		// at the exact tokens to fix. Names/placeholders only — never field values.
		if sb := singleBraceVars(tmpl.MetricPromptTemplate); len(sb) > 0 {
			singles := make([]string, len(sb))
			doubles := make([]string, len(sb))
			for i, name := range sb {
				singles[i] = "{" + name + "}"
				doubles[i] = "{{" + name + "}}"
			}
			return fmt.Errorf("%s; found single-brace %s which is NOT a placeholder — placeholders must be double-brace: use %s", msg, strings.Join(singles, ", "), strings.Join(doubles, ", "))
		}
		return fmt.Errorf("%s — add a {{...}} placeholder to the template (e.g. {{response}}) or check the template id", msg)
	}

	// (a) Fields present but not referenced by any placeholder.
	expectedSet := make(map[string]bool, len(expected))
	for _, k := range expected {
		expectedSet[k] = true
	}
	var unknown []string
	for k := range inst.Fields {
		if !expectedSet[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("eval: unknown instance field(s) %v for template %q; it references placeholders %v — a field that matches no placeholder is silently dropped and never reaches the judge (check for a typo, or add the placeholder to the template)", unknown, tmpl.ID, expected)
	}
	return nil
}

// singleBracePattern matches a single-brace {name} token, e.g. {response}. It
// mirrors varPattern (content.go) but with ONE brace on each side; it exists
// only to power a targeted hint, never to substitute values.
var singleBracePattern = regexp.MustCompile(`\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}`)

// singleBraceVars returns the unique single-brace {name} tokens in s, in
// first-seen order, EXCLUDING any that are actually part of a double-brace
// {{name}} token (which is a real placeholder). It is used to detect the common
// authoring mistake of writing {var} instead of {{var}}.
func singleBraceVars(s string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, loc := range singleBracePattern.FindAllStringSubmatchIndex(s, -1) {
		start, end := loc[0], loc[1]
		// Adjacent brace on either side ⇒ this is part of a {{...}} token, not a
		// single-brace mistake; skip it.
		if start > 0 && s[start-1] == '{' {
			continue
		}
		if end < len(s) && s[end] == '}' {
			continue
		}
		name := s[loc[2]:loc[3]]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// fieldKeys returns the instance's field names in sorted order for stable,
// human-readable error messages.
func fieldKeys(inst Instance) []string {
	keys := make([]string, 0, len(inst.Fields))
	for k := range inst.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

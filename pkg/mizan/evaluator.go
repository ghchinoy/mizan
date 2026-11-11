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

package mizan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/evalset"
)

// Generator is the narrow single-method interface satisfied by
// client.Models.GenerateContent, allowing custom transports, rate-limiting
// decorators, or deterministic unit-test fakes via [NewEvaluatorFromGenerator].
type Generator interface {
	GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

// Evaluator executes [Template] and [EvalSet] evaluations in-process. An
// Evaluator is safe for concurrent use by multiple goroutines.
type Evaluator struct {
	engine *eval.Engine
}

type evaluatorConfig struct {
	backendLabel      string
	modelOverride     string
	fallbackModel     string
	maxAttempts       int
	allowedURIHosts   []string
	allowedGCSBuckets []string
	allowLocalFiles   bool
	noticeWriter      io.Writer
}

// EvaluatorOption configures an [Evaluator] at construction time.
type EvaluatorOption func(*evaluatorConfig)

// WithBackendLabel sets the backend provenance label recorded on
// [AppliedJudge.Backend] (for example "enterprise" when using a
// genai.BackendEnterprise *genai.Client, since google.golang.org/genai rewrites
// ClientConfig.Backend to BackendVertexAI in place).
func WithBackendLabel(label string) EvaluatorOption {
	return func(c *evaluatorConfig) { c.backendLabel = label }
}

// WithModelOverride sets an evaluator-wide autorater model (for example
// "gemini-4-argon") that takes precedence over template-pinned models while
// yielding to a per-run [WithModel] option.
func WithModelOverride(model string) EvaluatorOption {
	return func(c *evaluatorConfig) { c.modelOverride = model }
}

// WithFallbackModel sets the fallback autorater model used only when neither
// [WithModel], [WithModelOverride], nor the template specifies a model.
func WithFallbackModel(model string) EvaluatorOption {
	return func(c *evaluatorConfig) { c.fallbackModel = model }
}

// WithMaxAttempts sets the maximum number of outer Mizan GenerateContent
// attempts (1 disables outer retries).
func WithMaxAttempts(n int) EvaluatorOption {
	return func(c *evaluatorConfig) { c.maxAttempts = n }
}

// WithAllowedURIHosts restricts https:// media URIs to the specified hostnames
// (case-insensitive).
func WithAllowedURIHosts(hosts []string) EvaluatorOption {
	cp := slices.Clone(hosts)
	return func(c *evaluatorConfig) { c.allowedURIHosts = cp }
}

// WithAllowedGCSBuckets restricts gs:// media URIs to the specified bucket names.
func WithAllowedGCSBuckets(buckets []string) EvaluatorOption {
	cp := slices.Clone(buckets)
	return func(c *evaluatorConfig) { c.allowedGCSBuckets = cp }
}

// WithLocalFileAccess enables or disables reading local paths constructed via
// [File] (disabled by default in pkg/mizan for server-side safety).
func WithLocalFileAccess(allow bool) EvaluatorOption {
	return func(c *evaluatorConfig) { c.allowLocalFiles = allow }
}

// WithNoticeWriter sets the writer for diagnostic routing notices (defaults to
// io.Discard so pkg/mizan never writes to os.Stderr).
func WithNoticeWriter(w io.Writer) EvaluatorOption {
	return func(c *evaluatorConfig) { c.noticeWriter = w }
}

// NewEvaluator constructs an [Evaluator] backed by a caller-owned
// [*genai.Client] (supporting Vertex AI, Gemini Developer API, and Enterprise
// backends).
func NewEvaluator(client *genai.Client, opts ...EvaluatorOption) (*Evaluator, error) {
	wrapped, err := eval.WrapGenaiClient(client)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}

	cc := client.ClientConfig()
	defaultBackend := "gemini-api"
	if cc.Backend == genai.BackendVertexAI || cc.Backend == genai.BackendEnterprise {
		defaultBackend = "vertex"
	}
	defaultMaxAttempts := 0
	if ro := cc.HTTPOptions.RetryOptions; ro != nil && (ro.Attempts == nil || *ro.Attempts > 1) {
		// Suppress nested outer Mizan retries when the caller's *genai.Client
		// already runs SDK-level retries, unless overridden via WithMaxAttempts.
		defaultMaxAttempts = 1
	}

	cfg := evaluatorConfig{
		backendLabel: defaultBackend,
		maxAttempts:  defaultMaxAttempts,
		noticeWriter: io.Discard,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if err := validateConfiguredModels(cfg); err != nil {
		return nil, err
	}

	return &Evaluator{
		engine: buildEvalEngine(wrapped, cc.Project, cc.Location, cfg),
	}, nil
}

// NewEvaluatorFromGenerator constructs an [Evaluator] over a custom [Generator]
// implementation (useful for custom middleware or unit tests without network I/O).
func NewEvaluatorFromGenerator(gen Generator, opts ...EvaluatorOption) (*Evaluator, error) {
	if gen == nil {
		return nil, fmt.Errorf("mizan: Generator must be non-nil")
	}
	cfg := evaluatorConfig{
		backendLabel: "custom",
		noticeWriter: io.Discard,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if err := validateConfiguredModels(cfg); err != nil {
		return nil, err
	}
	return &Evaluator{
		engine: buildEvalEngine(gen, "", "", cfg),
	}, nil
}

// NewOfflineEvaluator constructs a credential-free [Evaluator] capable of
// running deterministic [KindHeuristic] and [KindComputation] templates locally.
func NewOfflineEvaluator(opts ...EvaluatorOption) *Evaluator {
	cfg := evaluatorConfig{
		backendLabel: "local",
		noticeWriter: io.Discard,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return &Evaluator{
		engine: buildEvalEngine(nil, "", "", cfg),
	}
}

func validateConfiguredModels(cfg evaluatorConfig) error {
	if cfg.modelOverride != "" {
		if err := eval.ValidateModel(cfg.modelOverride); err != nil {
			return fmt.Errorf("mizan: %w", err)
		}
	}
	if cfg.fallbackModel != "" {
		if err := eval.ValidateModel(cfg.fallbackModel); err != nil {
			return fmt.Errorf("mizan: %w", err)
		}
	}
	return nil
}

func buildEvalEngine(g eval.GenaiClient, project, location string, cfg evaluatorConfig) *eval.Engine {
	nw := cfg.noticeWriter
	if nw == nil {
		nw = io.Discard
	}
	engOpts := []eval.Option{
		eval.WithNoticeWriter(nw),
		eval.WithAllowLocalFiles(cfg.allowLocalFiles),
		eval.WithBackend(cmp.Or(cfg.backendLabel, "genai")),
		eval.WithModelOverride(cfg.modelOverride),
		eval.WithDefaultModel(cfg.fallbackModel),
		eval.WithAllowedURIHosts(cfg.allowedURIHosts),
		eval.WithAllowedGCSBuckets(cfg.allowedGCSBuckets),
	}
	if g != nil {
		engOpts = append(engOpts, eval.WithGenaiClient(g))
	}
	if location != "" {
		engOpts = append(engOpts, eval.WithGenaiLocation(location))
	}
	if cfg.maxAttempts > 0 {
		engOpts = append(engOpts, eval.WithRetryMaxAttempts(cfg.maxAttempts))
	}
	return eval.NewEngine(nil, project, location, engOpts...)
}

// RunOption configures a single [Evaluator.Run] or [Evaluator.RunSet] call.
type RunOption struct {
	build func() (eval.RunOption, error)
}

// WithModel overrides the autorater model for a single evaluation call
// (highest precedence in the 5-tier model chain).
func WithModel(model string) RunOption {
	return RunOption{build: func() (eval.RunOption, error) {
		return eval.WithModel(model), nil
	}}
}

// WithChoices overrides the allowed discrete choices for a [KindChoice]
// evaluation without mutating the underlying [Template].
func WithChoices(choices ...string) RunOption {
	cp := slices.Clone(choices)
	return RunOption{build: func() (eval.RunOption, error) {
		return eval.WithChoices(cp), nil
	}}
}

// WithChoicesFromVariations derives the allowed choices for a [KindChoice]
// evaluation from the ordered [Variation] IDs bound in the [Instance],
// appending any optional extra choices (for example "none" or "tie").
func WithChoicesFromVariations(extra ...string) RunOption {
	cp := slices.Clone(extra)
	return RunOption{build: func() (eval.RunOption, error) {
		return eval.WithChoicesFromVariations(cp...), nil
	}}
}

// WithRubricScale sets the inclusive integer Likert bounds [min, max] for a
// structured [KindRubric] or [KindScore] evaluation.
func WithRubricScale(min, max int) RunOption {
	return RunOption{build: func() (eval.RunOption, error) {
		if min < 0 || min >= max {
			return nil, fmt.Errorf("mizan: invalid rubric scale %d..%d (min must be >= 0 and < max)", min, max)
		}
		return eval.WithRubricDetail(min, max), nil
	}}
}

// WithThinkingBudget sets GenerateContentConfig.ThinkingConfig.ThinkingBudget
// on Gemini models (0 disables thinking where supported; negative uses the
// model default).
func WithThinkingBudget(tokens int) RunOption {
	return RunOption{build: func() (eval.RunOption, error) {
		return eval.WithThinkingBudget(tokens), nil
	}}
}

// WithMediaResolution sets GenerateContentConfig.MediaResolution ("low",
// "medium", or "high") to bound multimodal video/image token consumption.
func WithMediaResolution(level string) RunOption {
	return RunOption{build: func() (eval.RunOption, error) {
		return eval.WithMediaResolution(level), nil
	}}
}

// WithMaxOutputTokens caps GenerateContentConfig.MaxOutputTokens when maxTokens > 0.
func WithMaxOutputTokens(maxTokens int32) RunOption {
	return RunOption{build: func() (eval.RunOption, error) {
		return eval.WithMaxOutputTokens(maxTokens), nil
	}}
}

func resolveRunOptions(opts []RunOption) ([]eval.RunOption, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	out := make([]eval.RunOption, 0, len(opts))
	for _, opt := range opts {
		if opt.build == nil {
			continue
		}
		ro, err := opt.build()
		if err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, nil
}

// Run evaluates tmpl against inst using the configured [Evaluator].
func (e *Evaluator) Run(ctx context.Context, tmpl Template, inst Instance, opts ...RunOption) (Result, error) {
	if e == nil || e.engine == nil {
		return Result{}, fmt.Errorf("mizan: Evaluator is nil")
	}
	evalOpts, err := resolveRunOptions(opts)
	if err != nil {
		return Result{}, err
	}
	rawRes, err := e.engine.Run(ctx, tmpl.raw, toEvalInstance(inst), evalOpts...)
	mapped := mapResult(rawRes)
	if err != nil {
		var finErr *eval.FinishError
		if errors.As(err, &finErr) {
			return mapped, &FinishError{
				FinishReason:  finErr.FinishReason,
				BlockReason:   finErr.BlockReason,
				FinishMessage: finErr.FinishMessage,
				ResponseID:    finErr.ResponseID,
				ModelVersion:  finErr.ModelVersion,
				TemplateID:    finErr.TemplateID,
				Kind:          Kind(finErr.Kind),
				Model:         finErr.Model,
			}
		}
		return mapped, err
	}
	return mapped, nil
}

// RunSet executes every member of set in declared order, resolving member
// templates from src, projecting shared inputs per member (honoring set inputs
// and per-member bind aliases), and aggregating scores and token usage.
func (e *Evaluator) RunSet(ctx context.Context, set EvalSet, src TemplateSource, opts SetRunOptions) (EvalSetResult, error) {
	if e == nil || e.engine == nil {
		return EvalSetResult{}, fmt.Errorf("mizan: Evaluator is nil")
	}
	if src == nil {
		return EvalSetResult{}, fmt.Errorf("mizan: TemplateSource must be non-nil")
	}
	evalOpts, err := resolveRunOptions(opts.RunOptions)
	if err != nil {
		return EvalSetResult{}, err
	}
	runner := evalset.New(sourceAdapter{src: src}, e.engine)
	rawRes, err := runner.Run(ctx, set.raw, evalset.RunOptions{
		Inputs:   toEvalInstance(opts.Inputs).Fields,
		RunOpts:  evalOpts,
		FailFast: opts.FailFast,
	})
	if err != nil {
		return EvalSetResult{}, err
	}
	return mapEvalSetResult(rawRes), nil
}

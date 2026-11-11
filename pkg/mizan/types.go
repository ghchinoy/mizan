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
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/evalset"
	"github.com/ghchinoy/mizan/internal/registry"
)

// Kind identifies the evaluation primitive or strategy of a [Template].
type Kind string

const (
	// KindBoul evaluates a boolean proposition with confidence and explanation.
	KindBoul Kind = Kind(registry.KindBoul)
	// KindChoice classifies or selects one option from a discrete choice set.
	KindChoice Kind = Kind(registry.KindChoice)
	// KindScore assigns a numeric score with explanation.
	KindScore Kind = Kind(registry.KindScore)
	// KindPointwise is the legacy Vertex pointwise kind (auto-routed to structured score on genai-only evaluators).
	KindPointwise Kind = Kind(registry.KindPointwise)
	// KindPairwise compares a candidate against a baseline (BASELINE, CANDIDATE, or TIE).
	KindPairwise Kind = Kind(registry.KindPairwise)
	// KindRubric evaluates authored rubric groups with per-criterion scores and rationales.
	KindRubric Kind = Kind(registry.KindRubric)
	// KindCustomSchema evaluates a caller-authored strict JSON response schema.
	KindCustomSchema Kind = Kind(registry.KindCustomSchema)
	// KindHeuristic runs a deterministic, credential-free check (contains, regex, equals, json-valid, json-schema-valid).
	KindHeuristic Kind = Kind(registry.KindHeuristic)
	// KindComputation runs a deterministic local metric (exact_match, bleu, rouge, tool_*, trajectory_*).
	KindComputation Kind = Kind(registry.KindComputation)
	// KindPrebuilt names a Vertex AI Evaluation Service prebuilt judge metric (not supported on genai-only evaluators).
	KindPrebuilt Kind = Kind(registry.KindPrebuilt)
)

// Modality identifies the content type accepted by a template input.
type Modality string

const (
	// ModalityText represents UTF-8 text content.
	ModalityText Modality = Modality(registry.ModalityText)
	// ModalityImage represents still image content (PNG, JPEG, WebP, GIF).
	ModalityImage Modality = Modality(registry.ModalityImage)
	// ModalityAudio represents audio content (WAV, MP3, OGG, FLAC).
	ModalityAudio Modality = Modality(registry.ModalityAudio)
	// ModalityVideo represents video content (MP4, MOV, WebM).
	ModalityVideo Modality = Modality(registry.ModalityVideo)
	// ModalityMusic represents musical audio content.
	ModalityMusic Modality = Modality(registry.ModalityMusic)
)

// InputSpec describes a declared placeholder on a [Template].
type InputSpec struct {
	Name     string   `json:"name"`
	Modality Modality `json:"modality"`
	Required bool     `json:"required"`
}

// RubricScale defines an inclusive integer Likert range [Min, Max].
type RubricScale struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// HeuristicSpec configures a deterministic [KindHeuristic] check.
type HeuristicSpec struct {
	Type            string `json:"type"`
	Target          string `json:"target"`
	Value           string `json:"value,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
	Schema          string `json:"schema,omitempty"`
}

// NativeMetricSpec configures a [KindComputation] or [KindPrebuilt] metric.
type NativeMetricSpec struct {
	Metric           string   `json:"metric"`
	PromptField      string   `json:"prompt_field,omitempty"`
	ResponseField    string   `json:"response_field,omitempty"`
	ReferenceField   string   `json:"reference_field,omitempty"`
	InstructionField string   `json:"instruction_field,omitempty"`
	ContextField     string   `json:"context_field,omitempty"`
	BaselineField    string   `json:"baseline_field,omitempty"`
	RougeType        string   `json:"rouge_type,omitempty"`
	UseStemmer       bool     `json:"use_stemmer,omitempty"`
	ToolName         string   `json:"tool_name,omitempty"`
	PassThreshold    *float64 `json:"pass_threshold,omitempty"`
}

// TemplateSpec defines the fields used to construct a [Template] programmatically
// in Go via [NewTemplate].
type TemplateSpec struct {
	ID                   string
	Name                 string
	Description          string
	Version              string
	Kind                 Kind
	Modalities           []Modality
	Inputs               []InputSpec
	MetricPromptTemplate string
	SystemInstruction    string
	AutoraterModel       string
	Choices              []string
	RubricGroups         map[string][]string
	RubricScale          *RubricScale
	BaselineFieldName    string
	CandidateFieldName   string
	ResponseSchemaJSON   string
	Heuristic            *HeuristicSpec
	Native               *NativeMetricSpec
}

// Template is an immutable, validated Mizan MetricTemplate manifest.
type Template struct {
	raw registry.MetricTemplate
}

// NewTemplate validates spec against Mizan's strict manifest schema and semantic
// rules and returns an immutable [Template] with its canonical SHA-256 ContentHash.
func NewTemplate(spec TemplateSpec) (Template, error) {
	normKind, err := registry.NormalizeKind(string(spec.Kind))
	if err != nil {
		return Template{}, fmt.Errorf("mizan: %w", err)
	}

	rt := registry.MetricTemplate{
		ID:                   spec.ID,
		Name:                 cmp.Or(spec.Name, spec.ID),
		Description:          spec.Description,
		Version:              cmp.Or(spec.Version, "0.1.0"),
		Kind:                 normKind,
		MetricPromptTemplate: spec.MetricPromptTemplate,
		SystemInstruction:    spec.SystemInstruction,
		AutoraterModel:       spec.AutoraterModel,
		Choices:              slices.Clone(spec.Choices),
		BaselineFieldName:    spec.BaselineFieldName,
		CandidateFieldName:   spec.CandidateFieldName,
	}
	for _, m := range spec.Modalities {
		rt.Modalities = append(rt.Modalities, registry.Modality(m))
	}
	for _, in := range spec.Inputs {
		mod := in.Modality
		if mod == "" {
			mod = ModalityText
		}
		rt.Inputs = append(rt.Inputs, registry.InputSpec{
			Name:     in.Name,
			Modality: registry.Modality(mod),
			Required: in.Required,
		})
	}
	if len(rt.Inputs) == 0 && rt.MetricPromptTemplate != "" {
		for _, v := range registry.ExtractPlaceholders(rt.MetricPromptTemplate) {
			rt.Inputs = append(rt.Inputs, registry.InputSpec{
				Name:     v,
				Modality: registry.ModalityText,
				Required: true,
			})
		}
	}
	if len(rt.Modalities) == 0 {
		seenMod := make(map[registry.Modality]bool, len(rt.Inputs))
		for _, in := range rt.Inputs {
			if in.Modality != "" && !seenMod[in.Modality] {
				seenMod[in.Modality] = true
				rt.Modalities = append(rt.Modalities, in.Modality)
			}
		}
		if len(rt.Modalities) == 0 {
			rt.Modalities = []registry.Modality{registry.ModalityText}
		}
	}
	if len(spec.RubricGroups) > 0 {
		rt.RubricGroups = make(map[string][]string, len(spec.RubricGroups))
		for g, crits := range spec.RubricGroups {
			rt.RubricGroups[g] = slices.Clone(crits)
		}
	}
	if spec.RubricScale != nil {
		rt.RubricDetail = &registry.RubricDetail{
			Scale: &registry.RubricScale{Min: spec.RubricScale.Min, Max: spec.RubricScale.Max},
		}
	}
	if spec.ResponseSchemaJSON != "" {
		rt.ResponseSchema = &registry.Schema{JSON: spec.ResponseSchemaJSON}
	}
	if spec.Heuristic != nil {
		rt.Heuristic = &registry.HeuristicSpec{
			Type:            registry.HeuristicType(spec.Heuristic.Type),
			Target:          spec.Heuristic.Target,
			Value:           spec.Heuristic.Value,
			CaseInsensitive: spec.Heuristic.CaseInsensitive,
			Schema:          spec.Heuristic.Schema,
		}
	}
	if spec.Native != nil {
		var passThr *float64
		if spec.Native.PassThreshold != nil {
			v := *spec.Native.PassThreshold
			passThr = &v
		}
		rt.Native = &registry.NativeMetricSpec{
			Metric:           spec.Native.Metric,
			PromptField:      spec.Native.PromptField,
			ResponseField:    spec.Native.ResponseField,
			ReferenceField:   spec.Native.ReferenceField,
			InstructionField: spec.Native.InstructionField,
			ContextField:     spec.Native.ContextField,
			BaselineField:    spec.Native.BaselineField,
			RougeType:        spec.Native.RougeType,
			UseStemmer:       spec.Native.UseStemmer,
			ToolName:         spec.Native.ToolName,
			PassThreshold:    passThr,
		}
	}

	omittedRuntimeChoices := normKind == registry.KindChoice && len(spec.Choices) == 0
	if omittedRuntimeChoices {
		rt.Choices = []string{"__runtime_choice_1__", "__runtime_choice_2__"}
	}

	yamlBytes, err := (registry.YAMLCodec{}).Marshal(&rt)
	if err != nil {
		return Template{}, fmt.Errorf("mizan: marshal template spec: %w", err)
	}
	validated, err := registry.ParseTemplateStrict(yamlBytes)
	if err != nil {
		return Template{}, fmt.Errorf("mizan: %w", err)
	}
	if omittedRuntimeChoices {
		validated.Choices = nil
		validated.ContentHash = registry.ContentHash(validated)
	}
	return Template{raw: *validated}, nil
}

// ID returns the "<namespace>/<slug>" identifier of the template.
func (t Template) ID() string { return t.raw.ID }

// Name returns the human-readable name of the template.
func (t Template) Name() string { return t.raw.Name }

// Version returns the semantic version string of the template.
func (t Template) Version() string { return t.raw.Version }

// ContentHash returns the canonical SHA-256 content hash of the template.
func (t Template) ContentHash() string { return t.raw.ContentHash }

// Kind returns the canonical evaluation [Kind] of the template.
func (t Template) Kind() Kind { return Kind(t.raw.Kind) }

// AutoraterModel returns the template-declared autorater model ID (empty when unset).
func (t Template) AutoraterModel() string { return t.raw.AutoraterModel }

// Choices returns a copy of the static choices declared on a [KindChoice] template.
func (t Template) Choices() []string { return slices.Clone(t.raw.Choices) }

// Inputs returns a copy of the declared input placeholders on the template.
func (t Template) Inputs() []InputSpec {
	if len(t.raw.Inputs) == 0 {
		return nil
	}
	out := make([]InputSpec, len(t.raw.Inputs))
	for i, in := range t.raw.Inputs {
		out[i] = InputSpec{
			Name:     in.Name,
			Modality: Modality(in.Modality),
			Required: in.Required,
		}
	}
	return out
}

// SetMember describes one ordered member metric inside an [EvalSet].
type SetMember struct {
	MetricID string            `json:"metric_id"`
	Weight   float64           `json:"weight"`
	Required bool              `json:"required"`
	Bind     map[string]string `json:"bind,omitempty"`
}

// SetAggregation describes the aggregation strategy and threshold of an [EvalSet].
type SetAggregation struct {
	Method    string   `json:"method"`
	Threshold *float64 `json:"threshold,omitempty"`
	Gate      bool     `json:"gate"`
}

// EvalSet is an immutable, validated Mizan EvalSet suite manifest.
type EvalSet struct {
	raw evalset.Set
}

// ID returns the "<namespace>/<slug>" identifier of the suite.
func (s EvalSet) ID() string { return s.raw.ID }

// Name returns the human-readable name of the suite.
func (s EvalSet) Name() string { return s.raw.Name }

// Version returns the semantic version string of the suite.
func (s EvalSet) Version() string { return s.raw.Version }

// AssetClass returns the optional asset class label declared on the suite.
func (s EvalSet) AssetClass() string { return s.raw.AssetClass }

// Members returns a copy of the ordered member definitions in the suite.
func (s EvalSet) Members() []SetMember {
	if len(s.raw.Members) == 0 {
		return nil
	}
	out := make([]SetMember, len(s.raw.Members))
	for i, m := range s.raw.Members {
		out[i] = SetMember{
			MetricID: m.MetricID,
			Weight:   m.Weight,
			Required: m.Required,
			Bind:     maps.Clone(m.Bind),
		}
	}
	return out
}

// Aggregation returns the suite's scalar aggregation configuration.
func (s EvalSet) Aggregation() SetAggregation {
	var thr *float64
	if s.raw.Aggregation.Threshold != nil {
		v := *s.raw.Aggregation.Threshold
		thr = &v
	}
	return SetAggregation{
		Method:    string(s.raw.Aggregation.Method),
		Threshold: thr,
		Gate:      s.raw.Aggregation.Gate,
	}
}

// Instance maps template placeholder names to [Asset] values for a single evaluation.
type Instance map[string]Asset

// SetRunOptions configures a single [Evaluator.RunSet] suite execution.
type SetRunOptions struct {
	Inputs     Instance
	FailFast   bool
	RunOptions []RunOption
}

// CriterionScore holds one per-criterion score and rationale returned by a
// structured [KindRubric] evaluation.
type CriterionScore struct {
	Group     string `json:"group"`
	Criterion string `json:"criterion"`
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

// TokenUsage reports prompt, candidate, thinking, and total token counts from a
// GenerateContent call.
type TokenUsage struct {
	PromptTokens     int32 `json:"prompt_tokens"`
	CandidatesTokens int32 `json:"candidates_tokens"`
	ThoughtsTokens   int32 `json:"thoughts_tokens"`
	TotalTokens      int32 `json:"total_tokens"`
}

// Stats captures wall-clock latency, retry telemetry, response identifiers, and
// token usage for an evaluation run.
type Stats struct {
	Duration     time.Duration `json:"duration_ns"`
	TokenUsage   *TokenUsage   `json:"token_usage,omitempty"`
	Attempts     int           `json:"attempts,omitempty"`
	RetryBackoff time.Duration `json:"retry_backoff_ns,omitempty"`
	ResponseID   string        `json:"response_id,omitempty"`
	ModelVersion string        `json:"model_version,omitempty"`
	FinishReason string        `json:"finish_reason,omitempty"`
	CachedTokens int32         `json:"cached_tokens,omitempty"`
}

// AppliedJudge records the resolved autorater provenance applied to a successful
// evaluation run.
type AppliedJudge struct {
	Model            string   `json:"model"`
	SamplingCount    int32    `json:"sampling_count,omitempty"`
	FlipEnabled      bool     `json:"flip_enabled,omitempty"`
	EffectiveHost    string   `json:"effective_host,omitempty"`
	Location         string   `json:"location,omitempty"`
	ModelSource      string   `json:"model_source,omitempty"`
	Backend          string   `json:"backend,omitempty"`
	ModelVersion     string   `json:"model_version,omitempty"`
	EffectiveChoices []string `json:"effective_choices,omitempty"`
}

// Result is the JSON-serializable outcome of a single [Evaluator.Run] call.
type Result struct {
	Score           *float32         `json:"score,omitempty"`
	Passed          *bool            `json:"passed,omitempty"`
	Confidence      *float32         `json:"confidence,omitempty"`
	ChoiceSelection string           `json:"choice_selection,omitempty"`
	PairwiseChoice  string           `json:"pairwise_choice,omitempty"`
	Explanation     string           `json:"explanation,omitempty"`
	RawOutput       []string         `json:"raw_output,omitempty"`
	CustomOutput    map[string]any   `json:"custom_output,omitempty"`
	RubricDetail    bool             `json:"rubric_detail,omitempty"`
	Criteria        []CriterionScore `json:"criteria,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Stats           Stats            `json:"stats"`
	Applied         *AppliedJudge    `json:"applied,omitempty"`
}

// FinishError reports that GenerateContent returned without a usable verdict due
// to prompt blocking, safety/recitation/max-tokens termination, or zero candidates.
type FinishError struct {
	FinishReason  string `json:"finish_reason,omitempty"`
	BlockReason   string `json:"block_reason,omitempty"`
	FinishMessage string `json:"finish_message,omitempty"`
	ResponseID    string `json:"response_id,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`
	TemplateID    string `json:"template_id"`
	Kind          Kind   `json:"kind"`
	Model         string `json:"model"`
}

// Error implements the error interface.
func (e *FinishError) Error() string {
	if e.BlockReason != "" {
		if e.FinishMessage != "" {
			return fmt.Sprintf("mizan: %s (%s) model %q blocked prompt (block_reason=%s): %s", e.Kind, e.TemplateID, e.Model, e.BlockReason, e.FinishMessage)
		}
		return fmt.Sprintf("mizan: %s (%s) model %q blocked prompt (block_reason=%s)", e.Kind, e.TemplateID, e.Model, e.BlockReason)
	}
	if e.FinishReason != "" {
		if e.FinishMessage != "" {
			return fmt.Sprintf("mizan: %s (%s) model %q finished with %s: %s", e.Kind, e.TemplateID, e.Model, e.FinishReason, e.FinishMessage)
		}
		return fmt.Sprintf("mizan: %s (%s) model %q finished with %s", e.Kind, e.TemplateID, e.Model, e.FinishReason)
	}
	return fmt.Sprintf("mizan: %s (%s) model %q returned no candidates", e.Kind, e.TemplateID, e.Model)
}

// MemberStatus reports the execution status of a single [EvalSet] member.
type MemberStatus string

const (
	// MemberStatusOK indicates the member resolved and completed without error.
	MemberStatusOK MemberStatus = MemberStatus(evalset.OK)
	// MemberStatusErrored indicates the member failed during evaluation.
	MemberStatusErrored MemberStatus = MemberStatus(evalset.Errored)
	// MemberStatusMissing indicates the member's TemplateID was not found in the TemplateSource.
	MemberStatusMissing MemberStatus = MemberStatus(evalset.Missing)
	// MemberStatusSkipped indicates the member was skipped after an earlier FailFast failure.
	MemberStatusSkipped MemberStatus = MemberStatus(evalset.Skipped)
)

// SetVerdict is the overall pass/fail verdict of an [EvalSet] execution.
type SetVerdict string

const (
	// VerdictPassed indicates all required members succeeded and any score threshold was met.
	VerdictPassed SetVerdict = SetVerdict(evalset.Passed)
	// VerdictFailed indicates a required member failed or the aggregate fell below threshold.
	VerdictFailed SetVerdict = SetVerdict(evalset.Failed)
)

// Aggregate summarizes the scalar aggregation over scored [EvalSet] members.
type Aggregate struct {
	Method    string   `json:"method"`
	Score     *float32 `json:"score,omitempty"`
	Threshold *float64 `json:"threshold,omitempty"`
	Passed    *bool    `json:"passed,omitempty"`
	Scored    int      `json:"scored"`
	Failed    int      `json:"failed"`
}

// MemberResult holds the outcome of one member inside an [EvalSetResult].
type MemberResult struct {
	MetricID string       `json:"metric_id"`
	Status   MemberStatus `json:"status"`
	Weight   float64      `json:"weight"`
	Required bool         `json:"required"`
	Score    *float32     `json:"score,omitempty"`
	Result   Result       `json:"result"`
	Error    string       `json:"error,omitempty"`
}

// EvalSetResult is the JSON-serializable outcome of an [Evaluator.RunSet] call.
type EvalSetResult struct {
	SetID        string         `json:"set_id"`
	SetName      string         `json:"set_name,omitempty"`
	Version      string         `json:"version,omitempty"`
	AssetClass   string         `json:"asset_class,omitempty"`
	Members      []MemberResult `json:"members"`
	Aggregate    Aggregate      `json:"aggregate"`
	Verdict      SetVerdict     `json:"verdict"`
	Gate         bool           `json:"gate"`
	StartedAt    time.Time      `json:"started_at"`
	Duration     time.Duration  `json:"duration_ns"`
	TokenUsage   *TokenUsage    `json:"token_usage,omitempty"`
	CachedTokens int32          `json:"cached_tokens,omitempty"`
}

// DecodeCustom decodes res.CustomOutput into a typed Go struct T.
func DecodeCustom[T any](res Result) (T, error) {
	var zero T
	if res.CustomOutput == nil {
		return zero, fmt.Errorf("mizan: result has no custom_output to decode")
	}
	b, err := json.Marshal(res.CustomOutput)
	if err != nil {
		return zero, fmt.Errorf("mizan: marshal custom_output: %w", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return zero, fmt.Errorf("mizan: decode custom_output into %T: %w", out, err)
	}
	return out, nil
}

func mapResult(in eval.Result) Result {
	out := Result{
		Score:           cloneFloat32Ptr(in.Score),
		Passed:          cloneBoolPtr(in.Passed),
		Confidence:      cloneFloat32Ptr(in.Confidence),
		ChoiceSelection: in.ChoiceSelection,
		PairwiseChoice:  in.PairwiseChoice,
		Explanation:     in.Explanation,
		RawOutput:       slices.Clone(in.RawOutput),
		CustomOutput:    maps.Clone(in.CustomOutput),
		RubricDetail:    in.RubricDetail,
		Warnings:        slices.Clone(in.Warnings),
		Stats: Stats{
			Duration:     in.Stats.Duration,
			Attempts:     in.Stats.Attempts,
			RetryBackoff: in.Stats.RetryBackoff,
			ResponseID:   in.Stats.ResponseID,
			ModelVersion: in.Stats.ModelVersion,
			FinishReason: in.Stats.FinishReason,
			CachedTokens: in.Stats.CachedTokens,
		},
	}
	if in.Stats.TokenUsage != nil {
		out.Stats.TokenUsage = &TokenUsage{
			PromptTokens:     in.Stats.TokenUsage.PromptTokens,
			CandidatesTokens: in.Stats.TokenUsage.CandidatesTokens,
			ThoughtsTokens:   in.Stats.TokenUsage.ThoughtsTokens,
			TotalTokens:      in.Stats.TokenUsage.TotalTokens,
		}
	}
	if in.Applied != nil {
		out.Applied = &AppliedJudge{
			Model:            in.Applied.Model,
			SamplingCount:    in.Applied.SamplingCount,
			FlipEnabled:      in.Applied.FlipEnabled,
			EffectiveHost:    in.Applied.EffectiveHost,
			Location:         in.Applied.Location,
			ModelSource:      in.Applied.ModelSource,
			Backend:          in.Applied.Backend,
			ModelVersion:     in.Applied.ModelVersion,
			EffectiveChoices: slices.Clone(in.Applied.EffectiveChoices),
		}
	}
	if in.RubricDetail {
		out.Criteria = extractCriteria(in.CustomOutput)
	}
	return out
}

func extractCriteria(custom map[string]any) []CriterionScore {
	rawList, ok := custom["per_criterion"].([]any)
	if !ok || len(rawList) == 0 {
		return nil
	}
	var out []CriterionScore
	for _, entry := range rawList {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		cs := CriterionScore{}
		if g, ok := m["group"].(string); ok {
			cs.Group = g
		}
		if c, ok := m["criterion"].(string); ok {
			cs.Criterion = c
		}
		if r, ok := m["rationale"].(string); ok {
			cs.Rationale = r
		}
		switch s := m["score"].(type) {
		case int:
			cs.Score = s
		case float64:
			cs.Score = int(s)
		}
		out = append(out, cs)
	}
	return out
}

func mapEvalSetResult(in evalset.EvalSetResult) EvalSetResult {
	out := EvalSetResult{
		SetID:      in.SetID,
		SetName:    in.SetName,
		Version:    in.Version,
		AssetClass: in.AssetClass,
		Aggregate: Aggregate{
			Method:    string(in.Aggregate.Method),
			Score:     cloneFloat32Ptr(in.Aggregate.Score),
			Threshold: cloneFloat64Ptr(in.Aggregate.Threshold),
			Passed:    cloneBoolPtr(in.Aggregate.Passed),
			Scored:    in.Aggregate.Scored,
			Failed:    in.Aggregate.Failed,
		},
		Verdict:   SetVerdict(in.Verdict),
		Gate:      in.Gate,
		StartedAt: in.StartedAt,
		Duration:  in.Duration,
	}
	var sum TokenUsage
	hasTokenUsage := false
	for _, m := range in.Members {
		mapped := mapResult(m.Result)
		if tu := mapped.Stats.TokenUsage; tu != nil {
			hasTokenUsage = true
			sum.PromptTokens += tu.PromptTokens
			sum.CandidatesTokens += tu.CandidatesTokens
			sum.ThoughtsTokens += tu.ThoughtsTokens
			sum.TotalTokens += tu.TotalTokens
		}
		out.CachedTokens += mapped.Stats.CachedTokens
		out.Members = append(out.Members, MemberResult{
			MetricID: m.MetricID,
			Status:   MemberStatus(m.Status),
			Weight:   m.Weight,
			Required: m.Required,
			Score:    cloneFloat32Ptr(m.Score),
			Result:   mapped,
			Error:    m.Error,
		})
	}
	if hasTokenUsage {
		out.TokenUsage = &sum
	}
	return out
}

func cloneFloat32Ptr(p *float32) *float32 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneFloat64Ptr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

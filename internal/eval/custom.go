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

package eval

// custom.go holds the genai custom_schema fallback path (KindCustomSchema): a
// direct genai.GenerateContent call with a strict ResponseSchema and
// exponential backoff. Unlike native EvaluateInstances, the genai path DOES
// accept inline bytes (spike-core / spike-custom) — its content converter
// (content.go toGenaiInlinePart) is distinct from the native one.
//
// The genai client targets location=global and is a SEPARATE seam from the
// native regional EvaluationClient (spike-core).

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

const maxVariationDescBytes = 256

// FinishError reports that GenerateContent returned without a usable candidate
// verdict due to prompt blocking, safety/recitation/max-tokens termination, or
// an empty candidate list.
type FinishError struct {
	FinishReason  string
	BlockReason   string
	FinishMessage string
	ResponseID    string
	ModelVersion  string
	TemplateID    string
	Kind          registry.MetricKind
	Model         string
}

func (e *FinishError) Error() string {
	if e.BlockReason != "" {
		if e.FinishMessage != "" {
			return fmt.Sprintf("eval: %s (%s) model %q blocked prompt (block_reason=%s): %s", e.Kind, e.TemplateID, e.Model, e.BlockReason, e.FinishMessage)
		}
		return fmt.Sprintf("eval: %s (%s) model %q blocked prompt (block_reason=%s)", e.Kind, e.TemplateID, e.Model, e.BlockReason)
	}
	if e.FinishReason != "" {
		if e.FinishMessage != "" {
			return fmt.Sprintf("eval: %s (%s) model %q finished with %s: %s", e.Kind, e.TemplateID, e.Model, e.FinishReason, e.FinishMessage)
		}
		return fmt.Sprintf("eval: %s (%s) model %q finished with %s", e.Kind, e.TemplateID, e.Model, e.FinishReason)
	}
	return fmt.Sprintf("eval: %s (%s) model %q returned no candidates", e.Kind, e.TemplateID, e.Model)
}

func parseMediaResolution(res string) (genai.MediaResolution, error) {
	switch strings.ToLower(strings.TrimSpace(res)) {
	case "":
		return "", nil
	case "low":
		return genai.MediaResolutionLow, nil
	case "medium":
		return genai.MediaResolutionMedium, nil
	case "high":
		return genai.MediaResolutionHigh, nil
	default:
		return "", fmt.Errorf("eval: invalid media resolution %q (want \"low\", \"medium\", or \"high\")", res)
	}
}

func hasControlChars(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b <= 0x1f || b == 0x7f {
			return true
		}
	}
	return false
}

// retryPolicy controls the exponential-backoff retry for genai calls. Only
// RESOURCE_EXHAUSTED (HTTP 429) is retried; every other error fails fast
// (spike-custom).
type retryPolicy struct {
	maxAttempts int
	baseDelay   time.Duration
	maxDelay    time.Duration
}

func defaultRetryPolicy() retryPolicy {
	return retryPolicy{maxAttempts: 5, baseDelay: time.Second, maxDelay: 30 * time.Second}
}

// genaiModels adapts a concrete *genai.Client's Models service to the narrow
// GenaiClient seam.
type genaiModels struct{ c *genai.Client }

func (g genaiModels) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return g.c.Models.GenerateContent(ctx, model, contents, config)
}

// WrapGenaiClient adapts a caller-constructed *genai.Client (Vertex AI, Gemini
// Developer API, or Enterprise backend) to Mizan's GenaiClient seam.
func WrapGenaiClient(c *genai.Client) (GenaiClient, error) {
	if c == nil || c.Models == nil {
		return nil, fmt.Errorf("eval: genai.Client and its Models service must be non-nil")
	}
	return genaiModels{c: c}, nil
}

// NewGenaiClient builds a live GenaiClient on the Vertex AI backend at
// location=global (spike-custom). ADC is used for auth. The returned value
// satisfies the GenaiClient seam and is passed via eval.WithGenaiClient from the
// composition root (internal/wire) — never from cmd/*.
func NewGenaiClient(ctx context.Context, projectID, location string) (GenaiClient, error) {
	if location == "" {
		location = "global"
	}
	c, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  projectID,
		Location: location,
	})
	if err != nil {
		return nil, fmt.Errorf("eval: build genai client: %w", err)
	}
	return genaiModels{c: c}, nil
}

// runCustomSchema materializes the genai custom_schema path: it renders the
// prompt (text placeholders substituted inline, non-text assets placed as
// Media-Preamble-First Parts), maps the template's ResponseSchema into a
// *genai.Schema, calls GenerateContent with strict JSON output and exponential
// backoff, and parses the JSON response into Result.CustomOutput (with the raw
// text in RawOutput).
func (e *Engine) runCustomSchema(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string, rc runConfig) (Result, error) {
	// Guard the genai client FIRST so the error precedence is unchanged from
	// before the runGenaiStructured extraction (a missing client fails with the
	// client error, ahead of prompt/schema validation).
	if e.genai == nil {
		return Result{}, fmt.Errorf("eval: no genai client configured for custom_schema (wire WithGenaiClient)")
	}
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	if tmpl.ResponseSchema == nil {
		return Result{}, fmt.Errorf("eval: custom_schema template %q has no response schema", tmpl.ID)
	}

	schema, err := toGenaiSchema(tmpl.ResponseSchema)
	if err != nil {
		return Result{}, err
	}

	return e.runGenaiStructured(ctx, tmpl, inst, tmpl.MetricPromptTemplate, "", schema, model, rc)
}

// runGenaiStructured is the shared genai structured-output core: it renders
// promptTemplate (substituting text placeholders inline while placing labeled
// media parts BEFORE the final rendered prompt + directiveSuffix), calls
// GenerateContent with strict JSON output and exponential backoff, checks
// FinishError before JSON decoding, and parses the JSON response into
// Result.CustomOutput.
func (e *Engine) runGenaiStructured(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, promptTemplate, directiveSuffix string, schema *genai.Schema, model string, rc runConfig) (Result, error) {
	if e.genai == nil {
		return Result{}, fmt.Errorf("eval: no genai client configured for structured output (wire WithGenaiClient)")
	}
	mediaRes, err := parseMediaResolution(rc.mediaResolution)
	if err != nil {
		return Result{}, err
	}

	parts, err := e.buildGenaiParts(promptTemplate, directiveSuffix, inst)
	if err != nil {
		return Result{}, err
	}
	contents := []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}

	cfg := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
		Temperature:      genai.Ptr[float32](0.0),
	}
	if mediaRes != "" {
		cfg.MediaResolution = mediaRes
	}
	if rc.maxOutputTokens > 0 {
		cfg.MaxOutputTokens = rc.maxOutputTokens
	}
	applyThinking(ctx, cfg)
	// Note: SystemInstruction is literal judge persona text and is never
	// placeholder-expanded.
	if tmpl.SystemInstruction != "" {
		cfg.SystemInstruction = genai.NewContentFromText(tmpl.SystemInstruction, genai.RoleUser)
	}

	modelID := genaiModelID(model)
	resp, attempts, retryBackoff, err := e.generateWithBackoff(ctx, tmpl, modelID, contents, cfg)
	if err != nil {
		return Result{Stats: Stats{Attempts: attempts, RetryBackoff: retryBackoff}}, err
	}

	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		return Result{}, &FinishError{
			BlockReason:   string(resp.PromptFeedback.BlockReason),
			FinishMessage: resp.PromptFeedback.BlockReasonMessage,
			ResponseID:    resp.ResponseID,
			ModelVersion:  resp.ModelVersion,
			TemplateID:    tmpl.ID,
			Kind:          tmpl.Kind,
			Model:         modelID,
		}
	}
	if len(resp.Candidates) == 0 {
		return Result{}, &FinishError{
			ResponseID:   resp.ResponseID,
			ModelVersion: resp.ModelVersion,
			TemplateID:   tmpl.ID,
			Kind:         tmpl.Kind,
			Model:        modelID,
		}
	}
	cand := resp.Candidates[0]
	if cand.FinishReason != "" && cand.FinishReason != genai.FinishReasonUnspecified && cand.FinishReason != genai.FinishReasonStop {
		return Result{}, &FinishError{
			FinishReason:  string(cand.FinishReason),
			FinishMessage: cand.FinishMessage,
			ResponseID:    resp.ResponseID,
			ModelVersion:  resp.ModelVersion,
			TemplateID:    tmpl.ID,
			Kind:          tmpl.Kind,
			Model:         modelID,
		}
	}

	raw := resp.Text()
	out, err := parseCustomOutput(raw)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		CustomOutput: out,
		RawOutput:    []string{raw},
		Stats: Stats{
			Attempts:     attempts,
			RetryBackoff: retryBackoff,
			ResponseID:   resp.ResponseID,
			ModelVersion: resp.ModelVersion,
			FinishReason: string(cand.FinishReason),
		},
	}
	res.Warnings = append(res.Warnings, genaiPathAutoraterWarnings(tmpl)...)
	if um := resp.UsageMetadata; um != nil {
		res.Stats.CachedTokens = um.CachedContentTokenCount
		res.Stats.TokenUsage = &TokenUsage{
			PromptTokens:     um.PromptTokenCount,
			CandidatesTokens: um.CandidatesTokenCount,
			ThoughtsTokens:   um.ThoughtsTokenCount,
			TotalTokens:      um.TotalTokenCount,
		}
	}
	return res, nil
}

// genaiPathAutoraterWarnings returns the non-fatal warnings for AutoraterConfig
// fields a template declares that the genai/global structured path cannot honor
// (RFC-0001 §5.4 decision item 3). It is called from runGenaiStructured, the
// single chokepoint shared by the custom_schema and rubric-detail paths, so both
// surface the warning identically.
//
//   - SamplingCount > 1: the genai GenerateContent API has no sampling-count
//     concept (it is an EvaluateInstances feature); a value of 0 or 1 means a
//     single sample and is a no-op, so only > 1 is worth warning about.
//   - FlipEnabled: likewise has no genai equivalent (it is a native pairwise
//     feature).
//
// The native path honors both and never reaches this function, so its behavior is
// unchanged. Warnings are informational only — scoring and run success are not
// affected.
func genaiPathAutoraterWarnings(tmpl registry.MetricTemplate) []string {
	var warnings []string
	if tmpl.SamplingCount > 1 {
		warnings = append(warnings, fmt.Sprintf(
			"mizan: samplingCount=%d is ignored on the genai/global structured path (it has no sampling-count concept); samplingCount applies only to the native evaluation path",
			tmpl.SamplingCount))
	}
	if tmpl.FlipEnabled {
		warnings = append(warnings,
			"mizan: flipEnabled is ignored on the genai/global structured path (it has no flip concept); flipEnabled applies only to the native pairwise evaluation path")
	}
	return warnings
}

// generateWithBackoff calls GenerateContent, retrying on RESOURCE_EXHAUSTED and
// transient 5xx/transport errors with full-jitter exponential backoff, honoring
// ctx during sleeps.
func (e *Engine) generateWithBackoff(ctx context.Context, tmpl registry.MetricTemplate, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, int, time.Duration, error) {
	pol := e.retry
	if pol.maxAttempts <= 0 {
		pol = defaultRetryPolicy()
	}

	kindLabel := cmp.Or(string(tmpl.Kind), "structured")
	var lastErr error
	var totalBackoff time.Duration
	delay := pol.baseDelay
	for attempt := 1; attempt <= pol.maxAttempts; attempt++ {
		resp, err := e.genai.GenerateContent(ctx, model, contents, cfg)
		if err == nil {
			return resp, attempt, totalBackoff, nil
		}
		if !isResourceExhausted(err) && !isTransientGenaiError(err) {
			return nil, attempt, totalBackoff, fmt.Errorf("eval: %s (%s) generate (non-retryable): %w", kindLabel, tmpl.ID, err)
		}
		lastErr = err
		if attempt == pol.maxAttempts {
			break
		}
		// Full jitter over [0.5*delay, delay]. Retry-backoff jitter is not a
		// security context, so a non-cryptographic RNG is the correct choice.
		sleep := time.Duration(float64(delay) * (0.5 + 0.5*rand.Float64())) //nolint:gosec // G404: jitter for retry backoff, not security-sensitive
		select {
		case <-time.After(sleep):
			totalBackoff += sleep
		case <-ctx.Done():
			return nil, attempt, totalBackoff, ctx.Err()
		}
		delay *= 2
		if delay > pol.maxDelay {
			delay = pol.maxDelay
		}
	}
	return nil, pol.maxAttempts, totalBackoff, fmt.Errorf("eval: %s (%s) generate: exhausted %d attempts: %w", kindLabel, tmpl.ID, pol.maxAttempts, lastErr)
}

// isResourceExhausted reports whether err is a genai RESOURCE_EXHAUSTED / 429.
// genai.APIError is a VALUE type, so errors.As must target that value type, not
// a pointer (spike-custom).
func isResourceExhausted(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == 429 || apiErr.Status == "RESOURCE_EXHAUSTED"
	}
	return false
}

// isTransientGenaiError reports server/transport failures worth retrying:
// HTTP 500/502/503/504 (INTERNAL / UNAVAILABLE / DEADLINE_EXCEEDED) and
// connection-level failures.
func isTransientGenaiError(err error) bool {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code == 500 || apiErr.Code == 502 || apiErr.Code == 503 || apiErr.Code == 504 ||
			apiErr.Status == "UNAVAILABLE" || apiErr.Status == "INTERNAL" || apiErr.Status == "DEADLINE_EXCEEDED"
	}
	msg := err.Error()
	for _, s := range []string{"connection reset by peer", "unexpected EOF", "broken pipe", "http2: server sent GOAWAY", "connection refused"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func resolvedModality(mod registry.Modality, mime string) string {
	return cmp.Or(string(mod), string(asset.ModalityForMIME(mime)), "media")
}

func formatMediaHeader(mod, label, desc string) string {
	if desc != "" {
		return fmt.Sprintf("[%s %q — %s]:", mod, label, desc)
	}
	return fmt.Sprintf("[%s %q]:", mod, label)
}

func validateVariationDesc(desc, name string) error {
	if len(desc) > maxVariationDescBytes {
		return fmt.Errorf("eval: variation description in placeholder %q is %d bytes, exceeds %d-byte limit", name, len(desc), maxVariationDescBytes)
	}
	if hasControlChars(desc) {
		return fmt.Errorf("eval: variation description in placeholder %q contains ASCII control characters", name)
	}
	return nil
}

// buildGenaiParts constructs the Media-Preamble-First []*genai.Part slice for
// structured genai calls (RFC-0002 Option A):
//
//   - Zero media placeholders -> single [TextPart(finalText)] (byte-identical
//     with historical text-only behavior).
//   - >= 1 media placeholders -> [LabelPart_1, MediaPart_1, ..., LabelPart_N,
//     MediaPart_N, TextPart(finalText)], deduplicating repeated {{var}}
//     placeholders so media bytes/URIs are attached once.
func (e *Engine) buildGenaiParts(promptTemplate, directiveSuffix string, inst Instance) ([]*genai.Part, error) {
	vars := extractVars(promptTemplate)
	var missing []string
	for _, v := range vars {
		if _, ok := inst.Fields[v]; !ok {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("eval: instance is missing values for template variables %v", missing)
	}

	var (
		preambleParts    []*genai.Part
		totalInlineBytes int64
		replaceErr       error
		emittedMarkers   = make(map[string]string, len(vars))
	)
	rendered := varPattern.ReplaceAllStringFunc(promptTemplate, func(match string) string {
		if replaceErr != nil {
			return match
		}
		name := varPattern.FindStringSubmatch(match)[1]
		if marker, ok := emittedMarkers[name]; ok {
			return marker
		}
		ref := inst.Fields[name]
		if isTextRef(ref) {
			return ref.Text
		}

		if len(ref.Items) > 0 {
			if len(ref.Items) > MaxVariations {
				replaceErr = fmt.Errorf("eval: placeholder %q has %d variations, exceeds MaxVariations (%d)", name, len(ref.Items), MaxVariations)
				return match
			}
			seenLabels := make(map[string]bool, len(ref.Items))
			var quotedLabels []string
			var groupMod string
			for idx, item := range ref.Items {
				if len(item.Items) > 0 {
					replaceErr = fmt.Errorf("eval: nested variation lists in placeholder %q (index %d) are not supported", name, idx)
					return match
				}
				lbl := item.Label
				if strings.TrimSpace(lbl) == "" {
					replaceErr = fmt.Errorf("eval: variation at index %d in placeholder %q has an empty ID", idx, name)
					return match
				}
				if hasControlChars(lbl) {
					replaceErr = fmt.Errorf("eval: variation ID %q in placeholder %q contains ASCII control characters", lbl, name)
					return match
				}
				if seenLabels[lbl] {
					replaceErr = fmt.Errorf("eval: duplicate variation ID %q in placeholder %q", lbl, name)
					return match
				}
				seenLabels[lbl] = true
				if err := validateVariationDesc(item.Description, name); err != nil {
					replaceErr = err
					return match
				}
				part, mime, inlineN, err := toGenaiPart(item, e.allowLocalFiles, e.allowedURIHosts, e.allowedGCSBuckets)
				if err != nil {
					replaceErr = err
					return match
				}
				totalInlineBytes += inlineN
				if totalInlineBytes > maxInlineBytes {
					replaceErr = fmt.Errorf("eval: total inline media payload (%d bytes) exceeds %d-byte limit; pass gs:// or https:// via URI or pre-upload via FromGenaiPart", totalInlineBytes, maxInlineBytes)
					return match
				}
				mod := resolvedModality(cmp.Or(item.Modality, ref.Modality), mime)
				if groupMod == "" {
					groupMod = mod
				} else if groupMod != mod {
					groupMod = "media"
				}
				preambleParts = append(preambleParts,
					genai.NewPartFromText(formatMediaHeader(mod, lbl, item.Description)),
					part,
				)
				quotedLabels = append(quotedLabels, fmt.Sprintf("%q", lbl))
			}
			marker := fmt.Sprintf("[attached %s variations: %s]", cmp.Or(groupMod, "media"), strings.Join(quotedLabels, ", "))
			emittedMarkers[name] = marker
			return marker
		}

		if err := validateVariationDesc(ref.Description, name); err != nil {
			replaceErr = err
			return match
		}
		lbl := cmp.Or(ref.Label, name)
		if hasControlChars(lbl) {
			replaceErr = fmt.Errorf("eval: media label %q in placeholder %q contains ASCII control characters", lbl, name)
			return match
		}
		part, mime, inlineN, err := toGenaiPart(ref, e.allowLocalFiles, e.allowedURIHosts, e.allowedGCSBuckets)
		if err != nil {
			replaceErr = err
			return match
		}
		totalInlineBytes += inlineN
		if totalInlineBytes > maxInlineBytes {
			replaceErr = fmt.Errorf("eval: total inline media payload (%d bytes) exceeds %d-byte limit; pass gs:// or https:// via URI or pre-upload via FromGenaiPart", totalInlineBytes, maxInlineBytes)
			return match
		}
		mod := resolvedModality(ref.Modality, mime)
		preambleParts = append(preambleParts,
			genai.NewPartFromText(formatMediaHeader(mod, lbl, ref.Description)),
			part,
		)
		marker := fmt.Sprintf("[attached %s: %q]", mod, name)
		emittedMarkers[name] = marker
		return marker
	})
	if replaceErr != nil {
		return nil, replaceErr
	}

	finalText := rendered
	if directiveSuffix != "" {
		finalText = rendered + "\n\n" + directiveSuffix
	}
	if len(preambleParts) == 0 {
		return []*genai.Part{genai.NewPartFromText(finalText)}, nil
	}
	return append(preambleParts, genai.NewPartFromText(finalText)), nil
}

// renderGenaiPrompt substitutes text placeholders inline and collects non-text
// assets as separate Parts (deduplicating repeated {{var}} placeholders and
// inferring modality from resolved MIME when Modality is unset).
func renderGenaiPrompt(template string, inst Instance) (string, []*genai.Part, error) {
	vars := extractVars(template)
	var missing []string
	for _, v := range vars {
		if _, ok := inst.Fields[v]; !ok {
			missing = append(missing, v)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", nil, fmt.Errorf("eval: instance is missing values for template variables %v", missing)
	}

	var mediaParts []*genai.Part
	var replaceErr error
	emitted := make(map[string]string, len(vars))
	rendered := varPattern.ReplaceAllStringFunc(template, func(match string) string {
		if replaceErr != nil {
			return match
		}
		name := varPattern.FindStringSubmatch(match)[1]
		if marker, ok := emitted[name]; ok {
			return marker
		}
		ref := inst.Fields[name]
		if isTextRef(ref) {
			return ref.Text
		}
		part, mime, _, err := toGenaiPart(ref, true, nil, nil)
		if err != nil {
			replaceErr = err
			return match
		}
		mediaParts = append(mediaParts, part)
		mod := resolvedModality(ref.Modality, mime)
		marker := fmt.Sprintf("[attached %s: %q]", mod, name)
		emitted[name] = marker
		return marker
	})
	if replaceErr != nil {
		return "", nil, replaceErr
	}
	return rendered, mediaParts, nil
}

// toGenaiSchema maps the registry's raw JSON schema into a *genai.Schema. It
// accepts both genai-style (uppercase "OBJECT") and standard JSON-schema
// (lowercase "object") type strings by normalizing the Type field recursively.
func toGenaiSchema(s *registry.Schema) (*genai.Schema, error) {
	if s == nil || strings.TrimSpace(s.JSON) == "" {
		return nil, fmt.Errorf("eval: custom_schema has empty response schema JSON")
	}
	var gs genai.Schema
	dec := json.NewDecoder(strings.NewReader(s.JSON))
	if err := dec.Decode(&gs); err != nil {
		return nil, fmt.Errorf("eval: parse response schema JSON: %w", err)
	}
	normalizeSchemaTypes(&gs)
	if gs.Type == "" {
		return nil, fmt.Errorf("eval: response schema is missing a top-level type")
	}
	return &gs, nil
}

func normalizeSchemaTypes(s *genai.Schema) {
	if s == nil {
		return
	}
	if s.Type != "" {
		s.Type = genai.Type(strings.ToUpper(string(s.Type)))
	}
	for _, p := range s.Properties {
		normalizeSchemaTypes(p)
	}
	normalizeSchemaTypes(s.Items)
}

// parseCustomOutput decodes the model's JSON verdict into a generic map. A
// defensive markdown-fence strip guards against models that wrap JSON in
// ```json fences (not observed with 2.5-flash + JSON mime type, but cheap
// insurance — spike-custom).
func parseCustomOutput(raw string) (map[string]any, error) {
	text := stripJSONFence(strings.TrimSpace(raw))
	if text == "" {
		return nil, fmt.Errorf("eval: custom_schema response was empty")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("eval: parse custom_schema output as JSON: %w", err)
	}
	return out, nil
}

func stripJSONFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimPrefix(s, "JSON")
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// genaiModelID returns the bare, publisher-relative model id the genai SDK
// expects (e.g. "gemini-2.5-flash"). Unlike the native EvaluateInstances path,
// genai does not require the full project-scoped resource name.
//
// The model has already been resolved (Engine.Run → resolveModel, never empty)
// and validated (Engine.Run → ValidateModel) before reaching here, so the former
// empty-string fallback branch was dead and has been removed.
func genaiModelID(model string) string {
	return bareModelID(model)
}

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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

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
// prompt (text placeholders substituted inline, non-text assets sent as inline
// Parts), maps the template's ResponseSchema into a *genai.Schema, calls
// GenerateContent with strict JSON output and exponential backoff, and parses
// the JSON response into Result.CustomOutput (with the raw text in RawOutput).
func (e *Engine) runCustomSchema(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string) (Result, error) {
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

	return e.runGenaiStructured(ctx, tmpl, inst, tmpl.MetricPromptTemplate, schema, model)
}

// runGenaiStructured is the shared genai structured-output core: it renders the
// given prompt (text placeholders substituted inline, non-text assets as inline
// Parts), calls GenerateContent with strict JSON output (the supplied schema) and
// exponential backoff, and parses the JSON response into Result.CustomOutput
// (raw text in RawOutput, token usage in Stats). Both the custom_schema path
// (runCustomSchema) and the rubric-detail path (runRubricStructured) delegate
// here so the single genai call site is not duplicated — the two callers differ
// only in how they build the prompt and schema. prompt is the FULLY rendered
// judge prompt (custom_schema passes the template verbatim; rubric-detail appends
// its per-criterion instruction block); its {{placeholders}} are validated and
// substituted from inst here.
func (e *Engine) runGenaiStructured(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, prompt string, schema *genai.Schema, model string) (Result, error) {
	if e.genai == nil {
		return Result{}, fmt.Errorf("eval: no genai client configured for structured output (wire WithGenaiClient)")
	}

	rendered, mediaParts, err := renderGenaiPrompt(prompt, inst)
	if err != nil {
		return Result{}, err
	}

	parts := append([]*genai.Part{genai.NewPartFromText(rendered)}, mediaParts...)
	contents := []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}

	cfg := &genai.GenerateContentConfig{
		ResponseMIMEType: "application/json",
		ResponseSchema:   schema,
		Temperature:      genai.Ptr[float32](0.0),
	}
	if tmpl.SystemInstruction != "" {
		cfg.SystemInstruction = genai.NewContentFromText(tmpl.SystemInstruction, genai.RoleUser)
	}

	resp, err := e.generateWithBackoff(ctx, genaiModelID(model), contents, cfg)
	if err != nil {
		return Result{}, err
	}

	raw := resp.Text()
	out, err := parseCustomOutput(raw)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		CustomOutput: out,
		RawOutput:    []string{raw},
	}
	// Token usage is available ONLY on this genai path, from the response's
	// UsageMetadata (WI-F4). The native EvaluateInstances response carries none,
	// so Result.Stats.TokenUsage stays nil there.
	if um := resp.UsageMetadata; um != nil {
		res.Stats.TokenUsage = &TokenUsage{
			PromptTokens:     um.PromptTokenCount,
			CandidatesTokens: um.CandidatesTokenCount,
			TotalTokens:      um.TotalTokenCount,
		}
	}
	return res, nil
}

// generateWithBackoff calls GenerateContent, retrying only on
// RESOURCE_EXHAUSTED with full-jitter exponential backoff, honoring ctx during
// the sleep (spike-custom).
func (e *Engine) generateWithBackoff(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	pol := e.retry
	if pol.maxAttempts <= 0 {
		pol = defaultRetryPolicy()
	}

	var lastErr error
	delay := pol.baseDelay
	for attempt := 1; attempt <= pol.maxAttempts; attempt++ {
		resp, err := e.genai.GenerateContent(ctx, model, contents, cfg)
		if err == nil {
			return resp, nil
		}
		if !isResourceExhausted(err) {
			return nil, fmt.Errorf("eval: custom_schema generate (non-retryable): %w", err)
		}
		lastErr = err
		if attempt == pol.maxAttempts {
			break
		}
		// Full jitter over [0.5*delay, delay].
		sleep := time.Duration(float64(delay) * (0.5 + 0.5*rand.Float64()))
		select {
		case <-time.After(sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		delay *= 2
		if delay > pol.maxDelay {
			delay = pol.maxDelay
		}
	}
	return nil, fmt.Errorf("eval: custom_schema generate: exhausted %d attempts: %w", pol.maxAttempts, lastErr)
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

// renderGenaiPrompt substitutes text placeholders inline and collects non-text
// assets as separate inline Parts (genai accepts inline bytes). It validates
// that every referenced variable has a value, failing fast before the API call.
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
	rendered := varPattern.ReplaceAllStringFunc(template, func(match string) string {
		name := varPattern.FindStringSubmatch(match)[1]
		ref := inst.Fields[name]
		if ref.Modality == "" || ref.Modality == registry.ModalityText {
			return ref.Text
		}
		part, err := toGenaiInlinePart(ref)
		if err != nil {
			replaceErr = err
			return match
		}
		mediaParts = append(mediaParts, part)
		return fmt.Sprintf("[attached %s: %q]", ref.Modality, name)
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

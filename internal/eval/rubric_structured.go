package eval

// rubric_structured.go holds the rubric per-criterion transparency path
// (KindRubric + --rubric-detail). Unlike the native rubric path (native.go,
// runRubric), which renders the authored criteria into a pointwise judge prompt
// and returns a single {score, explanation}, this path routes through the genai
// structured-output machinery (the same core as runCustomSchema) with a
// deterministic ResponseSchema, so the judge returns one typed
// {group, criterion, score, rationale} entry PER authored criterion plus an
// overall_score and explanation.
//
// This path runs on location=global (the genai client) and does NOT apply
// AutoraterConfig.SamplingCount — both accepted trade-offs of routing rubric
// through the genai path (see design/rubric-decisions-locked.md).

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
)

// runRubricStructured materializes the rubric per-criterion transparency path. It
// builds a deterministic ResponseSchema from the authored RubricGroups, renders a
// per-criterion judge instruction carrying the configured Likert scale, delegates
// to the shared genai structured-output core, then validates/clamps the judge's
// scores into [min,max] and surfaces overall_score as Result.Score. The full
// {per_criterion, overall_score, explanation} structure is kept in
// Result.CustomOutput.
func (e *Engine) runRubricStructured(ctx context.Context, tmpl registry.MetricTemplate, inst Instance, model string, min, max int) (Result, error) {
	// Guard the genai client FIRST (matching runCustomSchema's ordering) so a
	// missing client fails with the client error before any prompt/schema work.
	if e.genai == nil {
		return Result{}, fmt.Errorf("eval: no genai client configured for rubric detail (wire WithGenaiClient)")
	}
	if tmpl.MetricPromptTemplate == "" {
		return Result{}, fmt.Errorf("eval: template %q has empty metric prompt template", tmpl.ID)
	}
	if len(tmpl.RubricGroups) == 0 {
		return Result{}, fmt.Errorf("eval: rubric template %q has no rubric groups", tmpl.ID)
	}

	schema := generateRubricSchema()
	prompt := tmpl.MetricPromptTemplate + "\n\n" + renderRubricInstruction(tmpl.RubricGroups, min, max)

	res, err := e.runGenaiStructured(ctx, tmpl, inst, prompt, schema, model)
	if err != nil {
		return Result{}, err
	}

	// Validate/clamp the judge output into the configured scale and coerce
	// per-criterion scores to int (JSON numbers decode as float64), then surface
	// overall_score as Result.Score while keeping the full structure in
	// CustomOutput.
	clampRubricOutput(res.CustomOutput, min, max)
	if s, ok := overallScore(res.CustomOutput); ok {
		f := float32(s)
		res.Score = &f
	}
	// Mark the result so the renderer routes on this explicit signal rather than
	// sniffing CustomOutput's shape.
	res.RubricDetail = true
	return res, nil
}

// generateRubricSchema builds the fixed, deterministic genai ResponseSchema for
// the rubric-detail path. The per_criterion array is a generic list of
// {group, criterion, score(int), rationale} objects (the authored criteria are
// enumerated into the judge INSTRUCTION, not the schema), so the schema shape is
// stable and identical across runs. Built directly as a *genai.Schema (types
// already upper-cased) so it needs no JSON round-trip / normalizeSchemaTypes.
func generateRubricSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"per_criterion": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"group":     {Type: genai.TypeString},
						"criterion": {Type: genai.TypeString},
						"score":     {Type: genai.TypeInteger},
						"rationale": {Type: genai.TypeString},
					},
					Required: []string{"group", "criterion", "score", "rationale"},
				},
			},
			"overall_score": {Type: genai.TypeNumber},
			"explanation":   {Type: genai.TypeString},
		},
		Required: []string{"per_criterion", "overall_score", "explanation"},
	}
}

// renderRubricInstruction turns the authored RubricGroups into a deterministic
// per-criterion judge instruction block, mirroring renderRubricGroups' ordering
// (group names sorted; criteria in declared order). It states the configured
// scale so the judge scores every criterion and the overall_score on [min,max].
func renderRubricInstruction(groups map[string][]string, min, max int) string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "Score each criterion below from %d to %d, where %d is worst and %d is best. ", min, max, min, max)
	b.WriteString("For every criterion return an object {group, criterion, score, rationale}, where score is an integer ")
	fmt.Fprintf(&b, "in [%d, %d] and rationale briefly justifies it. ", min, max)
	b.WriteString("Return one entry per criterion in per_criterion, then give an overall_score ")
	fmt.Fprintf(&b, "in the same range (%d to %d) and an overall explanation.\n", min, max)
	for _, name := range names {
		b.WriteString("\n## ")
		b.WriteString(name)
		b.WriteString("\n")
		for _, criterion := range groups[name] {
			b.WriteString("- ")
			b.WriteString(criterion)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// clampRubricOutput validates the judge's structured output against the [min,max]
// scale IN PLACE: it clamps overall_score into range and, for each per_criterion
// entry, coerces score (a JSON float64) to a rounded int clamped into range. It
// tolerates missing/oddly-typed fields (leaving them untouched) rather than
// failing — the schema already constrains the shape; this is defensive
// range-enforcement on the numeric values.
func clampRubricOutput(out map[string]any, min, max int) {
	if out == nil {
		return
	}
	if v, ok := out["overall_score"]; ok {
		if f, ok := toFloat(v); ok {
			out["overall_score"] = clampFloat(f, float64(min), float64(max))
		}
	}
	pc, ok := out["per_criterion"].([]any)
	if !ok {
		return
	}
	for _, item := range pc {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := m["score"]; ok {
			if f, ok := toFloat(v); ok {
				m["score"] = clampInt(int(math.Round(f)), min, max)
			}
		}
	}
}

// overallScore extracts overall_score from the structured output as a float64.
func overallScore(out map[string]any) (float64, bool) {
	if out == nil {
		return 0, false
	}
	if v, ok := out["overall_score"]; ok {
		return toFloat(v)
	}
	return 0, false
}

// toFloat coerces the numeric shapes a decoded JSON value can take (float64 from
// encoding/json, plus int/float32 for programmatically-built maps in tests) to a
// float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// ParseRubricScale parses the --rubric-scale flag value in the form "<min>-<max>"
// (two integers, min < max), returning a crisp local error for any malformed
// value so it fails before the eval call rather than being sent to the judge.
//
// DELIBERATE LIMITATION: only NON-NEGATIVE Likert ranges are supported. "-" is
// the field separator, so a negative bound (e.g. "-2-5") splits into more than
// two fields and is rejected as malformed. This is intentional — practical Likert
// rubric scales are non-negative (e.g. "1-5", "0-10") — and is NOT an oversight;
// negative-bound parsing is explicitly out of scope. Callers wanting a signed
// range would need a different flag format (a follow-up, not this task).
func ParseRubricScale(s string) (min, max int, err error) {
	trimmed := strings.TrimSpace(s)
	parts := strings.Split(trimmed, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("eval: invalid --rubric-scale %q: want \"<min>-<max>\" with two non-negative integers, e.g. \"1-5\" or \"0-10\" (negative bounds are not supported)", s)
	}
	min, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("eval: invalid --rubric-scale %q: min is not a non-negative integer: %v", s, err)
	}
	max, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("eval: invalid --rubric-scale %q: max is not a non-negative integer: %v", s, err)
	}
	if min >= max {
		return 0, 0, fmt.Errorf("eval: invalid --rubric-scale %q: min (%d) must be less than max (%d)", s, min, max)
	}
	return min, max, nil
}

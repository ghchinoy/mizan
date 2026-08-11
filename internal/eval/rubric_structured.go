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

	// Reconcile the judge-returned per_criterion list against the AUTHORED
	// (group, criterion) set (R-R2). MISSING and DUPLICATE authored criteria are
	// hard errors (they corrupt the scorecard); EXTRA (unauthored) criteria are
	// kept in the output and surfaced as warnings on res.Warnings.
	warnings, err := reconcileRubricOutput(res.CustomOutput, tmpl.RubricGroups)
	if err != nil {
		return Result{}, err
	}
	// APPEND (not overwrite): runGenaiStructured may already have populated
	// res.Warnings with the H3 genai-path autorater-field warnings (RFC-0001 §5.4).
	// Preserve those, then add the per-criterion reconciliation warnings.
	res.Warnings = append(res.Warnings, warnings...)

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

// rubricPair identifies an authored criterion by its (group, criterion) pair,
// matched by EXACT string equality on both fields — the same strings that were
// SENT to the judge by renderRubricInstruction (group names and criterion
// strings, verbatim). Using a struct key (rather than a joined string) keeps the
// two fields independent, so identical criterion strings in different groups stay
// distinct and no delimiter can collide.
type rubricPair struct {
	group     string
	criterion string
}

// reconcileRubricOutput reconciles the judge-returned per_criterion entries in
// out against the AUTHORED (group, criterion) set from groups, enforcing the
// R-R2 owner-locked semantics:
//
//   - MISSING (authored but not returned): HARD ERROR — missing criteria corrupt
//     the scorecard. The returned error names the missing pair(s).
//   - DUPLICATE (the same authored pair returned more than once): HARD ERROR —
//     duplicates corrupt the scorecard. The error names the duplicated pair(s).
//   - EXTRA (returned but not authored): WARN + PASSTHROUGH — extras are
//     informative, not corrupting; they are LEFT in out and reported as warnings.
//   - HAPPY PATH (exact 1:1 authored<->returned): no error, no warnings.
//
// Errors use the existing eval:-prefixed style; the returned warnings are
// surfaced by the caller (Result.Warnings, printed to stderr by the CLI).
func reconcileRubricOutput(out map[string]any, groups map[string][]string) ([]string, error) {
	// Authored pairs (and their canonical order for deterministic messages).
	authored := make(map[rubricPair]bool, len(groups))
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	authoredOrder := make([]rubricPair, 0)
	for _, name := range names {
		for _, criterion := range groups[name] {
			p := rubricPair{group: name, criterion: criterion}
			if !authored[p] {
				authored[p] = true
				authoredOrder = append(authoredOrder, p)
			}
		}
	}

	// Count how many times the judge returned each authored pair, and collect
	// extras (unauthored pairs) in returned order for stable warnings.
	returnedCount := make(map[rubricPair]int, len(authored))
	var extras []rubricPair
	if pc, ok := out["per_criterion"].([]any); ok {
		for _, item := range pc {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			p := rubricPair{group: fieldAsString(m, "group"), criterion: fieldAsString(m, "criterion")}
			if authored[p] {
				returnedCount[p]++
			} else {
				extras = append(extras, p)
			}
		}
	}

	// MISSING and DUPLICATE authored pairs -> hard error (both categories are
	// reported together so one run surfaces every scorecard-corrupting problem).
	var missing, duplicate []rubricPair
	for _, p := range authoredOrder {
		switch returnedCount[p] {
		case 0:
			missing = append(missing, p)
		case 1:
			// exact match, nothing to do
		default:
			duplicate = append(duplicate, p)
		}
	}
	if len(missing) > 0 || len(duplicate) > 0 {
		var b strings.Builder
		b.WriteString("eval: rubric reconciliation failed:")
		if len(missing) > 0 {
			fmt.Fprintf(&b, " missing authored criterion(s): %s", formatPairs(missing))
		}
		if len(duplicate) > 0 {
			if len(missing) > 0 {
				b.WriteString(";")
			}
			fmt.Fprintf(&b, " duplicated authored criterion(s): %s", formatPairs(duplicate))
		}
		return nil, fmt.Errorf("%s", b.String())
	}

	// EXTRA pairs -> warn + passthrough (kept in out, one warning per extra entry).
	var warnings []string
	for _, p := range extras {
		warnings = append(warnings, fmt.Sprintf(
			"mizan: rubric reconciliation warning: judge returned unauthored criterion (group=%q, criterion=%q); kept in output",
			p.group, p.criterion))
	}
	return warnings, nil
}

// fieldAsString returns m[key] as a string, or "" if absent or non-string. Used
// to read a per_criterion entry's identity fields for reconciliation.
func fieldAsString(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// formatPairs renders a slice of rubricPair for an error message, e.g.
// [group="clarity" criterion="No jargon", group="tone" criterion="..."].
func formatPairs(pairs []rubricPair) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("group=%q criterion=%q", p.group, p.criterion)
	}
	return "[" + strings.Join(parts, ", ") + "]"
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
	if !rubricScaleBoundsValid(min, max) {
		return 0, 0, fmt.Errorf("eval: invalid --rubric-scale %q: min (%d) must be less than max (%d)", s, min, max)
	}
	return min, max, nil
}

// rubricScaleBoundsValid reports whether [min,max] is a usable Likert range under
// the SAME contract ParseRubricScale enforces on the --rubric-scale flag:
// non-negative bounds with min strictly less than max. It is factored out so the
// flag path (ParseRubricScale) and the template-declared-scale path
// (Engine.resolveRubricScale) validate IDENTICALLY rather than diverging — a
// template scale must be no less trustworthy than a flag scale.
func rubricScaleBoundsValid(min, max int) bool {
	return min >= 0 && min < max
}

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

// Package staxexport converts a Mizan MetricTemplate into one or more Stax
// LLMEvaluator definitions, per the interchange spec
// (design/mizan-stax-export-spec.md) and the L1 fidelity finding
// (state/l1-fidelity-spike-finding.md, OQ-C = GO).
//
// It is a PURE, local, credential-free transform: it reads a registry template
// (already loaded from the local registry) and returns Stax evaluator values.
// It never calls Vertex/genai, never opens a network connection, and never
// reads, emits, or migrates credentials (design invariants N3, ADC-only). The
// caller (cmd/mizan) is responsible for loading the template and writing the
// JSON output.
//
// Fidelity decisions implemented here (from the finding + spec §5, and the two
// authoritative PR-#87 review clarifications):
//
//   - rubric  -> Option B (fan-out) by DEFAULT: one evaluator per (group,
//     criterion) pair, grouped via the "{id}::{group}::{criterion}" name
//     convention. Option A (flatten) is opt-in (Options.Flatten) and MUST emit a
//     dropped-granularity warning.
//   - pointwise -> direct map: RatingRubric bands -> output_categories, one
//     evaluator.
//   - pairwise / custom_schema / non-text modalities -> FAIL CLOSED with a clear
//     "unsupported in v1" error (no lossy guess, nothing emitted).
//   - Placeholder rename map (spec §4) applied to prompt/system bodies. An
//     unmapped placeholder is a HARD ERROR; two distinct Mizan fields that map to
//     the SAME Stax reserved var (an alias collision) is a HARD ERROR.
//   - Category names use the EXPLICIT derivation rule: an anchored band (one with
//     a RatingRubric description) is "{band}-{desc}"; an interior/un-anchored band
//     is "score-{band}".
package staxexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ghchinoy/mizan/internal/registry"
)

// ModelInput is a Stax evaluator prompt/system input: a role plus a template
// body carrying {{stax_var}} substitutions. It mirrors Stax's ModelInput entity
// (google-labs-code/stax server/.../entitities/ModelInput.java), whose message
// content is a role (InputRole enum: USER, ASSISTANT, SYSTEM, …) plus body text.
type ModelInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Evaluator is a single Stax LLMEvaluator: a prompt template, an optional system
// instruction (a ModelInput with role "system" — spec §3, matching Stax's
// InputRole.SYSTEM), and the named output_categories mapped to their numeric
// scores. It has a custom MarshalJSON (see below).
type Evaluator struct {
	Name             string         `json:"name"`
	System           *ModelInput    `json:"system,omitempty"`
	Prompt           ModelInput     `json:"prompt"`
	OutputCategories map[string]int `json:"output_categories"`
}

// MarshalJSON renders the evaluator with output_categories in ascending
// numeric-value order (ties broken by category name), not the lexical key order
// a plain map[string]int would emit. A map would serialize "5-great" before
// "score-2" (because '5' < 's'), which neither matches Stax's ordered category
// list (google-labs-code/stax LLMEvaluator DTO: output_categories is a
// List<OutputCategoryDTO>, an ORDERED list) nor the interchange spec's §6 worked
// examples. Emitting in value order keeps the wire bytes deterministic and makes
// the JSON self-consistent (1→N). Field order is fixed: name, system (omitted
// when absent), prompt, output_categories. HTML escaping is disabled so template
// braces stay literal ({{output}}), independent of the caller's encoder settings.
func (e Evaluator) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	name, err := marshalCompact(e.Name)
	if err != nil {
		return nil, err
	}
	b.WriteString(`{"name":`)
	b.Write(name)
	if e.System != nil {
		sys, err := marshalCompact(e.System)
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"system":`)
		b.Write(sys)
	}
	prompt, err := marshalCompact(e.Prompt)
	if err != nil {
		return nil, err
	}
	b.WriteString(`,"prompt":`)
	b.Write(prompt)

	type cat struct {
		name  string
		value int
	}
	ordered := make([]cat, 0, len(e.OutputCategories))
	for k, v := range e.OutputCategories {
		ordered = append(ordered, cat{k, v})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].value != ordered[j].value {
			return ordered[i].value < ordered[j].value
		}
		return ordered[i].name < ordered[j].name
	})
	b.WriteString(`,"output_categories":{`)
	for i, o := range ordered {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := marshalCompact(o.name)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		fmt.Fprintf(&b, ":%d", o.value)
	}
	b.WriteString("}}")
	return b.Bytes(), nil
}

// marshalCompact encodes v as compact JSON with HTML escaping disabled, so a
// prompt body's {{output}} braces (and any <, >, & in user text) survive
// verbatim. The caller's encoder re-indents this output uniformly.
func marshalCompact(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Options controls the export.
type Options struct {
	// Flatten selects rubric Option A (one evaluator, aggregate bands) instead of
	// the default Option B (fan-out, one evaluator per criterion). It has no effect
	// on pointwise. When set for a rubric template, Export emits a
	// dropped-per-criterion-granularity warning.
	Flatten bool

	// PlaceholderOverride is the configurable rename table (spec §4 rule 3): it
	// maps a Mizan input field name (matched case-insensitively) to a Stax reserved
	// var, letting a template with a non-conventional field name export without
	// renaming the source. Entries here win over the built-in alias table.
	PlaceholderOverride map[string]string
}

// Result is the outcome of a successful export: the evaluators to write and any
// non-fatal warnings the caller should surface (e.g. the flatten granularity
// loss).
type Result struct {
	Evaluators []Evaluator
	Warnings   []string
}

// staxReservedVars is the fixed set of Stax reserved template variables a Mizan
// field may be renamed to (spec §2, §4). system.instruction is mapped from the
// SystemInstruction field, not from a body placeholder.
var staxReservedVars = []string{"output", "prompt", "expected_output", "history"}

// defaultAliases maps a lowercased Mizan input field name to its Stax reserved
// var (spec §4). Pairwise candidate/baseline SxS vars are intentionally absent —
// pairwise is unsupported in v1 and fails closed before any rename.
var defaultAliases = map[string]string{
	// the thing being judged -> {{output}}
	"response":  "output",
	"output":    "output",
	"answer":    "output",
	"candidate": "output",
	// the user/task prompt -> {{prompt}}
	"prompt":      "prompt",
	"question":    "prompt",
	"input":       "prompt",
	"instruction": "prompt",
	// reference answer -> {{expected_output}}
	"reference":    "expected_output",
	"expected":     "expected_output",
	"ground_truth": "expected_output",
	"gold":         "expected_output",
	// prior turns -> {{history}}
	"history":      "history",
	"conversation": "history",
	"context":      "history",
}

// placeholderPattern matches a {{name}} placeholder (optionally spaced),
// mirroring the registry's own grammar (internal/registry/validate.go).
var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// Export converts a Mizan template into Stax evaluators per the spec. It fails
// closed (returns an error, emits nothing) on unsupported kinds/modalities, an
// unmapped placeholder, or a placeholder alias collision.
func Export(t registry.MetricTemplate, opts Options) (Result, error) {
	if err := checkSupported(t); err != nil {
		return Result{}, err
	}

	r := newRenamer(opts.PlaceholderOverride)

	switch t.Kind {
	case registry.KindPointwise:
		return exportPointwise(t, r)
	case registry.KindRubric:
		if opts.Flatten {
			return exportRubricFlatten(t, r)
		}
		return exportRubricFanout(t, r)
	default:
		// Defensive: checkSupported already rejected the other kinds.
		return Result{}, fmt.Errorf("staxexport: metric kind %q is unsupported in v1", t.Kind)
	}
}

// checkSupported fails closed on the kinds and modalities L1 v1 does not export.
func checkSupported(t registry.MetricTemplate) error {
	switch t.Kind {
	case registry.KindPairwise:
		return fmt.Errorf("staxexport: metric kind %q is unsupported in v1: Stax pairwise/SxS is a different evaluator shape and Mizan's flip/sampling controls have no Stax equivalent; export a pointwise or rubric template instead", t.Kind)
	case registry.KindCustomSchema:
		return fmt.Errorf("staxexport: metric kind %q is unsupported in v1: Stax has no categorical analogue for an arbitrary JSON response schema; export a pointwise or rubric template instead", t.Kind)
	case registry.KindPointwise, registry.KindRubric:
		// supported
	default:
		return fmt.Errorf("staxexport: metric kind %q is unsupported in v1", t.Kind)
	}
	// An empty Modalities set (none declared) is treated as supported: it carries
	// no non-text modality, and text is the default/only faithful Stax target. Only
	// an explicitly-declared non-text modality fails closed.
	for _, m := range t.Modalities {
		if m != registry.ModalityText {
			return fmt.Errorf("staxexport: modality %q is unsupported in v1: Stax evaluators are text-centric and non-text modalities have no faithful target; export a text-modality template instead", m)
		}
	}
	return nil
}

// exportPointwise maps a pointwise template to a single Stax evaluator: the
// (renamed) prompt body plus output_categories derived from the resolved Likert
// scale and RatingRubric band descriptions (spec §3, decision 3).
func exportPointwise(t registry.MetricTemplate, r renamer) (Result, error) {
	sys, prompt, err := r.rewriteBodies2(t.SystemInstruction, t.MetricPromptTemplate)
	if err != nil {
		return Result{}, err
	}
	min, max, err := resolveScale(t)
	if err != nil {
		return Result{}, err
	}
	ev := Evaluator{
		Name:             t.ID,
		Prompt:           ModelInput{Role: "user", Content: prompt},
		OutputCategories: categoriesForScale(min, max, mergeBands(t.RatingRubric)),
	}
	if sys != "" {
		ev.System = &ModelInput{Role: "system", Content: sys}
	}
	return Result{Evaluators: []Evaluator{ev}}, nil
}

// exportRubricFanout implements Option B (the default): one evaluator per
// (group, criterion) pair, grouped via the "{id}::{group}::{criterion}" name
// convention (finding decision 1+4, spec §5 Option B).
func exportRubricFanout(t registry.MetricTemplate, r renamer) (Result, error) {
	if len(t.RubricGroups) == 0 {
		return Result{}, fmt.Errorf("staxexport: rubric template %q has no rubric groups to export", t.ID)
	}
	sys, base, err := r.rewriteBodies2(t.SystemInstruction, t.MetricPromptTemplate)
	if err != nil {
		return Result{}, err
	}
	min, max, err := resolveScale(t)
	if err != nil {
		return Result{}, err
	}

	var evs []Evaluator
	for _, group := range sortedGroups(t.RubricGroups) {
		bands := t.RatingRubric[group] // nil when the group has no RatingRubric
		for _, criterion := range t.RubricGroups[group] {
			content := fmt.Sprintf("Evaluate {{output}} on the criterion %q (rubric group: %s). Return ONE category.", criterion, group)
			if base != "" {
				content = base + "\n\n" + content
			}
			ev := Evaluator{
				Name:             fmt.Sprintf("%s::%s::%s", t.ID, group, criterion),
				Prompt:           ModelInput{Role: "user", Content: content},
				OutputCategories: categoriesForScale(min, max, bands),
			}
			if sys != "" {
				ev.System = &ModelInput{Role: "system", Content: sys}
			}
			evs = append(evs, ev)
		}
	}
	return Result{Evaluators: evs}, nil
}

// exportRubricFlatten implements Option A (opt-in): one evaluator whose prompt
// embeds every criterion and whose categories are the aggregate generic Likert
// bands. It emits the required dropped-per-criterion-granularity warning (spec §5
// Option A, decision 2).
func exportRubricFlatten(t registry.MetricTemplate, r renamer) (Result, error) {
	if len(t.RubricGroups) == 0 {
		return Result{}, fmt.Errorf("staxexport: rubric template %q has no rubric groups to export", t.ID)
	}
	sys, base, err := r.rewriteBodies2(t.SystemInstruction, t.MetricPromptTemplate)
	if err != nil {
		return Result{}, err
	}
	min, max, err := resolveScale(t)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}
	b.WriteString("Evaluate {{output}} against the following rubric and return ONE overall category.\n")
	var dropped []string
	for _, group := range sortedGroups(t.RubricGroups) {
		for _, criterion := range t.RubricGroups[group] {
			fmt.Fprintf(&b, "- [%s] %s\n", group, criterion)
			dropped = append(dropped, fmt.Sprintf("%s::%s", group, criterion))
		}
	}

	ev := Evaluator{
		Name:             t.ID + " (flattened)",
		Prompt:           ModelInput{Role: "user", Content: b.String()},
		OutputCategories: categoriesForScale(min, max, nil), // aggregate: generic score-N
	}
	if sys != "" {
		ev.System = &ModelInput{Role: "system", Content: sys}
	}
	warning := fmt.Sprintf(
		"flatten (Option A) dropped per-criterion granularity: %d criteria collapsed into ONE overall score, and per-criterion scores/rationales and per-group band descriptions are lost (%s)",
		len(dropped), strings.Join(dropped, ", "))
	return Result{Evaluators: []Evaluator{ev}, Warnings: []string{warning}}, nil
}

// resolveScale returns the inclusive Likert range for a template: the template's
// RubricDetail.Scale when declared, else the default 1-5. It rejects an inverted
// or empty range so a malformed scale fails at export rather than producing empty
// categories.
func resolveScale(t registry.MetricTemplate) (int, int, error) {
	min, max := 1, 5
	if t.RubricDetail != nil && t.RubricDetail.Scale != nil {
		min, max = t.RubricDetail.Scale.Min, t.RubricDetail.Scale.Max
	}
	if min >= max {
		return 0, 0, fmt.Errorf("staxexport: rubric scale [%d,%d] is invalid (min must be < max)", min, max)
	}
	return min, max, nil
}

// categoriesForScale builds the output_categories map for one Likert range using
// the EXPLICIT category-name rule (PR-#87 review decision 2): a band with a
// non-empty description is "{band}-{slug(desc)}" (anchored, e.g. "1-poor");
// every other band is "score-{band}" (interior/un-anchored, e.g. "score-2").
// bands may be nil (all generic).
func categoriesForScale(min, max int, bands map[string]string) map[string]int {
	out := make(map[string]int, max-min+1)
	for n := min; n <= max; n++ {
		name := fmt.Sprintf("score-%d", n)
		if bands != nil {
			if desc, ok := bands[strconv.Itoa(n)]; ok {
				if s := slug(desc); s != "" {
					name = fmt.Sprintf("%d-%s", n, s)
				}
			}
		}
		out[name] = n
	}
	return out
}

// mergeBands flattens a RatingRubric (group -> band -> description) into a single
// band -> description map for the pointwise direct map, where there is one
// evaluator and no per-group fan-out. Groups are merged in sorted order so a band
// present in multiple groups resolves deterministically. Returns nil when there
// are no bands (all categories then fall back to generic score-N).
func mergeBands(rr map[string]map[string]string) map[string]string {
	if len(rr) == 0 {
		return nil
	}
	merged := map[string]string{}
	for _, group := range sortedRatingGroups(rr) {
		for band, desc := range rr[group] {
			if strings.TrimSpace(desc) != "" {
				merged[band] = desc
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// slug turns a free-text band description into a category-name token: lowercased,
// with each run of non-alphanumeric characters collapsed to a single hyphen and
// leading/trailing hyphens trimmed (e.g. "Very Good!" -> "very-good").
func slug(s string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevHyphen = false
		case !prevHyphen:
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func sortedGroups(g map[string][]string) []string {
	names := make([]string, 0, len(g))
	for name := range g {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedRatingGroups(rr map[string]map[string]string) []string {
	names := make([]string, 0, len(rr))
	for name := range rr {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// renamer applies the placeholder rename map (spec §4) with the configured
// overrides.
type renamer struct {
	override map[string]string // lowercased Mizan field -> Stax reserved var
}

func newRenamer(override map[string]string) renamer {
	low := make(map[string]string, len(override))
	for k, v := range override {
		low[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return renamer{override: low}
}

// resolve maps a Mizan field name to its Stax reserved var, honoring overrides
// first. The bool is false when the field maps to no reserved var.
func (r renamer) resolve(field string) (string, bool) {
	key := strings.ToLower(field)
	if v, ok := r.override[key]; ok && v != "" {
		return v, true
	}
	v, ok := defaultAliases[key]
	return v, ok
}

// rewriteBodies rewrites {{mizan_field}} -> {{stax_var}} across all given bodies
// (typically system instruction + prompt). It enforces two hard-error rules over
// the UNION of placeholders in all bodies:
//
//   - unmapped field: a placeholder whose field maps to no Stax reserved var
//     (spec §4 rule 2);
//   - alias collision: two DISTINCT Mizan fields that map to the SAME Stax
//     reserved var (PR-#87 review decision 1).
//
// Returned bodies are in the same order as the arguments.
func (r renamer) rewriteBodies(bodies ...string) ([]string, error) {
	// Pass 1: collect the field->var mapping and detect the two hard errors over
	// the union of all bodies, so the failure is reported once, not per-body.
	var unmapped []string
	seenUnmapped := map[string]bool{}
	targetFields := map[string]map[string]bool{} // stax var -> set of source fields
	for _, body := range bodies {
		for _, m := range placeholderPattern.FindAllStringSubmatch(body, -1) {
			field := m[1]
			target, ok := r.resolve(field)
			if !ok {
				if !seenUnmapped[strings.ToLower(field)] {
					seenUnmapped[strings.ToLower(field)] = true
					unmapped = append(unmapped, field)
				}
				continue
			}
			if targetFields[target] == nil {
				targetFields[target] = map[string]bool{}
			}
			targetFields[target][strings.ToLower(field)] = true
		}
	}
	if len(unmapped) > 0 {
		sort.Strings(unmapped)
		return nil, fmt.Errorf(
			"staxexport: prompt references placeholder(s) %s that map to no Stax reserved var; rename the field or supply an override (allowed targets: %s)",
			quoteJoin(unmapped), strings.Join(staxReservedVars, ", "))
	}
	if collisions := collisionMessages(targetFields); len(collisions) > 0 {
		return nil, fmt.Errorf(
			"staxexport: placeholder alias collision — %s; two Mizan fields cannot map to the same Stax reserved var (rename one field or supply an override)",
			strings.Join(collisions, "; "))
	}

	// Pass 2: rewrite. Every placeholder resolves (pass 1 proved it), so the
	// rewrite cannot fail.
	out := make([]string, len(bodies))
	for i, body := range bodies {
		out[i] = placeholderPattern.ReplaceAllStringFunc(body, func(match string) string {
			field := placeholderPattern.FindStringSubmatch(match)[1]
			target, _ := r.resolve(field)
			return "{{" + target + "}}"
		})
	}
	return out, nil
}

// rewriteBodies is most often called for (system, prompt); this thin wrapper
// keeps that common call site readable and returns the two bodies directly.
func (r renamer) rewriteBodies2(system, prompt string) (string, string, error) {
	out, err := r.rewriteBodies(system, prompt)
	if err != nil {
		return "", "", err
	}
	return out[0], out[1], nil
}

// collisionMessages returns a sorted, human-readable description of every Stax
// reserved var that more than one distinct Mizan field maps to.
func collisionMessages(targetFields map[string]map[string]bool) []string {
	var msgs []string
	for target, fields := range targetFields {
		if len(fields) <= 1 {
			continue
		}
		names := make([]string, 0, len(fields))
		for f := range fields {
			names = append(names, f)
		}
		sort.Strings(names)
		msgs = append(msgs, fmt.Sprintf("fields %s all map to {{%s}}", quoteJoin(names), target))
	}
	sort.Strings(msgs)
	return msgs
}

func quoteJoin(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = strconv.Quote(s)
	}
	return strings.Join(q, ", ")
}

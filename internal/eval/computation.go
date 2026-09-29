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

// computation.go implements kind:computation — the Vertex Gen AI Evaluation
// Service "computation-based" metrics, where NO model is involved:
//
//	exact_match, bleu, rouge
//	tool_call_valid, tool_name_match, tool_parameter_key_match, tool_parameter_kv_match
//	trajectory_exact_match, trajectory_in_order_match, trajectory_any_order_match,
//	trajectory_precision, trajectory_recall, trajectory_single_tool_use
//
// Two engines serve them:
//
//   - LOCAL (default; no --engine or --engine local): runComputation, a FREE
//     function (not an *Engine method) so it structurally cannot reach
//     e.client / e.globalClient / e.genai / e.diffusion — the same by-construction
//     credential-free invariant as runHeuristic. No ADC, no network.
//   - VERTEX (--engine vertex): runComputationVertex sends the matching
//     EvaluateInstances oneof input to the REGIONAL client (computation metrics
//     have no autorater, so there is no model to validate and no global-host
//     routing to do). Use it to check local/managed parity.
//
// The local semantics are written to match the Vertex documentation and the
// reference implementations Vertex is built on (sacrebleu for BLEU, Google's
// rouge_score for ROUGE). The choices that the docs leave open are documented at
// each function and summarized in docs/user-guide.md ("Computation metrics").

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/protobuf/proto"

	"github.com/ghchinoy/mizan/internal/registry"
)

// Engine names accepted for kind:computation.
const (
	engineLocal  = "local"
	engineVertex = "vertex"
)

// isDiffusionOverride reports whether an engine override selects DiffusionGemma.
func isDiffusionOverride(e string) bool { return e == "diffusion" || e == "diffgemma" }

// nativeTextFields resolves the template's spec.native role bindings against the
// instance and returns role -> text. It fails LOUD, client-side, on:
//   - a required role whose input was not supplied;
//   - a supplied field that no role reads (the native analogue of the
//     "unknown instance field" silent-drop guard in Engine.Run — the value would
//     never reach the metric);
//   - a non-text value (every native metric scores text).
//
// Optional roles whose input was not supplied are simply absent from the map.
func nativeTextFields(tmpl registry.MetricTemplate, inst Instance) (map[string]string, error) {
	if tmpl.Native == nil {
		return nil, fmt.Errorf("eval: %s template %q has no spec.native", tmpl.Kind, tmpl.ID)
	}
	bindings, err := registry.ResolveNativeFields(tmpl.Native)
	if err != nil {
		return nil, fmt.Errorf("eval: template %q: %w", tmpl.ID, err)
	}
	bound := make(map[string]bool, len(bindings))
	out := make(map[string]string, len(bindings))
	var missing []string
	for _, b := range bindings {
		bound[b.Field] = true
		ref, ok := inst.Fields[b.Field]
		if !ok {
			if b.Required {
				missing = append(missing, b.Field)
			}
			continue
		}
		if !isTextRef(ref) {
			return nil, fmt.Errorf("eval: template %q field %q (%s) must be text (got modality %q)", tmpl.ID, b.Field, b.Role, ref.Modality)
		}
		out[b.Role] = ref.Text
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("eval: template %q (%s %s) is missing required field(s) %v", tmpl.ID, tmpl.Kind, tmpl.Native.Metric, missing)
	}
	var unknown []string
	for k := range inst.Fields {
		if !bound[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		var names []string
		for _, b := range bindings {
			names = append(names, b.Field)
		}
		return nil, fmt.Errorf("eval: unknown instance field(s) %v for template %q; metric %q reads %v — a field no role reads is silently dropped (check for a typo or the spec.native field mapping)", unknown, tmpl.ID, tmpl.Native.Metric, names)
	}
	return out, nil
}

// applyPassThreshold sets res.Passed = Score >= threshold when the template
// declares spec.native.passThreshold and a score is present.
func applyPassThreshold(res *Result, spec *registry.NativeMetricSpec) {
	if spec == nil || spec.PassThreshold == nil || res.Score == nil {
		return
	}
	// Compare in float64 against the float32 score widened, with a tiny epsilon so
	// a score of exactly the threshold (e.g. 0.5 stored as float32) passes.
	passed := float64(*res.Score)+1e-6 >= *spec.PassThreshold
	res.Passed = &passed
	if res.CustomOutput != nil {
		res.CustomOutput["pass_threshold"] = *spec.PassThreshold
		res.CustomOutput["passed"] = passed
	}
}

// runComputation evaluates a kind:computation template LOCALLY, with no
// credential, client, or network access. It is a free function by design (see
// the file comment).
func runComputation(tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	fields, err := nativeTextFields(tmpl, inst)
	if err != nil {
		return Result{}, err
	}
	score, explanation, err := computeLocalMetric(tmpl.Native, fields)
	if err != nil {
		return Result{}, fmt.Errorf("eval: template %q: %w", tmpl.ID, err)
	}
	s32 := float32(score)
	res := Result{
		Score:        &s32,
		Explanation:  explanation,
		CustomOutput: map[string]any{"metric": tmpl.Native.Metric, "engine": engineLocal},
	}
	applyPassThreshold(&res, tmpl.Native)
	return res, nil
}

// computeLocalMetric dispatches one computation metric over the role->text map.
// It returns the score and a short deterministic explanation. A malformed
// PREDICTION (e.g. a response that is not valid tool-call JSON) is a legitimate
// low score, not an error — that is what the metric measures. A malformed
// REFERENCE is gold data the dataset author controls, so it is an error.
func computeLocalMetric(spec *registry.NativeMetricSpec, f map[string]string) (float64, string, error) {
	pred, ref := f[registry.NativeRoleResponse], f[registry.NativeRoleReference]
	switch spec.Metric {
	case "exact_match":
		s := boolScore(strings.TrimSpace(pred) == strings.TrimSpace(ref))
		return s, fmt.Sprintf("exact_match = %g (response %s reference after trimming whitespace)", s, map[bool]string{true: "equals", false: "differs from"}[s == 1]), nil
	case "bleu":
		s := sentenceBLEU(pred, ref)
		return s, fmt.Sprintf("bleu = %.4f (sentence BLEU-4, 13a tokenization, exp smoothing, effective order)", s), nil
	case "rouge":
		rt := spec.EffectiveRougeType()
		s, err := rougeF(rt, pred, ref)
		if err != nil {
			return 0, "", err
		}
		return s, fmt.Sprintf("rouge = %.4f (%s F-measure)", s, rt), nil
	case "tool_call_valid":
		ok, why := toolCallValid(pred)
		return boolScore(ok), "tool_call_valid: " + why, nil
	case "tool_name_match", "tool_parameter_key_match", "tool_parameter_kv_match":
		return toolMetric(spec.Metric, pred, ref)
	case "trajectory_exact_match", "trajectory_in_order_match", "trajectory_any_order_match",
		"trajectory_precision", "trajectory_recall", "trajectory_single_tool_use":
		return trajectoryMetric(spec, pred, ref)
	default:
		return 0, "", fmt.Errorf("unknown computation metric %q", spec.Metric)
	}
}

func boolScore(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// =============================================================================
// BLEU
// =============================================================================

// bleu13aPunct / bleu13aPeriodComma* / bleu13aDash port sacrebleu's
// TokenizerRegexp (the post-tokenizer of the default "13a" tokenizer, equivalent
// to mteval-v13a used by WMT) rule for rule:
//
//  1. pad every ASCII symbol in {-~ [-` space-& (-+ :-@ / with spaces;
//  2. split '.' and ',' unless preceded by a digit;
//  3. split '.' and ',' unless followed by a digit;
//  4. split '-' when preceded by a digit;
//
// then collapse whitespace. Go RE2 and Python re agree on leftmost,
// non-overlapping replacement, so the output is token-identical.
var (
	bleu13aPunct        = regexp.MustCompile("([\\{-\\~\\[-\\` -\\&\\(-\\+\\:-\\@\\/])")
	bleu13aPeriodComma1 = regexp.MustCompile(`([^0-9])([\.,])`)
	bleu13aPeriodComma2 = regexp.MustCompile(`([\.,])([^0-9])`)
	bleu13aDash         = regexp.MustCompile(`([0-9])(-)`)
)

// tokenize13a tokenizes a segment exactly like sacrebleu's default "13a"
// tokenizer (case preserved — sacrebleu's default is case-sensitive). This is
// the tokenization choice for mizan's local BLEU: whitespace tokenization AFTER
// separating punctuation, as the standard WMT tokenizer does, so a local score
// matches sacrebleu.sentence_bleu (and therefore Vertex) on the same strings.
func tokenize13a(line string) []string {
	line = strings.ReplaceAll(line, "<skipped>", "")
	line = strings.ReplaceAll(line, "-\n", "")
	line = strings.ReplaceAll(line, "\n", " ")
	if strings.Contains(line, "&") {
		line = strings.ReplaceAll(line, "&quot;", `"`)
		line = strings.ReplaceAll(line, "&amp;", "&")
		line = strings.ReplaceAll(line, "&lt;", "<")
		line = strings.ReplaceAll(line, "&gt;", ">")
	}
	line = " " + line + " "
	line = bleu13aPunct.ReplaceAllString(line, " ${1} ")
	line = bleu13aPeriodComma1.ReplaceAllString(line, "${1} ${2} ")
	line = bleu13aPeriodComma2.ReplaceAllString(line, " ${1} ${2}")
	line = bleu13aDash.ReplaceAllString(line, "${1} ${2} ")
	return strings.Fields(line)
}

// ngramCounts returns the multiset of n-grams of order n.
func ngramCounts(tokens []string, n int) map[string]int {
	out := map[string]int{}
	for i := 0; i+n <= len(tokens); i++ {
		out[strings.Join(tokens[i:i+n], "\x00")]++
	}
	return out
}

// sentenceBLEU computes sentence-level BLEU-4 on [0,1], matching
// sacrebleu.sentence_bleu(hyp, [ref]) / 100 with its defaults
// (tokenize="13a", smooth_method="exp", use_effective_order=True):
//
//   - clipped n-gram precisions p_n = matches_n / total_n for n = 1..4;
//   - "effective order": orders for which the HYPOTHESIS has no n-grams at all
//     (total_n == 0, i.e. it is shorter than n tokens) are dropped, and the
//     geometric mean is taken over the remaining orders only. This is exactly
//     sacrebleu's use_effective_order, which truncates on total_n == 0;
//   - an order that has n-grams but zero matches is smoothed with sacrebleu's
//     "exp" method (the mteval-v13a NIST smoothing): the k-th such order gets
//     p_n = 1 / (2^k * total_n) instead of collapsing the score to 0;
//   - if there are no unigram..4-gram matches at all, BLEU is 0;
//   - brevity penalty BP = exp(1 - r/c) when c < r (c = hyp tokens, r = ref tokens).
//
// Deviation note: the task brief phrased effective order as "use only orders up
// to the longest with matches". sacrebleu (and therefore Vertex) actually
// truncates on the longest order the hypothesis HAS and smooths zero-match
// orders; we follow sacrebleu so the local engine agrees with Vertex.
func sentenceBLEU(hyp, ref string) float64 {
	const maxOrder = 4
	h, r := tokenize13a(hyp), tokenize13a(ref)
	var correct, total [maxOrder]int
	for n := 1; n <= maxOrder; n++ {
		hc, rc := ngramCounts(h, n), ngramCounts(r, n)
		for g, c := range hc {
			total[n-1] += c
			if rc[g] < c {
				correct[n-1] += rc[g]
			} else {
				correct[n-1] += c
			}
		}
	}
	anyMatch := false
	for _, c := range correct {
		if c > 0 {
			anyMatch = true
		}
	}
	if !anyMatch {
		return 0
	}
	bp := 1.0
	if len(h) < len(r) {
		if len(h) == 0 {
			return 0
		}
		bp = math.Exp(1 - float64(len(r))/float64(len(h)))
	}
	var logSum float64
	effOrder := 0
	smooth := 1.0
	for n := 1; n <= maxOrder; n++ {
		if total[n-1] == 0 {
			break
		}
		effOrder = n
		var p float64
		if correct[n-1] == 0 {
			smooth *= 2
			p = 1 / (smooth * float64(total[n-1]))
		} else {
			p = float64(correct[n-1]) / float64(total[n-1])
		}
		logSum += math.Log(p)
	}
	if effOrder == 0 {
		return 0
	}
	return bp * math.Exp(logSum/float64(effOrder))
}

// =============================================================================
// ROUGE
// =============================================================================

var rougeNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// rougeTokenize matches Google's rouge_score tokenizer without stemming:
// lowercase, replace every run of non-[a-z0-9] characters with a space, split.
// (Non-ASCII letters are therefore dropped, as in rouge_score.) useStemmer is
// rejected at validation, so there is no stemming branch.
func rougeTokenize(s string) []string {
	return strings.Fields(rougeNonAlnum.ReplaceAllString(strings.ToLower(s), " "))
}

// fmeasure is rouge_score's harmonic mean (0 when precision+recall is 0).
func fmeasure(p, r float64) float64 {
	if p+r > 0 {
		return 2 * p * r / (p + r)
	}
	return 0
}

// rougeF returns the ROUGE F-measure of pred against ref for one variant,
// following rouge_score.RougeScorer:
//
//   - rouge1 / rouge2: clipped n-gram overlap; precision = overlap / pred
//     n-grams, recall = overlap / ref n-grams (denominators floored at 1);
//   - rougeL: sentence-level LCS over the whole token sequences;
//   - rougeLsum: summary-level LCS ("union LCS") after splitting BOTH texts into
//     sentences on newlines (empty lines dropped) — rouge_score's behavior with
//     split_summaries=False, which is also what mizan sends to Vertex.
func rougeF(rougeType, pred, ref string) (float64, error) {
	switch rougeType {
	case registry.RougeType1, registry.RougeType2:
		n := 1
		if rougeType == registry.RougeType2 {
			n = 2
		}
		pc, rc := ngramCounts(rougeTokenize(pred), n), ngramCounts(rougeTokenize(ref), n)
		overlap, pTotal, rTotal := 0, 0, 0
		for g, c := range rc {
			rTotal += c
			overlap += min(c, pc[g])
		}
		for _, c := range pc {
			pTotal += c
		}
		p := float64(overlap) / float64(max(pTotal, 1))
		r := float64(overlap) / float64(max(rTotal, 1))
		return fmeasure(p, r), nil
	case registry.RougeTypeL:
		pt, rt := rougeTokenize(pred), rougeTokenize(ref)
		if len(pt) == 0 || len(rt) == 0 {
			return 0, nil
		}
		l := lcsTable(rt, pt)[len(rt)][len(pt)]
		return fmeasure(float64(l)/float64(len(pt)), float64(l)/float64(len(rt))), nil
	case registry.RougeTypeLsum:
		return summaryLevelLCS(rougeSentences(ref), rougeSentences(pred)), nil
	default:
		return 0, fmt.Errorf("unknown rougeType %q", rougeType)
	}
}

// rougeSentences splits on newlines, drops empty lines, and tokenizes each.
func rougeSentences(s string) [][]string {
	var out [][]string
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		out = append(out, rougeTokenize(line))
	}
	return out
}

// lcsTable is the standard LCS dynamic-programming table (rows: ref, cols: can).
func lcsTable(ref, can []string) [][]int {
	t := make([][]int, len(ref)+1)
	for i := range t {
		t[i] = make([]int, len(can)+1)
	}
	for i := 1; i <= len(ref); i++ {
		for j := 1; j <= len(can); j++ {
			if ref[i-1] == can[j-1] {
				t[i][j] = t[i-1][j-1] + 1
			} else {
				t[i][j] = max(t[i-1][j], t[i][j-1])
			}
		}
	}
	return t
}

// lcsIndices backtracks an LCS table into the ref-token indices of one LCS,
// breaking ties exactly like rouge_score's _backtrack_norec.
func lcsIndices(ref, can []string) []int {
	t := lcsTable(ref, can)
	var idx []int
	i, j := len(ref), len(can)
	for i > 0 && j > 0 {
		switch {
		case ref[i-1] == can[j-1]:
			idx = append([]int{i - 1}, idx...)
			i--
			j--
		case t[i][j-1] > t[i-1][j]:
			j--
		default:
			i--
		}
	}
	return idx
}

// summaryLevelLCS ports rouge_score's _summary_level_lcs: for each reference
// sentence take the UNION of its LCS positions against every candidate
// sentence, and count a hit per union token while both texts still have an
// unconsumed occurrence of it. precision = hits / candidate tokens, recall =
// hits / reference tokens.
func summaryLevelLCS(refSents, canSents [][]string) float64 {
	if len(refSents) == 0 || len(canSents) == 0 {
		return 0
	}
	m, n := 0, 0
	refCnt, canCnt := map[string]int{}, map[string]int{}
	for _, s := range refSents {
		m += len(s)
		for _, tok := range s {
			refCnt[tok]++
		}
	}
	for _, s := range canSents {
		n += len(s)
		for _, tok := range s {
			canCnt[tok]++
		}
	}
	if m == 0 || n == 0 {
		return 0
	}
	hits := 0
	for _, r := range refSents {
		union := map[int]bool{}
		for _, c := range canSents {
			for _, i := range lcsIndices(r, c) {
				union[i] = true
			}
		}
		positions := make([]int, 0, len(union))
		for i := range union {
			positions = append(positions, i)
		}
		sort.Ints(positions)
		for _, i := range positions {
			tok := r[i]
			if canCnt[tok] > 0 && refCnt[tok] > 0 {
				hits++
				canCnt[tok]--
				refCnt[tok]--
			}
		}
	}
	return fmeasure(float64(hits)/float64(n), float64(hits)/float64(m))
}

// =============================================================================
// Tool-call metrics
// =============================================================================

// toolCall is one parsed call from a {"content", "tool_calls": [...]} document.
type toolCall struct {
	name    string
	nameOK  bool           // "name" present and a non-empty string
	args    map[string]any // decoded "arguments" object
	argsOK  bool           // "arguments" present and a JSON object
	present bool
}

// parseToolCalls parses the Vertex tool-call document shape
// {"content": "...", "tool_calls": [{"name": "...", "arguments": {...}}]}.
// A missing or null tool_calls is zero calls.
func parseToolCalls(s string) ([]toolCall, error) {
	var doc struct {
		ToolCalls []json.RawMessage `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, err
	}
	out := make([]toolCall, 0, len(doc.ToolCalls))
	for _, raw := range doc.ToolCalls {
		var m map[string]json.RawMessage
		tc := toolCall{present: true}
		if json.Unmarshal(raw, &m) == nil {
			var name string
			if json.Unmarshal(m["name"], &name) == nil && name != "" {
				tc.name, tc.nameOK = name, true
			}
			var args map[string]any
			if a, ok := m["arguments"]; ok && json.Unmarshal(a, &args) == nil && args != nil {
				tc.args, tc.argsOK = args, true
			}
		}
		out = append(out, tc)
	}
	return out, nil
}

// toolCallValid: 1 when the response is tool-call JSON whose FIRST tool call has
// a non-empty string "name" and an "arguments" JSON object. Following the Vertex
// docs, tool_call_valid and tool_name_match look at the first tool call only; a
// response with no tool call at all is not a valid tool call.
func toolCallValid(pred string) (bool, string) {
	calls, err := parseToolCalls(pred)
	if err != nil {
		return false, "response is not valid tool-call JSON"
	}
	if len(calls) == 0 {
		return false, "response has no tool_calls"
	}
	c := calls[0]
	switch {
	case !c.nameOK:
		return false, "first tool call has no name"
	case !c.argsOK:
		return false, "first tool call has no arguments object"
	}
	return true, fmt.Sprintf("first tool call %q is well-formed", c.name)
}

// toolMetric implements tool_name_match, tool_parameter_key_match and
// tool_parameter_kv_match on the FIRST tool call of the response and reference.
//
// Definitions (kept close to the Vertex docs; the docs do not pin the edge cases,
// so these are mizan's documented choices):
//
//   - tool_name_match: 1 if the first predicted call's name equals the first
//     reference call's name, else 0.
//   - tool_parameter_key_match: |K_ref ∩ K_pred| / |K_ref|, the fraction of the
//     reference call's argument KEYS present in the predicted call's arguments.
//     Extra predicted keys are not penalized. A reference call with no arguments
//     scores 1 (nothing to match).
//   - tool_parameter_kv_match: the same fraction, counting a reference key only
//     when the predicted value is equal to the reference value after JSON
//     decoding (deep equality; numbers compare numerically, so 1 == 1.0).
//
// Edge cases common to all three: if the reference has no tool call, the score
// is 1 when the prediction also has none, else 0; if the prediction has no tool
// call (or is not valid JSON) but the reference has one, the score is 0.
func toolMetric(metric, pred, ref string) (float64, string, error) {
	refCalls, err := parseToolCalls(ref)
	if err != nil {
		return 0, "", fmt.Errorf("%s: reference is not valid tool-call JSON: %w", metric, err)
	}
	predCalls, perr := parseToolCalls(pred)
	if perr != nil {
		predCalls = nil
	}
	if len(refCalls) == 0 {
		s := boolScore(len(predCalls) == 0)
		return s, fmt.Sprintf("%s = %g (reference has no tool call)", metric, s), nil
	}
	if len(predCalls) == 0 {
		why := "response has no tool call"
		if perr != nil {
			why = "response is not valid tool-call JSON"
		}
		return 0, fmt.Sprintf("%s = 0 (%s)", metric, why), nil
	}
	r, p := refCalls[0], predCalls[0]
	// Vertex EvaluateInstances scores key/kv match 0 when the first call names
	// differ (observed 2026-09-26: wrong-tool calls with identical arguments
	// return tool_parameter_kv_match = 0). Mirror that so local == Vertex.
	if metric != "tool_name_match" && !(r.nameOK && p.nameOK && r.name == p.name) {
		return 0, fmt.Sprintf("%s = 0 (tool name %q does not match reference %q)", metric, p.name, r.name), nil
	}
	switch metric {
	case "tool_name_match":
		s := boolScore(r.nameOK && p.nameOK && r.name == p.name)
		return s, fmt.Sprintf("tool_name_match = %g (predicted %q, reference %q)", s, p.name, r.name), nil
	default:
		if len(r.args) == 0 {
			return 1, fmt.Sprintf("%s = 1 (reference call has no arguments)", metric), nil
		}
		matched := 0
		for k, rv := range r.args {
			pv, ok := p.args[k]
			if !ok {
				continue
			}
			if metric == "tool_parameter_key_match" || reflect.DeepEqual(pv, rv) {
				matched++
			}
		}
		s := float64(matched) / float64(len(r.args))
		return s, fmt.Sprintf("%s = %.4f (%d of %d reference argument(s) matched)", metric, s, matched, len(r.args)), nil
	}
}

// =============================================================================
// Trajectory metrics
// =============================================================================

// trajectoryCall is one {"tool_name", "tool_input"} step. key is the comparison
// identity: name plus the canonicalized input JSON.
type trajectoryCall struct {
	name  string
	input string // canonical JSON of tool_input
	key   string
}

// canonicalToolInput canonicalizes a tool_input value: an object/array/scalar is
// re-marshaled with sorted keys; a STRING is first tried as embedded JSON (the
// Vertex ToolCall.tool_input field is itself a JSON string) and canonicalized if
// it parses, else kept as the literal string.
func canonicalToolInput(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	if s, ok := v.(string); ok {
		var inner any
		if json.Unmarshal([]byte(s), &inner) == nil {
			v = inner
		}
	}
	b, err := json.Marshal(v) // encoding/json sorts map keys at every level
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseTrajectory parses a JSON list of {"tool_name": "...", "tool_input": {...}}.
func parseTrajectory(s string) ([]trajectoryCall, error) {
	var steps []struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal([]byte(s), &steps); err != nil {
		return nil, err
	}
	out := make([]trajectoryCall, 0, len(steps))
	for i, st := range steps {
		in, err := canonicalToolInput(st.ToolInput)
		if err != nil {
			return nil, fmt.Errorf("step %d tool_input: %w", i, err)
		}
		out = append(out, trajectoryCall{name: st.ToolName, input: in, key: st.ToolName + "\x00" + in})
	}
	return out, nil
}

// trajectoryMetric implements the trajectory_* family. Calls are compared by
// tool name plus canonicalized input JSON. Definitions:
//
//   - exact_match:     1 if the predicted and reference sequences are identical.
//   - in_order_match:  1 if the reference is a subsequence of the prediction
//     (all reference calls appear in order; extra predicted calls allowed).
//   - any_order_match: 1 if every reference call appears in the prediction in
//     any order (multiset containment: a reference call repeated k times needs k
//     predicted matches).
//   - precision:       matched predicted calls / predicted calls.
//   - recall:          matched reference calls / reference calls.
//     "Matched" is multiset intersection size: sum over distinct calls of
//     min(count_pred, count_ref). An empty prediction has precision 1 only when
//     the reference is empty too (else 0); an empty reference has recall 1.
//   - single_tool_use: 1 if spec.native.toolName appears (by name) anywhere in
//     the prediction.
//
// A prediction that is not a valid trajectory scores 0; an invalid reference is
// an error (gold data).
func trajectoryMetric(spec *registry.NativeMetricSpec, pred, ref string) (float64, string, error) {
	metric := spec.Metric
	p, perr := parseTrajectory(pred)
	if perr != nil {
		return 0, fmt.Sprintf("%s = 0 (response is not a valid trajectory: %v)", metric, perr), nil
	}
	if metric == "trajectory_single_tool_use" {
		for _, c := range p {
			if c.name == spec.ToolName {
				return 1, fmt.Sprintf("trajectory_single_tool_use = 1 (%q was called)", spec.ToolName), nil
			}
		}
		return 0, fmt.Sprintf("trajectory_single_tool_use = 0 (%q was never called)", spec.ToolName), nil
	}
	r, err := parseTrajectory(ref)
	if err != nil {
		return 0, "", fmt.Errorf("%s: reference is not a valid trajectory: %w", metric, err)
	}
	var s float64
	switch metric {
	case "trajectory_exact_match":
		eq := len(p) == len(r)
		for i := 0; eq && i < len(p); i++ {
			eq = p[i].key == r[i].key
		}
		s = boolScore(eq)
	case "trajectory_in_order_match":
		j := 0
		for i := 0; i < len(p) && j < len(r); i++ {
			if p[i].key == r[j].key {
				j++
			}
		}
		s = boolScore(j == len(r))
	case "trajectory_any_order_match":
		s = boolScore(multisetIntersection(p, r) == len(r))
	case "trajectory_precision":
		switch {
		case len(p) == 0 && len(r) == 0:
			s = 1
		case len(p) == 0:
			s = 0
		default:
			s = float64(multisetIntersection(p, r)) / float64(len(p))
		}
	case "trajectory_recall":
		if len(r) == 0 {
			s = 1
		} else {
			s = float64(multisetIntersection(p, r)) / float64(len(r))
		}
	default:
		return 0, "", fmt.Errorf("unknown trajectory metric %q", metric)
	}
	return s, fmt.Sprintf("%s = %.4f (%d predicted call(s), %d reference call(s))", metric, s, len(p), len(r)), nil
}

// multisetIntersection returns sum over distinct call keys of min(count in a,
// count in b).
func multisetIntersection(a, b []trajectoryCall) int {
	ca := map[string]int{}
	for _, c := range a {
		ca[c.key]++
	}
	n := 0
	for _, c := range b {
		if ca[c.key] > 0 {
			ca[c.key]--
			n++
		}
	}
	return n
}

// =============================================================================
// Vertex EvaluateInstances path (--engine vertex)
// =============================================================================

// toProtoTrajectory converts a JSON trajectory string into the Vertex proto,
// sending each tool_input as its canonical JSON string.
func toProtoTrajectory(s string) (*aiplatformpb.Trajectory, error) {
	calls, err := parseTrajectory(s)
	if err != nil {
		return nil, err
	}
	t := &aiplatformpb.Trajectory{}
	for _, c := range calls {
		t.ToolCalls = append(t.ToolCalls, &aiplatformpb.ToolCall{ToolName: proto.String(c.name), ToolInput: proto.String(c.input)})
	}
	return t, nil
}

// buildComputationRequest materializes the EvaluateInstances oneof input for a
// computation metric at the given location. There is NO AutoraterConfig:
// computation metrics involve no model.
func buildComputationRequest(location string, spec *registry.NativeMetricSpec, f map[string]string) (*aiplatformpb.EvaluateInstancesRequest, error) {
	pred, ref := f[registry.NativeRoleResponse], f[registry.NativeRoleReference]
	_, hasRef := f[registry.NativeRoleReference]
	optRef := func() *string {
		if hasRef {
			return proto.String(ref)
		}
		// Vertex rejects tool_call_valid without a reference ("Required field is
		// not set") even though validity ignores it; send an empty tool-call
		// envelope as a placeholder.
		return proto.String(`{"content": "", "tool_calls": []}`)
	}
	req := &aiplatformpb.EvaluateInstancesRequest{Location: location}
	switch spec.Metric {
	case "exact_match":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_ExactMatchInput{ExactMatchInput: &aiplatformpb.ExactMatchInput{
			MetricSpec: &aiplatformpb.ExactMatchSpec{},
			Instances:  []*aiplatformpb.ExactMatchInstance{{Prediction: proto.String(pred), Reference: proto.String(ref)}},
		}}
	case "bleu":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_BleuInput{BleuInput: &aiplatformpb.BleuInput{
			MetricSpec: &aiplatformpb.BleuSpec{UseEffectiveOrder: true},
			Instances:  []*aiplatformpb.BleuInstance{{Prediction: proto.String(pred), Reference: proto.String(ref)}},
		}}
	case "rouge":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_RougeInput{RougeInput: &aiplatformpb.RougeInput{
			// SplitSummaries=false: rougeLsum splits on newlines, like the local engine.
			MetricSpec: &aiplatformpb.RougeSpec{RougeType: spec.EffectiveRougeType(), UseStemmer: spec.UseStemmer, SplitSummaries: false},
			Instances:  []*aiplatformpb.RougeInstance{{Prediction: proto.String(pred), Reference: proto.String(ref)}},
		}}
	case "tool_call_valid":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_ToolCallValidInput{ToolCallValidInput: &aiplatformpb.ToolCallValidInput{
			MetricSpec: &aiplatformpb.ToolCallValidSpec{},
			Instances:  []*aiplatformpb.ToolCallValidInstance{{Prediction: proto.String(pred), Reference: optRef()}},
		}}
	case "tool_name_match":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_ToolNameMatchInput{ToolNameMatchInput: &aiplatformpb.ToolNameMatchInput{
			MetricSpec: &aiplatformpb.ToolNameMatchSpec{},
			Instances:  []*aiplatformpb.ToolNameMatchInstance{{Prediction: proto.String(pred), Reference: proto.String(ref)}},
		}}
	case "tool_parameter_key_match":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_ToolParameterKeyMatchInput{ToolParameterKeyMatchInput: &aiplatformpb.ToolParameterKeyMatchInput{
			MetricSpec: &aiplatformpb.ToolParameterKeyMatchSpec{},
			Instances:  []*aiplatformpb.ToolParameterKeyMatchInstance{{Prediction: proto.String(pred), Reference: proto.String(ref)}},
		}}
	case "tool_parameter_kv_match":
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_ToolParameterKvMatchInput{ToolParameterKvMatchInput: &aiplatformpb.ToolParameterKVMatchInput{
			MetricSpec: &aiplatformpb.ToolParameterKVMatchSpec{},
			Instances:  []*aiplatformpb.ToolParameterKVMatchInstance{{Prediction: proto.String(pred), Reference: proto.String(ref)}},
		}}
	case "trajectory_single_tool_use":
		pt, err := toProtoTrajectory(pred)
		if err != nil {
			return nil, fmt.Errorf("response is not a valid trajectory: %w", err)
		}
		req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_TrajectorySingleToolUseInput{TrajectorySingleToolUseInput: &aiplatformpb.TrajectorySingleToolUseInput{
			MetricSpec: &aiplatformpb.TrajectorySingleToolUseSpec{ToolName: proto.String(spec.ToolName)},
			Instances:  []*aiplatformpb.TrajectorySingleToolUseInstance{{PredictedTrajectory: pt}},
		}}
	case "trajectory_exact_match", "trajectory_in_order_match", "trajectory_any_order_match", "trajectory_precision", "trajectory_recall":
		pt, err := toProtoTrajectory(pred)
		if err != nil {
			return nil, fmt.Errorf("response is not a valid trajectory: %w", err)
		}
		rt, err := toProtoTrajectory(ref)
		if err != nil {
			return nil, fmt.Errorf("reference is not a valid trajectory: %w", err)
		}
		switch spec.Metric {
		case "trajectory_exact_match":
			req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_TrajectoryExactMatchInput{TrajectoryExactMatchInput: &aiplatformpb.TrajectoryExactMatchInput{
				MetricSpec: &aiplatformpb.TrajectoryExactMatchSpec{},
				Instances:  []*aiplatformpb.TrajectoryExactMatchInstance{{PredictedTrajectory: pt, ReferenceTrajectory: rt}},
			}}
		case "trajectory_in_order_match":
			req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_TrajectoryInOrderMatchInput{TrajectoryInOrderMatchInput: &aiplatformpb.TrajectoryInOrderMatchInput{
				MetricSpec: &aiplatformpb.TrajectoryInOrderMatchSpec{},
				Instances:  []*aiplatformpb.TrajectoryInOrderMatchInstance{{PredictedTrajectory: pt, ReferenceTrajectory: rt}},
			}}
		case "trajectory_any_order_match":
			req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_TrajectoryAnyOrderMatchInput{TrajectoryAnyOrderMatchInput: &aiplatformpb.TrajectoryAnyOrderMatchInput{
				MetricSpec: &aiplatformpb.TrajectoryAnyOrderMatchSpec{},
				Instances:  []*aiplatformpb.TrajectoryAnyOrderMatchInstance{{PredictedTrajectory: pt, ReferenceTrajectory: rt}},
			}}
		case "trajectory_precision":
			req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_TrajectoryPrecisionInput{TrajectoryPrecisionInput: &aiplatformpb.TrajectoryPrecisionInput{
				MetricSpec: &aiplatformpb.TrajectoryPrecisionSpec{},
				Instances:  []*aiplatformpb.TrajectoryPrecisionInstance{{PredictedTrajectory: pt, ReferenceTrajectory: rt}},
			}}
		case "trajectory_recall":
			req.MetricInputs = &aiplatformpb.EvaluateInstancesRequest_TrajectoryRecallInput{TrajectoryRecallInput: &aiplatformpb.TrajectoryRecallInput{
				MetricSpec: &aiplatformpb.TrajectoryRecallSpec{},
				Instances:  []*aiplatformpb.TrajectoryRecallInstance{{PredictedTrajectory: pt, ReferenceTrajectory: rt}},
			}}
		}
	default:
		return nil, fmt.Errorf("unknown computation metric %q", spec.Metric)
	}
	return req, nil
}

// firstScore returns the score of the first metric value in a repeated
// *_metric_values list (EvaluateInstances returns one value per instance, and
// mizan sends exactly one instance).
func firstScore[T interface{ GetScore() float32 }](vals []T, has func(T) bool) (*float32, bool) {
	if len(vals) == 0 || !has(vals[0]) {
		return nil, false
	}
	s := vals[0].GetScore()
	return &s, true
}

// computationVertexScore maps the EvaluateInstances response for a computation
// metric onto a single score, e.g. ExactMatchResults.ExactMatchMetricValues[0].Score.
func computationVertexScore(metric string, resp *aiplatformpb.EvaluateInstancesResponse) (*float32, error) {
	var (
		s  *float32
		ok bool
	)
	switch metric {
	case "exact_match":
		s, ok = firstScore(resp.GetExactMatchResults().GetExactMatchMetricValues(), func(v *aiplatformpb.ExactMatchMetricValue) bool { return v.Score != nil })
	case "bleu":
		s, ok = firstScore(resp.GetBleuResults().GetBleuMetricValues(), func(v *aiplatformpb.BleuMetricValue) bool { return v.Score != nil })
	case "rouge":
		s, ok = firstScore(resp.GetRougeResults().GetRougeMetricValues(), func(v *aiplatformpb.RougeMetricValue) bool { return v.Score != nil })
	case "tool_call_valid":
		s, ok = firstScore(resp.GetToolCallValidResults().GetToolCallValidMetricValues(), func(v *aiplatformpb.ToolCallValidMetricValue) bool { return v.Score != nil })
	case "tool_name_match":
		s, ok = firstScore(resp.GetToolNameMatchResults().GetToolNameMatchMetricValues(), func(v *aiplatformpb.ToolNameMatchMetricValue) bool { return v.Score != nil })
	case "tool_parameter_key_match":
		s, ok = firstScore(resp.GetToolParameterKeyMatchResults().GetToolParameterKeyMatchMetricValues(), func(v *aiplatformpb.ToolParameterKeyMatchMetricValue) bool { return v.Score != nil })
	case "tool_parameter_kv_match":
		s, ok = firstScore(resp.GetToolParameterKvMatchResults().GetToolParameterKvMatchMetricValues(), func(v *aiplatformpb.ToolParameterKVMatchMetricValue) bool { return v.Score != nil })
	case "trajectory_exact_match":
		s, ok = firstScore(resp.GetTrajectoryExactMatchResults().GetTrajectoryExactMatchMetricValues(), func(v *aiplatformpb.TrajectoryExactMatchMetricValue) bool { return v.Score != nil })
	case "trajectory_in_order_match":
		s, ok = firstScore(resp.GetTrajectoryInOrderMatchResults().GetTrajectoryInOrderMatchMetricValues(), func(v *aiplatformpb.TrajectoryInOrderMatchMetricValue) bool { return v.Score != nil })
	case "trajectory_any_order_match":
		s, ok = firstScore(resp.GetTrajectoryAnyOrderMatchResults().GetTrajectoryAnyOrderMatchMetricValues(), func(v *aiplatformpb.TrajectoryAnyOrderMatchMetricValue) bool { return v.Score != nil })
	case "trajectory_precision":
		s, ok = firstScore(resp.GetTrajectoryPrecisionResults().GetTrajectoryPrecisionMetricValues(), func(v *aiplatformpb.TrajectoryPrecisionMetricValue) bool { return v.Score != nil })
	case "trajectory_recall":
		s, ok = firstScore(resp.GetTrajectoryRecallResults().GetTrajectoryRecallMetricValues(), func(v *aiplatformpb.TrajectoryRecallMetricValue) bool { return v.Score != nil })
	case "trajectory_single_tool_use":
		s, ok = firstScore(resp.GetTrajectorySingleToolUseResults().GetTrajectorySingleToolUseMetricValues(), func(v *aiplatformpb.TrajectorySingleToolUseMetricValue) bool { return v.Score != nil })
	default:
		return nil, fmt.Errorf("unknown computation metric %q", metric)
	}
	if !ok {
		return nil, fmt.Errorf("eval: EvaluateInstances response contained no %s score", metric)
	}
	return s, nil
}

// runComputationVertex runs a computation metric through Vertex EvaluateInstances
// on the REGIONAL client. No autorater is sent or validated (there is no model),
// so no global-host routing applies either.
func (e *Engine) runComputationVertex(ctx context.Context, tmpl registry.MetricTemplate, inst Instance) (Result, error) {
	if e.client == nil {
		return Result{}, fmt.Errorf("eval: no evaluation client configured (--engine vertex needs Vertex credentials; omit --engine to run %q locally)", tmpl.Native.Metric)
	}
	fields, err := nativeTextFields(tmpl, inst)
	if err != nil {
		return Result{}, err
	}
	loc := fmt.Sprintf("projects/%s/locations/%s", e.projectID, e.location)
	req, err := buildComputationRequest(loc, tmpl.Native, fields)
	if err != nil {
		return Result{}, fmt.Errorf("eval: template %q: %w", tmpl.ID, err)
	}
	resp, err := e.client.EvaluateInstances(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("eval: EvaluateInstances (%s): %w", tmpl.Native.Metric, err)
	}
	score, err := computationVertexScore(tmpl.Native.Metric, resp)
	if err != nil {
		return Result{}, err
	}
	res := Result{
		Score:        score,
		Explanation:  fmt.Sprintf("%s = %.4f (Vertex EvaluateInstances)", tmpl.Native.Metric, *score),
		CustomOutput: map[string]any{"metric": tmpl.Native.Metric, "engine": engineVertex},
	}
	applyPassThreshold(&res, tmpl.Native)
	return res, nil
}

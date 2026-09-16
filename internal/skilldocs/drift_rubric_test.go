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

package skilldocs

// This is the contract gate for the rubric-generate-from-brand-book skill
// (plugins/mizan-authoring), the Phase-4 sibling of drift_test.go (eval.Result
// json key-set) and drift_pack_test.go (pack validate text/exit-code). The skill
// documents TWO shapes that no existing drift test covers; both gates here are
// HERMETIC (no network/ADC — `rubric generate` and `eval adaptive` need a live
// LLM call and are NOT invoked here). Instead each gate renders the REAL writer /
// encoder over constructed structs and diffs the documented shape against it:
//
//  1. DRAFT YAML rubricProvenance — `mizan rubric generate --out` writes a draft
//     KindRubric template via registry.MarshalTemplate, carrying a
//     spec.rubricProvenance block (registry.RubricProvenance / RubricMeta). This
//     gate marshals a fully-populated template through the SAME MarshalTemplate
//     the CLI uses and asserts the yaml key-set under spec.rubricProvenance equals
//     the block the SKILL.md documents, AND that every non-reserved struct field
//     is documented (completeness).
//  2. registry import -o json — the FREEZE step documents the
//     registry.ImportReport shape. `registry import -o json` renders it via
//     cmd/mizan.printJSON (a thin json.Encoder(SetIndent) wrapper); this gate
//     replicates that encoder over a fully-populated ImportReport and asserts the
//     documented json key-set equals it (+ completeness over the real struct).
//
// The eval adaptive `-o json` path emits eval.Result, which is ALREADY covered by
// drift_test.go's eval.Result gate — it is referenced by the skill and NOT
// duplicated here. Each gate includes a synthetic-drift self-test proving it
// discriminates (an added/removed/renamed key FAILS).
//
// Reused package-level helpers from the sibling drift tests: diff, sortedKeys
// (drift_test.go) and repoFile (drift_pack_test.go).

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/ghchinoy/mizan/internal/registry"
)

// rubricSkillRelPath is the rubric-generate-from-brand-book SKILL.md relative to
// this test file (<repo>/internal/skilldocs, so "../.." is the repo root).
var rubricSkillRelPath = "../../plugins/mizan-authoring/skills/rubric-generate-from-brand-book/SKILL.md"

// helpersCmdRelPath is cmd/mizan/helpers.go — the source of truth for the
// `registry import -o json` render path (renderImportReport -> printJSON).
var helpersCmdRelPath = "../../cmd/mizan/helpers.go"

// Drift markers precede the fenced blocks that document each shape in SKILL.md.
const (
	provenanceMarker   = "<!-- drift:rubricProvenance -->"
	importReportMarker = "<!-- drift:registry-import -o json -->"
)

// fenceReYAML matches a fenced ```yaml block (indented fences allowed). It is a
// distinct name from drift_test.go's json-only fenceRe.
var fenceReYAML = regexp.MustCompile("(?ms)^[ \t]*```yaml[ \t]*\r?\n(.*?)^[ \t]*```")

// collectKeysDeep walks a decoded YAML/JSON value and returns the set of
// path-anchored keys. Unlike drift_test.go's collectKeys it also descends into
// slices (each element under the same prefix), so list-of-object shapes like
// rubricProvenance.rubricMeta[] expose their element keys. Scalars are leaves.
func collectKeysDeep(prefix string, v any, out map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out[path] = true
			collectKeysDeep(path, val, out)
		}
	case map[any]any: // yaml.v3 can decode nested maps as map[any]any
		for k, val := range t {
			ks, ok := k.(string)
			if !ok {
				continue
			}
			path := ks
			if prefix != "" {
				path = prefix + "." + ks
			}
			out[path] = true
			collectKeysDeep(path, val, out)
		}
	case []any:
		for _, el := range t {
			collectKeysDeep(prefix, el, out)
		}
	}
}

// yamlSubtreeKeys unmarshals a YAML document and returns the path-anchored key
// set of the value at spec.<child> (e.g. spec.rubricProvenance), with paths
// re-based to that subtree (the "spec.<child>." prefix stripped).
func yamlSubtreeKeys(t *testing.T, doc []byte, child string) map[string]bool {
	t.Helper()
	var root map[string]any
	if err := yaml.Unmarshal(doc, &root); err != nil {
		t.Fatalf("unmarshal yaml: %v\n%s", err, doc)
	}
	spec, ok := root["spec"].(map[string]any)
	if !ok {
		t.Fatalf("yaml has no spec object; got %T", root["spec"])
	}
	sub, ok := spec[child]
	if !ok {
		t.Fatalf("yaml spec has no %q key; keys present: %v", child, spec)
	}
	out := map[string]bool{}
	collectKeysDeep("", sub, out)
	return out
}

// documentedYAMLBlock extracts the fenced ```yaml block that follows marker in
// SKILL.md and returns its raw bytes.
func documentedYAMLBlock(t *testing.T, marker string) []byte {
	t.Helper()
	text := repoFile(t, rubricSkillRelPath)
	mi := strings.Index(text, marker)
	if mi < 0 {
		t.Fatalf("SKILL.md is missing the %q marker", marker)
	}
	m := fenceReYAML.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```yaml block found after the %q marker", marker)
	}
	return []byte(m[1])
}

// documentedJSONBlock extracts the fenced ```json block that follows marker in
// SKILL.md and returns the parsed object. (fenceRe is defined in drift_test.go.)
func documentedJSONBlock(t *testing.T, marker string) map[string]any {
	t.Helper()
	text := repoFile(t, rubricSkillRelPath)
	mi := strings.Index(text, marker)
	if mi < 0 {
		t.Fatalf("SKILL.md is missing the %q marker", marker)
	}
	m := fenceRe.FindStringSubmatch(text[mi:])
	if m == nil {
		t.Fatalf("no fenced ```json block found after the %q marker", marker)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("documented json block after %q is invalid JSON: %v\nblock:\n%s", marker, err, m[1])
	}
	return doc
}

// yamlStructKeys reflects over a struct type and returns the path-anchored set of
// yaml key names it declares, honoring `yaml:"-"` and inline `,omitempty`, and
// descending into nested structs and slices of structs. reservedYAML fields
// (path -> true) are recorded but NOT descended and are returned separately by
// the caller's skip set. It is used to prove the documented block is COMPLETE.
func yamlStructKeys(prefix string, tp reflect.Type, reserved map[string]bool, out map[string]bool) {
	for tp.Kind() == reflect.Pointer {
		tp = tp.Elem()
	}
	if tp.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("yaml"); ok {
			first := strings.Split(tag, ",")[0]
			if first == "-" {
				continue
			}
			if first != "" {
				name = first
			}
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if reserved[path] {
			continue
		}
		out[path] = true
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && !isYAMLScalarStruct(ft) {
			yamlStructKeys(path, ft, reserved, out)
		}
	}
}

// isYAMLScalarStruct returns true for structs that marshal as a scalar (time.Time),
// so the reflector treats them as leaves rather than descending into their fields.
func isYAMLScalarStruct(tp reflect.Type) bool {
	return tp == reflect.TypeOf(time.Time{})
}

// representativeProvenanceTemplate builds a KindRubric MetricTemplate with a
// fully-populated RubricProvenance (every non-reserved field, including one
// RubricMeta entry with all fields) so MarshalTemplate emits every provenance key
// the draft writer can produce. PromptTemplate is left empty on purpose: it is
// reserved/never populated by the CLI (omitempty), so it must NOT appear.
func representativeProvenanceTemplate() registry.MetricTemplate {
	return registry.MetricTemplate{
		ID:                   "acme/brand-rubric",
		Name:                 "Adaptive rubric: brand",
		Description:          "Adaptive-generated rubric (authoring aid).",
		Version:              "0.1.0",
		Kind:                 registry.KindRubric,
		Modalities:           []registry.Modality{registry.ModalityText},
		Inputs:               []registry.InputSpec{{Name: "response", Modality: registry.ModalityText, Required: true}},
		MetricPromptTemplate: "Evaluate: {{response}}",
		RubricGroups:         map[string][]string{"brand": {"The response uses approved brand terminology."}},
		RubricProvenance: &registry.RubricProvenance{
			Method:         registry.OriginAdaptiveGenerated,
			GeneratorModel: "gemini-2.5-flash",
			Recipe:         "general_quality_v1",
			SampleInputRef: `inline:"sample" sha256:abc`,
			GeneratedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			APIVersion:     "v1beta1:generateInstanceRubrics",
			RubricMeta: []registry.RubricMeta{{
				Group:      "brand",
				Criterion:  "The response uses approved brand terminology.",
				Type:       "STICKY",
				Importance: "HIGH",
				Origin:     registry.OriginAdaptiveGenerated,
			}},
		},
	}
}

// TestRubricProvenanceDriftKeySetEquality is the primary draft-YAML gate: the
// spec.rubricProvenance key set the SKILL.md documents must EQUAL the key set the
// real MarshalTemplate writer emits from a fully-populated template. A field
// added/renamed/retagged on RubricProvenance or RubricMeta changes the emitted
// set and fails here until the skill is updated.
func TestRubricProvenanceDriftKeySetEquality(t *testing.T) {
	tmpl := representativeProvenanceTemplate()
	raw, err := registry.MarshalTemplate(&tmpl)
	if err != nil {
		t.Fatalf("MarshalTemplate: %v", err)
	}
	live := yamlSubtreeKeys(t, raw, "rubricProvenance")

	docYAML := documentedYAMLBlock(t, provenanceMarker)
	documented := yamlSubtreeKeys(t, docYAML, "rubricProvenance")

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md rubricProvenance block is MISSING keys the writer emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md rubricProvenance block documents keys the writer does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live rubricProvenance key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestRubricProvenanceDriftStructCompleteness proves the documented block covers
// EVERY declared (non-reserved) RubricProvenance/RubricMeta yaml field, including
// omitempty fields a rendered sample might not exercise. PromptTemplate is the one
// reserved field (never populated by the CLI) and is excluded.
func TestRubricProvenanceDriftStructCompleteness(t *testing.T) {
	reserved := map[string]bool{"promptTemplate": true}
	declared := map[string]bool{}
	yamlStructKeys("", reflect.TypeOf(registry.RubricProvenance{}), reserved, declared)

	docYAML := documentedYAMLBlock(t, provenanceMarker)
	documented := yamlSubtreeKeys(t, docYAML, "rubricProvenance")

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("RubricProvenance declares yaml fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// TestRubricProvenanceReservedFieldNeverEmitted guards the specific invariant that
// promptTemplate is reserved (omitempty, never populated by the CLI): it must be
// absent from the real writer output AND undocumented, so the skill never tells an
// agent to read it.
func TestRubricProvenanceReservedFieldNeverEmitted(t *testing.T) {
	tmpl := representativeProvenanceTemplate()
	raw, err := registry.MarshalTemplate(&tmpl)
	if err != nil {
		t.Fatalf("MarshalTemplate: %v", err)
	}
	if strings.Contains(string(raw), "promptTemplate") {
		t.Error("MarshalTemplate emitted 'promptTemplate'; it is reserved and must stay omitempty/unpopulated")
	}
	if strings.Contains(repoFile(t, rubricSkillRelPath), "promptTemplate") {
		t.Error("SKILL.md documents 'promptTemplate'; it is reserved and never emitted by the draft writer")
	}
}

// replicateImportReportJSON renders r exactly as the CLI's `registry import -o
// json` path does. renderImportReport calls cmd/mizan.printJSON, a thin
// json.Encoder(SetIndent) wrapper, so encoding/json here produces the identical
// KEY SET the CLI emits (the same replication discipline drift_test.go uses for
// the eval.Result encoder). cmd/mizan is package main and cannot be imported.
func replicateImportReportJSON(t *testing.T, r registry.ImportReport) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		t.Fatalf("encode ImportReport: %v", err)
	}
	var live map[string]any
	if err := json.Unmarshal(buf.Bytes(), &live); err != nil {
		t.Fatalf("re-parse rendered ImportReport json: %v", err)
	}
	return live
}

// representativeImportReport populates every field of ImportReport (and its nested
// SourceInfo + one ImportEntry) so the rendered json exposes all keys.
func representativeImportReport() registry.ImportReport {
	return registry.ImportReport{
		Source:     registry.SourceInfo{Type: "local", Origin: "packs/acme"},
		Strategy:   registry.StrategyNewer,
		DryRun:     false,
		Inserted:   1,
		Updated:    0,
		Skipped:    0,
		Conflicted: 0,
		Unchanged:  0,
		Forked:     0,
		Entries: []registry.ImportEntry{{
			ID:     "acme/brand-rubric",
			Action: registry.ActionInserted,
			Reason: "",
		}},
	}
}

// TestImportReportDriftKeySetEquality is the freeze-step gate: the
// registry.ImportReport json key set documented in SKILL.md must EQUAL the key set
// the CLI's `registry import -o json` path renders. A field added/renamed on
// ImportReport/ImportEntry/SourceInfo changes the rendered set and fails here.
func TestImportReportDriftKeySetEquality(t *testing.T) {
	live := map[string]bool{}
	collectKeysDeep("", replicateImportReportJSON(t, representativeImportReport()), live)

	documented := map[string]bool{}
	collectKeysDeep("", documentedJSONBlock(t, importReportMarker), documented)

	if missing := diff(live, documented); len(missing) > 0 {
		t.Errorf("SKILL.md registry-import block is MISSING keys the CLI emits (drift): %v", missing)
	}
	if extra := diff(documented, live); len(extra) > 0 {
		t.Errorf("SKILL.md registry-import block documents keys the CLI does NOT emit (drift): %v", extra)
	}
	if !reflect.DeepEqual(sortedKeys(documented), sortedKeys(live)) {
		t.Errorf("documented vs live ImportReport key-set mismatch:\n documented: %v\n live:       %v",
			sortedKeys(documented), sortedKeys(live))
	}
}

// TestImportReportDriftStructCompleteness proves the documented block covers EVERY
// exported ImportReport field (recursing into SourceInfo and ImportEntry). No json
// tags are declared, so the keys are the Go field names.
func TestImportReportDriftStructCompleteness(t *testing.T) {
	declared := map[string]bool{}
	jsonStructKeys("", reflect.TypeOf(registry.ImportReport{}), declared)

	documented := map[string]bool{}
	collectKeysDeep("", documentedJSONBlock(t, importReportMarker), documented)

	if missing := diff(declared, documented); len(missing) > 0 {
		t.Errorf("ImportReport declares json fields NOT documented in SKILL.md (drift): %v\n"+
			"declared: %v\ndocumented: %v", missing, sortedKeys(declared), sortedKeys(documented))
	}
}

// jsonStructKeys reflects over a struct type and returns the path-anchored set of
// json key names it declares (Go field names when untagged), descending into
// nested structs and slices of structs. Used for ImportReport completeness.
func jsonStructKeys(prefix string, tp reflect.Type, out map[string]bool) {
	for tp.Kind() == reflect.Pointer {
		tp = tp.Elem()
	}
	if tp.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			first := strings.Split(tag, ",")[0]
			if first == "-" {
				continue
			}
			if first != "" {
				name = first
			}
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out[path] = true
		ft := f.Type
		for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			jsonStructKeys(path, ft, out)
		}
	}
}

// TestRubricDriftSelfTest is the synthetic-drift self-test: it proves BOTH gates
// actually discriminate, so a real drift (add/remove/rename a key) could not slip
// through. It mutates copies of the documented key-sets and asserts the equality
// check would then fail — without touching the SKILL.md on disk.
func TestRubricDriftSelfTest(t *testing.T) {
	// --- provenance gate discriminates ---
	tmpl := representativeProvenanceTemplate()
	raw, err := registry.MarshalTemplate(&tmpl)
	if err != nil {
		t.Fatalf("MarshalTemplate: %v", err)
	}
	provLive := yamlSubtreeKeys(t, raw, "rubricProvenance")
	provDoc := yamlSubtreeKeys(t, documentedYAMLBlock(t, provenanceMarker), "rubricProvenance")
	// Sanity: the real writer must emit the cross-team contract key `method` and
	// the per-criterion `rubricMeta.origin` (proves the fixture is meaningful).
	for _, k := range []string{"method", "rubricMeta.origin"} {
		if !provLive[k] {
			t.Fatalf("self-test: expected the writer to emit rubricProvenance.%s; got %v", k, sortedKeys(provLive))
		}
	}
	// ADD a fabricated key -> documented != live.
	mutated := clone(provDoc)
	mutated["fabricatedKey"] = true
	if reflect.DeepEqual(sortedKeys(mutated), sortedKeys(provLive)) {
		t.Error("self-test: adding a key did not change the provenance key-set; the gate cannot detect drift")
	}
	// REMOVE a real key -> documented != live.
	mutated = clone(provDoc)
	delete(mutated, "method")
	if reflect.DeepEqual(sortedKeys(mutated), sortedKeys(provLive)) {
		t.Error("self-test: removing 'method' did not change the provenance key-set; the gate cannot detect drift")
	}

	// --- import-report gate discriminates ---
	impLive := map[string]bool{}
	collectKeysDeep("", replicateImportReportJSON(t, representativeImportReport()), impLive)
	impDoc := map[string]bool{}
	collectKeysDeep("", documentedJSONBlock(t, importReportMarker), impDoc)
	mutated = clone(impDoc)
	mutated["Fabricated"] = true
	if reflect.DeepEqual(sortedKeys(mutated), sortedKeys(impLive)) {
		t.Error("self-test: adding a key did not change the ImportReport key-set; the gate cannot detect drift")
	}

	// A fabricated method value must NOT be in the real writer output — proving the
	// token/key assertions are not trivially satisfiable.
	if strings.Contains(string(raw), "hand-crafted-by-nobody") {
		t.Error("self-test: a fabricated provenance token unexpectedly matched the real writer output")
	}
}

// (clone — a string-set copier used by the self-test — is defined in
// drift_results_test.go in this package and reused here.)

// TestImportReportRenderPathAnchored anchors the replicated encoder to the real
// CLI: cmd/mizan/helpers.go's renderImportReport must still route `-o json`
// through printJSON over a registry.ImportReport, so the replicated encoder above
// cannot silently diverge from the real render path.
func TestImportReportRenderPathAnchored(t *testing.T) {
	src := repoFile(t, helpersCmdRelPath)
	for _, lit := range []string{
		"func renderImportReport(w io.Writer, r registry.ImportReport) error {",
		"return printJSON(w, r)",
	} {
		if !strings.Contains(src, lit) {
			t.Errorf("cmd/mizan/helpers.go no longer contains %q; the `registry import -o json` render path may have drifted from the replicated encoder", lit)
		}
	}
}

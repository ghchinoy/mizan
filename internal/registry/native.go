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

package registry

import (
	"fmt"
	"sort"
	"strings"
)

// native.go owns the registry side of the two Vertex-native metric kinds,
// KindComputation and KindPrebuilt: the persisted NativeMetricSpec, the table of
// supported metric ids and the instance roles each one reads, and the single
// validation routine that `pack validate`, the ingest codec's callers, and the
// CLI all share. The eval engine (internal/eval) reads the SAME table via
// ResolveNativeFields so the fields validated at authoring time are exactly the
// fields the engine will read at run time — there is no second copy to drift.

// NativeMetricSpec configures kind: computation and kind: prebuilt (YAML key
// spec.native). A nil *NativeMetricSpec on a MetricTemplate means the template is
// neither kind — existing templates are unaffected (no migration, no behavior
// change), mirroring the HeuristicSpec precedent. It is persisted, schema-
// validated, and INCLUDED in the content hash: editing the metric id, a field
// mapping, or a threshold shifts the hash, which is what the exact-version anchor
// (TemplateRef.ContentHash) needs for auditable results.
//
// The *Field members name spec.inputs entries (NOT literal values): at eval time
// the engine looks each one up in the instance and places its text in the Vertex
// instance slot for that role (prediction, reference, context, instruction,
// baseline_prediction) or in the DiffusionGemma state JSON.
type NativeMetricSpec struct {
	// Metric is the Vertex metric id, e.g. "bleu" or "groundedness". It must be
	// valid for the template's kind (see NativeMetricIDs).
	Metric string `yaml:"metric" json:"metric"`
	// PromptField is an alias for InstructionField (the user prompt / question a
	// response answers). Set at most one of the two.
	PromptField string `yaml:"promptField,omitempty" json:"promptField,omitempty"`
	// ResponseField is the input holding the response under evaluation (the
	// Vertex "prediction"). Default "response".
	ResponseField string `yaml:"responseField,omitempty" json:"responseField,omitempty"`
	// ReferenceField is the input holding the reference / ground truth. Default
	// "reference" for metrics that REQUIRE a reference; for metrics where Vertex
	// treats the reference as optional (summarization_*, question_answering_*),
	// it is used only when set explicitly.
	ReferenceField string `yaml:"referenceField,omitempty" json:"referenceField,omitempty"`
	// ContextField is the input holding grounding context (groundedness,
	// summarization_*, question_answering_*).
	ContextField string `yaml:"contextField,omitempty" json:"contextField,omitempty"`
	// InstructionField is the input holding the instruction / question
	// (fulfillment, summarization_*, question_answering_*).
	InstructionField string `yaml:"instructionField,omitempty" json:"instructionField,omitempty"`
	// BaselineField is the input holding the baseline response for the
	// pairwise_* prebuilt metrics (the Vertex "baseline_prediction").
	BaselineField string `yaml:"baselineField,omitempty" json:"baselineField,omitempty"`
	// RougeType selects the ROUGE variant: rouge1 | rouge2 | rougeL | rougeLsum.
	// Default rougeL. rouge only.
	RougeType string `yaml:"rougeType,omitempty" json:"rougeType,omitempty"`
	// UseStemmer is carried for Vertex RougeSpec parity but is REJECTED at
	// validation: mizan's local ROUGE does not implement the Porter stemmer, and
	// accepting the flag would make the local and Vertex engines silently
	// disagree. rouge only.
	UseStemmer bool `yaml:"useStemmer,omitempty" json:"useStemmer,omitempty"`
	// ToolName is the tool whose presence trajectory_single_tool_use checks.
	ToolName string `yaml:"toolName,omitempty" json:"toolName,omitempty"`
	// PassThreshold, when set, turns the numeric score into a verdict:
	// Result.Passed = Score >= PassThreshold. Not valid on pairwise_* metrics
	// (they return a preference, not a score).
	PassThreshold *float64 `yaml:"passThreshold,omitempty" json:"passThreshold,omitempty"`
}

// Native instance roles. These are the canonical slot names the engine binds
// spec fields to; they match the Vertex instance field names except that the
// Vertex "prediction" is called "response" here (the field name every other mizan
// kind uses) and "baseline_prediction" is "baseline".
const (
	NativeRoleResponse    = "response"
	NativeRoleReference   = "reference"
	NativeRoleContext     = "context"
	NativeRoleInstruction = "instruction"
	NativeRoleBaseline    = "baseline"
)

// Default input names for the two roles that have one.
const (
	DefaultNativeResponseField  = "response"
	DefaultNativeReferenceField = "reference"
)

// Supported ROUGE variants (NativeMetricSpec.RougeType). The Vertex RougeSpec
// accepts the same spellings.
const (
	RougeType1    = "rouge1"
	RougeType2    = "rouge2"
	RougeTypeL    = "rougeL"
	RougeTypeLsum = "rougeLsum"
)

// roleReq says whether a native metric reads a role, and whether it is required.
type roleReq int

const (
	roleNone roleReq = iota
	roleOptional
	roleRequired
)

// nativeMetricInfo describes one supported native metric: its kind and the
// instance roles it reads (response is always required and therefore implicit).
type nativeMetricInfo struct {
	kind        MetricKind
	reference   roleReq
	context     roleReq
	instruction roleReq
	baseline    roleReq
}

// nativeMetrics is the single source of truth for the supported metric ids and
// the inputs each one reads. The role requirements follow the Vertex Gen AI
// Evaluation Service input messages in aiplatform v1beta1 (e.g. GroundednessInstance
// has prediction+context; SummarizationQualityInstance has prediction, optional
// reference, context and instruction; the pairwise variants add
// baseline_prediction). Keep it in lockstep with the "metric" enum in
// schema/metrictemplate.json.
var nativeMetrics = map[string]nativeMetricInfo{
	// --- computation: no model involved -----------------------------------
	"exact_match": {kind: KindComputation, reference: roleRequired},
	"bleu":        {kind: KindComputation, reference: roleRequired},
	"rouge":       {kind: KindComputation, reference: roleRequired},
	// tool_call_valid inspects only the prediction; Vertex's instance carries an
	// optional reference which is forwarded when mapped but never scored.
	"tool_call_valid":            {kind: KindComputation, reference: roleOptional},
	"tool_name_match":            {kind: KindComputation, reference: roleRequired},
	"tool_parameter_key_match":   {kind: KindComputation, reference: roleRequired},
	"tool_parameter_kv_match":    {kind: KindComputation, reference: roleRequired},
	"trajectory_exact_match":     {kind: KindComputation, reference: roleRequired},
	"trajectory_in_order_match":  {kind: KindComputation, reference: roleRequired},
	"trajectory_any_order_match": {kind: KindComputation, reference: roleRequired},
	"trajectory_precision":       {kind: KindComputation, reference: roleRequired},
	"trajectory_recall":          {kind: KindComputation, reference: roleRequired},
	// single_tool_use reads only the predicted trajectory plus spec.native.toolName.
	"trajectory_single_tool_use": {kind: KindComputation},

	// --- prebuilt: Vertex's named judge metrics ---------------------------
	"safety":                         {kind: KindPrebuilt},
	"groundedness":                   {kind: KindPrebuilt, context: roleRequired},
	"fluency":                        {kind: KindPrebuilt},
	"coherence":                      {kind: KindPrebuilt},
	"fulfillment":                    {kind: KindPrebuilt, instruction: roleRequired},
	"summarization_quality":          {kind: KindPrebuilt, reference: roleOptional, context: roleRequired, instruction: roleRequired},
	"summarization_helpfulness":      {kind: KindPrebuilt, reference: roleOptional, context: roleRequired, instruction: roleRequired},
	"summarization_verbosity":        {kind: KindPrebuilt, reference: roleOptional, context: roleRequired, instruction: roleRequired},
	"question_answering_quality":     {kind: KindPrebuilt, reference: roleOptional, context: roleRequired, instruction: roleRequired},
	"question_answering_relevance":   {kind: KindPrebuilt, reference: roleOptional, context: roleOptional, instruction: roleRequired},
	"question_answering_helpfulness": {kind: KindPrebuilt, reference: roleOptional, context: roleOptional, instruction: roleRequired},
	"question_answering_correctness": {kind: KindPrebuilt, reference: roleOptional, context: roleOptional, instruction: roleRequired},
	"pairwise_summarization_quality": {kind: KindPrebuilt, reference: roleOptional, context: roleRequired, instruction: roleRequired, baseline: roleRequired},
	"pairwise_question_answering_quality": {kind: KindPrebuilt, reference: roleOptional, context: roleRequired, instruction: roleRequired,
		baseline: roleRequired},
}

// IsNativeKind reports whether kind is one of the two NativeMetricSpec-driven
// kinds (computation, prebuilt).
func IsNativeKind(kind MetricKind) bool {
	return kind == KindComputation || kind == KindPrebuilt
}

// ValidNativeMetric reports whether metric is a supported id for kind.
func ValidNativeMetric(kind MetricKind, metric string) bool {
	info, ok := nativeMetrics[metric]
	return ok && info.kind == kind
}

// NativeMetricIDs returns the supported metric ids for kind, sorted.
func NativeMetricIDs(kind MetricKind) []string {
	var out []string
	for id, info := range nativeMetrics {
		if info.kind == kind {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// IsPairwiseNativeMetric reports whether metric is a pairwise_* prebuilt metric
// (it returns a BASELINE/CANDIDATE/TIE preference rather than a score).
func IsPairwiseNativeMetric(metric string) bool {
	info, ok := nativeMetrics[metric]
	return ok && info.baseline != roleNone
}

// NativeFieldBinding binds one instance role to the spec.inputs field that feeds
// it (after defaults are applied).
type NativeFieldBinding struct {
	Role     string // NativeRole*
	Field    string // spec.inputs name
	Required bool   // the metric cannot run without it
}

// NativeInstruction returns the input feeding the instruction role:
// InstructionField, or its alias PromptField.
func (s *NativeMetricSpec) NativeInstruction() string {
	if s.InstructionField != "" {
		return s.InstructionField
	}
	return s.PromptField
}

// ResolveNativeFields returns the role->field bindings the metric in spec reads,
// in canonical role order (response, reference, context, instruction, baseline),
// with the response/reference defaults applied. It errors when the metric is
// unknown, a required role has no field, or a field is mapped to a role the
// metric does not read (a mapping that would silently never reach the judge).
// It is the one routine both validation and the engine use.
func ResolveNativeFields(spec *NativeMetricSpec) ([]NativeFieldBinding, error) {
	if spec == nil {
		return nil, fmt.Errorf("spec.native is required")
	}
	info, ok := nativeMetrics[spec.Metric]
	if !ok {
		return nil, fmt.Errorf("unknown native metric %q", spec.Metric)
	}
	if spec.PromptField != "" && spec.InstructionField != "" {
		return nil, fmt.Errorf("spec.native sets both promptField and instructionField; they are aliases, set only one")
	}
	resp := spec.ResponseField
	if resp == "" {
		resp = DefaultNativeResponseField
	}
	out := []NativeFieldBinding{{Role: NativeRoleResponse, Field: resp, Required: true}}

	bind := func(role, yamlKey, field, def string, req roleReq) error {
		switch req {
		case roleNone:
			if field != "" {
				return fmt.Errorf("metric %q does not read a %s; remove spec.native.%s", spec.Metric, role, yamlKey)
			}
		case roleOptional:
			if field != "" {
				out = append(out, NativeFieldBinding{Role: role, Field: field})
			}
		case roleRequired:
			if field == "" {
				field = def
			}
			if field == "" {
				return fmt.Errorf("metric %q requires spec.native.%s (the input holding the %s)", spec.Metric, yamlKey, role)
			}
			out = append(out, NativeFieldBinding{Role: role, Field: field, Required: true})
		}
		return nil
	}
	instrKey := "instructionField"
	if spec.InstructionField == "" && spec.PromptField != "" {
		instrKey = "promptField"
	}
	for _, b := range []struct {
		role, key, field, def string
		req                   roleReq
	}{
		{NativeRoleReference, "referenceField", spec.ReferenceField, DefaultNativeReferenceField, info.reference},
		{NativeRoleContext, "contextField", spec.ContextField, "", info.context},
		{NativeRoleInstruction, instrKey, spec.NativeInstruction(), "", info.instruction},
		{NativeRoleBaseline, "baselineField", spec.BaselineField, "", info.baseline},
	} {
		if err := bind(b.role, b.key, b.field, b.def, b.req); err != nil {
			return nil, err
		}
	}
	// Two roles bound to one input would feed the same text to, e.g., both the
	// response and the reference — always an authoring mistake.
	seen := map[string]string{}
	for _, b := range out {
		if prev, dup := seen[b.Field]; dup {
			return nil, fmt.Errorf("spec.native maps input %q to both the %s and the %s", b.Field, prev, b.Role)
		}
		seen[b.Field] = b.Role
	}
	return out, nil
}

// EffectiveRougeType returns the ROUGE variant a spec selects (default rougeL).
func (s *NativeMetricSpec) EffectiveRougeType() string {
	if s.RougeType == "" {
		return RougeTypeL
	}
	return s.RougeType
}

// ValidateNativeSpec checks a kind:computation / kind:prebuilt spec.native block
// and returns one human-readable message per defect (nil when valid). inputNames
// is the set of declared spec.inputs names. It is the SINGLE validation routine:
// `pack validate` (validateKindSpecific) and the CLI's create/update guard both
// call it, so the authoring-time and pack-time rules cannot diverge.
func ValidateNativeSpec(kind MetricKind, spec *NativeMetricSpec, inputNames map[string]bool) []string {
	if spec == nil {
		return []string{fmt.Sprintf("kind %q requires spec.native (with at least spec.native.metric)", kind)}
	}
	if strings.TrimSpace(spec.Metric) == "" {
		return []string{fmt.Sprintf("kind %q requires spec.native.metric (one of: %s)", kind, strings.Join(NativeMetricIDs(kind), ", "))}
	}
	if !ValidNativeMetric(kind, spec.Metric) {
		msg := fmt.Sprintf("spec.native.metric %q is not a %s metric (want one of: %s)", spec.Metric, kind, strings.Join(NativeMetricIDs(kind), ", "))
		if info, ok := nativeMetrics[spec.Metric]; ok {
			msg += fmt.Sprintf("; %q is a kind %q metric", spec.Metric, info.kind)
		}
		return []string{msg}
	}

	var errs []string
	bindings, err := ResolveNativeFields(spec)
	if err != nil {
		errs = append(errs, err.Error())
	}
	for _, b := range bindings {
		if !inputNames[b.Field] {
			errs = append(errs, fmt.Sprintf("spec.native %s field %q is not declared in spec.inputs", b.Role, b.Field))
		}
	}

	// Metric-specific options: each is valid on exactly one metric (family).
	if spec.Metric == "rouge" {
		switch spec.RougeType {
		case "", RougeType1, RougeType2, RougeTypeL, RougeTypeLsum:
		default:
			errs = append(errs, fmt.Sprintf("spec.native.rougeType %q is invalid (want one of: rouge1, rouge2, rougeL, rougeLsum)", spec.RougeType))
		}
		if spec.UseStemmer {
			errs = append(errs, "spec.native.useStemmer: true is not supported: mizan's local ROUGE does not implement the Porter stemmer, "+
				"so local and Vertex scores would silently disagree; remove useStemmer (tokens are lowercased alphanumerics)")
		}
	} else {
		if spec.RougeType != "" {
			errs = append(errs, fmt.Sprintf("spec.native.rougeType only applies to metric \"rouge\" (metric is %q)", spec.Metric))
		}
		if spec.UseStemmer {
			errs = append(errs, fmt.Sprintf("spec.native.useStemmer only applies to metric \"rouge\" (metric is %q)", spec.Metric))
		}
	}
	if spec.Metric == "trajectory_single_tool_use" {
		if strings.TrimSpace(spec.ToolName) == "" {
			errs = append(errs, "metric \"trajectory_single_tool_use\" requires spec.native.toolName")
		}
	} else if spec.ToolName != "" {
		errs = append(errs, fmt.Sprintf("spec.native.toolName only applies to metric \"trajectory_single_tool_use\" (metric is %q)", spec.Metric))
	}
	if spec.PassThreshold != nil && IsPairwiseNativeMetric(spec.Metric) {
		errs = append(errs, fmt.Sprintf("spec.native.passThreshold does not apply to %q (a pairwise metric returns a preference, not a score)", spec.Metric))
	}
	return errs
}

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

package mcpserver

// mapping.go holds the PURE, SDK-agnostic projections between the MCP tool
// structs (types.go) and the internal domain types (registry / eval). It imports
// NO go-sdk types — the containment rule (design §2.1). Keeping these functions
// free of the SDK and of service calls makes the whole mapping layer trivially
// unit-testable and keeps the tool-logic reviewable in one place.

import (
	"fmt"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// listFilter maps a ListMetricsIn to a registry.ListFilter. A zero input yields
// a zero filter (matches everything).
func listFilter(in ListMetricsIn) registry.ListFilter {
	f := registry.ListFilter{Namespace: in.Namespace, Tags: in.Tags}
	for _, m := range in.Modalities {
		f.Modalities = append(f.Modalities, registry.Modality(m))
	}
	for _, k := range in.Kinds {
		f.Kinds = append(f.Kinds, registry.MetricKind(k))
	}
	return f
}

// metricSummary projects a MetricTemplate onto the list-tool summary.
func metricSummary(t registry.MetricTemplate) MetricSummary {
	return MetricSummary{
		ID:          t.ID,
		Name:        t.Name,
		Kind:        string(t.Kind),
		Modalities:  modalityStrings(t.Modalities),
		Description: t.Description,
		Tags:        t.Tags,
		Version:     t.Version,
	}
}

// metricDetail projects a MetricTemplate onto the get-tool full schema.
func metricDetail(t registry.MetricTemplate) GetMetricOut {
	out := GetMetricOut{
		ID:                   t.ID,
		Name:                 t.Name,
		Kind:                 string(t.Kind),
		Modalities:           modalityStrings(t.Modalities),
		Description:          t.Description,
		Version:              t.Version,
		Choices:              t.Choices,
		BaselineFieldName:    t.BaselineFieldName,
		CandidateFieldName:   t.CandidateFieldName,
		AutoraterModel:       t.AutoraterModel,
		SamplingCount:        t.SamplingCount,
		FlipEnabled:          t.FlipEnabled,
		MetricPromptTemplate: t.MetricPromptTemplate,
		Tags:                 t.Tags,
	}
	if t.ResponseSchema != nil {
		out.ResponseSchema = t.ResponseSchema.JSON
	}
	for _, in := range t.Inputs {
		out.Inputs = append(out.Inputs, InputField{
			Name:     in.Name,
			Modality: string(in.Modality),
			Required: in.Required,
		})
	}
	return out
}

// modalityStrings converts a []registry.Modality to a []string.
func modalityStrings(ms []registry.Modality) []string {
	if len(ms) == 0 {
		return nil
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(m))
	}
	return out
}

// assetRef converts a single FieldValue to an eval.AssetRef, enforcing that
// EXACTLY ONE of text/file/gcs is set (mirrors the CLI's per-flag assembly in
// cmd buildInstance). An empty or multiply-set value is an error. When
// allowLocalFiles is false, a {file: ...} input is rejected with a tool error —
// a local file: input is a local-transport-only affordance (stdio), unsafe for a
// possibly-remote HTTP caller.
func assetRef(key string, v FieldValue, allowLocalFiles bool) (eval.AssetRef, error) {
	set := 0
	if v.Text != "" {
		set++
	}
	if v.File != "" {
		set++
	}
	if v.GCS != "" {
		set++
	}
	switch {
	case set == 0:
		return eval.AssetRef{}, fmt.Errorf("field %q: set exactly one of text, file, or gcs", key)
	case set > 1:
		return eval.AssetRef{}, fmt.Errorf("field %q: set exactly one of text, file, or gcs (got %d)", key, set)
	}
	switch {
	case v.Text != "":
		return eval.AssetRef{Modality: registry.ModalityText, Text: v.Text}, nil
	case v.File != "":
		if !allowLocalFiles {
			return eval.AssetRef{}, fmt.Errorf("field %q: local file inputs are disabled on this transport; use gcs: or text:", key)
		}
		return eval.AssetRef{FilePath: v.File}, nil
	default:
		return eval.AssetRef{GCSUri: v.GCS}, nil
	}
}

// instanceFromFields builds an eval.Instance from the MCP fields map. It mirrors
// the CLI's buildInstance: each entry becomes one AssetRef keyed by field name.
// allowLocalFiles is threaded to assetRef to gate local file: inputs per transport.
func instanceFromFields(fields map[string]FieldValue, allowLocalFiles bool) (eval.Instance, error) {
	inst := eval.Instance{Fields: map[string]eval.AssetRef{}}
	for k, v := range fields {
		if k == "" {
			return eval.Instance{}, fmt.Errorf("empty field name is not allowed")
		}
		ref, err := assetRef(k, v, allowLocalFiles)
		if err != nil {
			return eval.Instance{}, err
		}
		inst.Fields[k] = ref
	}
	return inst, nil
}

// runOutput projects an eval.Result onto the mizan_eval_run output.
func runOutput(res eval.Result) EvalRunOut {
	return EvalRunOut{
		Score:          res.Score,
		Passed:         res.Passed,
		Confidence:     res.Confidence,
		Choice:         res.ChoiceSelection,
		PairwiseChoice: res.PairwiseChoice,
		CustomOutput:   res.CustomOutput,
		Explanation:    res.Explanation,
		Warnings:       res.Warnings,
	}
}

// pairwiseOutput projects an eval.Result onto the mizan_eval_pairwise output.
func pairwiseOutput(res eval.Result) EvalPairwiseOut {
	return EvalPairwiseOut{
		PairwiseChoice: res.PairwiseChoice,
		Explanation:    res.Explanation,
		Warnings:       res.Warnings,
	}
}

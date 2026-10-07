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

// handlers.go holds the SDK-agnostic tool logic. Each method takes a typed input
// struct and returns a typed output struct + error; it imports NO go-sdk types
// (the containment rule). server.go wraps each of these in a thin
// mcp.ToolHandlerFor adapter. A returned error becomes an MCP TOOL error
// (IsError=true) at the SDK boundary.

import (
	"context"
	"fmt"
	"os"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/results"
)

// handlers carries the injected service dependencies the four tools share. The
// package never calls wire.* itself — deps are injected so tests use fakes
// (design §3).
type handlers struct {
	deps Deps
}

// storeDefault reports whether a run should be persisted, treating a nil *bool
// (the param omitted) as the CLI default-on behavior.
func storeDefault(store *bool) bool {
	return store == nil || *store
}

// list backs mizan_list_metrics -> registry.Service.List.
func (h *handlers) list(ctx context.Context, in ListMetricsIn) (ListMetricsOut, error) {
	ts, err := h.deps.Registry.List(ctx, listFilter(in))
	if err != nil {
		return ListMetricsOut{}, err
	}
	out := ListMetricsOut{Metrics: make([]MetricSummary, 0, len(ts))}
	for _, t := range ts {
		out.Metrics = append(out.Metrics, metricSummary(t))
	}
	return out, nil
}

// get backs mizan_get_metric -> registry.Service.Get. An unknown id surfaces the
// registry's error, which the SDK turns into a tool error (IsError=true).
func (h *handlers) get(ctx context.Context, in GetMetricIn) (GetMetricOut, error) {
	if in.ID == "" {
		return GetMetricOut{}, fmt.Errorf("id is required")
	}
	t, err := h.deps.Registry.Get(ctx, in.ID)
	if err != nil {
		return GetMetricOut{}, err
	}
	return metricDetail(*t), nil
}

// run backs mizan_eval_run: fetch the template, assemble the instance from
// fields, run the engine, and (by default) persist the run and return its runId.
//
// PROJECT/LOCATION OVERRIDE (design §5, brief §"Field assembly & project/location
// override"): in.Project / in.Location are accepted in the schema but CANNOT be
// applied here in Phase A. The engine is injected already-constructed (its
// projectID/location are fixed at NewEngine time), and the CLI applies an
// override via applyProjectOverride on *config.Config BEFORE wire.NewEngine
// builds the engine. Phase B (the cmd transport layer) must intercept these
// params and build/select a per-call engine (applyProjectOverride + wire.NewEngine)
// so the override takes effect. They are NOT silently dropped: this is documented
// here and in the phase-A project-log for Phase B to wire. Only the model
// override is applicable at this layer (eval.WithModel).
func (h *handlers) run(ctx context.Context, in EvalRunIn) (EvalRunOut, error) {
	tmpl, err := h.deps.Registry.Get(ctx, in.Metric)
	if err != nil {
		return EvalRunOut{}, err
	}
	inst, err := instanceFromFields(in.Fields)
	if err != nil {
		return EvalRunOut{}, err
	}
	res, err := h.deps.Engine.Run(ctx, *tmpl, inst, eval.WithModel(in.Model))
	if err != nil {
		return EvalRunOut{}, err
	}
	out := runOutput(res)
	if storeDefault(in.Store) {
		runID, warn := h.record(ctx, "eval run", *tmpl, inst, res)
		if warn != "" {
			out.Warnings = append(out.Warnings, warn)
		}
		out.RunID = runID
	}
	return out, nil
}

// pairwise backs mizan_eval_pairwise: fold baseline/candidate into the instance
// under the template's Baseline/CandidateFieldName, validate, run, and (by
// default) persist. The same project/location deferral as run applies.
func (h *handlers) pairwise(ctx context.Context, in EvalPairwiseIn) (EvalPairwiseOut, error) {
	tmpl, err := h.deps.Registry.Get(ctx, in.Metric)
	if err != nil {
		return EvalPairwiseOut{}, err
	}
	if tmpl.BaselineFieldName == "" || tmpl.CandidateFieldName == "" {
		return EvalPairwiseOut{}, fmt.Errorf("metric %q is not a pairwise metric (it does not set baseline and candidate field names)", in.Metric)
	}
	inst, err := instanceFromFields(in.Fields)
	if err != nil {
		return EvalPairwiseOut{}, err
	}
	baseline, err := assetRef(tmpl.BaselineFieldName, in.Baseline)
	if err != nil {
		return EvalPairwiseOut{}, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := assetRef(tmpl.CandidateFieldName, in.Candidate)
	if err != nil {
		return EvalPairwiseOut{}, fmt.Errorf("candidate: %w", err)
	}
	inst.Fields[tmpl.BaselineFieldName] = baseline
	inst.Fields[tmpl.CandidateFieldName] = candidate

	if err := requirePairwiseFields(*tmpl, inst); err != nil {
		return EvalPairwiseOut{}, err
	}
	res, err := h.deps.Engine.Run(ctx, *tmpl, inst, eval.WithModel(in.Model))
	if err != nil {
		return EvalPairwiseOut{}, err
	}
	out := pairwiseOutput(res)
	if storeDefault(in.Store) {
		runID, warn := h.record(ctx, "eval pairwise", *tmpl, inst, res)
		if warn != "" {
			out.Warnings = append(out.Warnings, warn)
		}
		out.RunID = runID
	}
	return out, nil
}

// requirePairwiseFields enforces that the template's baseline and candidate field
// names are present in the instance (mirrors cmd/mizan's requirePairwiseFields).
func requirePairwiseFields(tmpl registry.MetricTemplate, inst eval.Instance) error {
	var missing []string
	if tmpl.BaselineFieldName != "" {
		if _, ok := inst.Fields[tmpl.BaselineFieldName]; !ok {
			missing = append(missing, fmt.Sprintf("baseline field %q", tmpl.BaselineFieldName))
		}
	}
	if tmpl.CandidateFieldName != "" {
		if _, ok := inst.Fields[tmpl.CandidateFieldName]; !ok {
			missing = append(missing, fmt.Sprintf("candidate field %q", tmpl.CandidateFieldName))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("pairwise requires the %s", joinAnd(missing))
	}
	return nil
}

// joinAnd joins parts with " and " (small local helper to avoid pulling strings).
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		out := parts[0]
		for _, p := range parts[1:] {
			out += " and " + p
		}
		return out
	}
}

// record persists a successful eval result to the results store and returns the
// run id. Store failure is NON-fatal (mirrors the CLI storeResult policy): it
// returns an empty runId plus a warning string the caller surfaces in the tool
// output's warnings, rather than failing the eval.
func (h *handlers) record(ctx context.Context, command string, tmpl registry.MetricTemplate, inst eval.Instance, res eval.Result) (runID string, warning string) {
	if h.deps.Results == nil {
		return "", "result not persisted: results store not configured"
	}
	var applied results.AppliedAutorater
	if res.Applied != nil {
		applied = results.AppliedAutorater{
			Model:         res.Applied.Model,
			SamplingCount: res.Applied.SamplingCount,
			FlipEnabled:   res.Applied.FlipEnabled,
			EffectiveHost: res.Applied.EffectiveHost,
			Location:      res.Applied.Location,
			ModelSource:   res.Applied.ModelSource,
		}
	}
	host, _ := os.Hostname()
	var projectID, location string
	if h.deps.Config != nil {
		projectID = h.deps.Config.ProjectID
		location = h.deps.Config.Location
	}
	stored, err := h.deps.Results.Record(ctx, results.RecordInput{
		Command:   command,
		ProjectID: projectID,
		Location:  location,
		HostLabel: host,
		Template:  tmpl,
		Instance:  inst,
		Applied:   applied,
		Outcome:   res,
	})
	if err != nil {
		return "", fmt.Sprintf("result not persisted: %v", err)
	}
	return stored.RunID, ""
}

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

package evalset

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// TemplateGetter resolves a metric template by id. *registry.Service satisfies
// it (Service.Get). It is the narrow seam the runner depends on, so tests can
// supply a fake without a store.
type TemplateGetter interface {
	Get(ctx context.Context, id string) (*registry.MetricTemplate, error)
}

// MemberRunner runs one metric template against one instance. *eval.Engine
// satisfies it (Engine.Run). It is the narrow seam the runner depends on, so
// tests can supply a fake without any live API access.
type MemberRunner interface {
	Run(ctx context.Context, tmpl registry.MetricTemplate, inst eval.Instance, opts ...eval.RunOption) (eval.Result, error)
}

// Runner executes an eval-set's ordered members against the eval engine and
// computes an aggregate + verdict. It depends only on the two narrow seams
// above.
type Runner struct {
	templates TemplateGetter
	engine    MemberRunner
}

// New constructs a Runner over the given template getter and member runner.
func New(tg TemplateGetter, mr MemberRunner) *Runner {
	return &Runner{templates: tg, engine: mr}
}

// RunOptions configures a single set run.
type RunOptions struct {
	// Inputs are the shared set-level inputs projected onto each member's
	// expected placeholders via Member.Bind, Set.Inputs, and identity fallback.
	Inputs map[string]eval.AssetRef
	// Model is an optional per-run autorater model override applied to every
	// member.
	Model string
	// RunOpts are optional per-run eval.RunOption values forwarded to every
	// member execution alongside WithModel.
	RunOpts []eval.RunOption
	// FailFast, when true, aborts the run at the first member that errors or is
	// missing; remaining members are recorded as Skipped. The default
	// (continue-on-error) runs every member.
	FailFast bool
}

type preparedMember struct {
	tmpl       *registry.MetricTemplate
	resolveErr error
	inst       eval.Instance
}

// Run executes set.Members SEQUENTIALLY in order. Before invoking the engine it
// resolves member templates and projects opt.Inputs onto each member's expected
// placeholders (failing pre-flight if a member's bind mapping is invalid or if a
// supplied suite input is not referenced by any resolved member).
//
// FailFast breaks at the first errored/missing member (subsequent members are
// Skipped). Continue-on-error is the default. The aggregate is computed over the
// numeric-scored OK members; the verdict is ALWAYS computed (see verdict).
func (r *Runner) Run(ctx context.Context, set Set, opt RunOptions) (EvalSetResult, error) {
	started := time.Now()

	res := EvalSetResult{
		SetID:      set.ID,
		SetName:    set.Name,
		Version:    set.Version,
		AssetClass: set.AssetClass,
		Gate:       set.Aggregation.Gate,
		StartedAt:  started,
	}

	prepared := make([]preparedMember, len(set.Members))
	suiteConsumed := make(map[string]bool, len(opt.Inputs))
	resolvedCount := 0
	for i, m := range set.Members {
		tmpl, err := r.templates.Get(ctx, m.MetricID)
		if err != nil {
			prepared[i] = preparedMember{resolveErr: err}
			continue
		}
		resolvedCount++
		inst, consumed, projErr := projectMemberInstance(set, m, *tmpl, opt.Inputs)
		if projErr != nil {
			return EvalSetResult{}, projErr
		}
		for k := range consumed {
			suiteConsumed[k] = true
		}
		prepared[i] = preparedMember{tmpl: tmpl, inst: inst}
	}

	if len(opt.Inputs) > 0 && resolvedCount > 0 {
		var unconsumed []string
		for k := range opt.Inputs {
			if !suiteConsumed[k] {
				unconsumed = append(unconsumed, k)
			}
		}
		if len(unconsumed) > 0 {
			sort.Strings(unconsumed)
			return EvalSetResult{}, fmt.Errorf("evalset: suite %q input(s) %v are not referenced by any member placeholder or bind mapping (check for a typo)", set.ID, unconsumed)
		}
	}

	runOpts := slices.Clone(opt.RunOpts)
	if opt.Model != "" {
		runOpts = append(runOpts, eval.WithModel(opt.Model))
	}

	aborted := false
	for i, m := range set.Members {
		mr := MemberResult{
			MetricID: m.MetricID,
			Weight:   m.Weight,
			Required: m.Required,
		}

		if aborted {
			mr.Status = Skipped
			res.Members = append(res.Members, mr)
			continue
		}

		pm := prepared[i]
		if pm.resolveErr != nil {
			mr.Status = Missing
			mr.Error = pm.resolveErr.Error()
			res.Members = append(res.Members, mr)
			if opt.FailFast {
				aborted = true
			}
			continue
		}

		out, err := r.engine.Run(ctx, *pm.tmpl, pm.inst, runOpts...)
		if err != nil {
			mr.Status = Errored
			mr.Error = err.Error()
			res.Members = append(res.Members, mr)
			if opt.FailFast {
				aborted = true
			}
			continue
		}

		mr.Status = OK
		mr.Result = out
		// A non-finite score (NaN/Inf) from the engine is treated like a non-scalar
		// member: recorded in the row (via Result) but its Score is left nil so it
		// cannot propagate into the aggregate or silently pass a threshold.
		if s := out.Score; s != nil && isFinite(float64(*s)) {
			mr.Score = s
		}
		res.Members = append(res.Members, mr)
	}

	res.Aggregate = aggregate(set.Aggregation.Method, set.Aggregation.Threshold, res.Members)
	res.Verdict = verdict(res.Members, res.Aggregate, set.Aggregation.Threshold)
	res.Duration = time.Since(started)

	return res, nil
}

// verdict computes the set verdict, ALWAYS (independent of the Gate flag). It is
// FAILED when any required member did not succeed, or when a threshold is set and
// the aggregate score is nil, non-finite, or below it; otherwise PASSED. A nil or
// non-finite aggregate against a set threshold must never silently PASS.
func verdict(members []MemberResult, agg Aggregate, threshold *float64) SetVerdict {
	for _, m := range members {
		if m.Required && m.Status != OK {
			return Failed
		}
	}
	if threshold != nil {
		if agg.Score == nil || !isFinite(float64(*agg.Score)) || float64(*agg.Score) < *threshold {
			return Failed
		}
	}
	return Passed
}

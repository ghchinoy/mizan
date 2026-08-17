package evalset

import (
	"context"
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
	// Inputs are the SHARED set-level inputs, passed by identity to every member's
	// instance (set input name == member placeholder name). Phase 1 does not
	// resolve bind aliases.
	Inputs map[string]eval.AssetRef
	// Model is an optional per-run autorater model override applied to every
	// member.
	Model string
	// FailFast, when true, aborts the run at the first member that errors or is
	// missing; remaining members are recorded as Skipped. The default
	// (continue-on-error) runs every member.
	FailFast bool
}

// Run executes set.Members SEQUENTIALLY in order. For each member it resolves
// the template (a resolution error -> Status:Missing), builds the member's
// instance by identity binding (see instanceFor), and runs the engine (an engine
// error -> Status:Errored). A successful run records Status:OK with the score.
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

	var runOpts []eval.RunOption
	if opt.Model != "" {
		runOpts = append(runOpts, eval.WithModel(opt.Model))
	}

	aborted := false
	for _, m := range set.Members {
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

		tmpl, err := r.templates.Get(ctx, m.MetricID)
		if err != nil {
			mr.Status = Missing
			mr.Error = err.Error()
			res.Members = append(res.Members, mr)
			if opt.FailFast {
				aborted = true
			}
			continue
		}

		inst := instanceFor(opt.Inputs)
		out, err := r.engine.Run(ctx, *tmpl, inst, runOpts...)
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

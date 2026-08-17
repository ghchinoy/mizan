package evalset

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// fakeGetter is a fake TemplateGetter: it returns a template per id, or an error
// for ids not present (simulating a missing template).
type fakeGetter struct {
	templates map[string]*registry.MetricTemplate
}

func (f *fakeGetter) Get(_ context.Context, id string) (*registry.MetricTemplate, error) {
	t, ok := f.templates[id]
	if !ok {
		return nil, errors.New("template not found: " + id)
	}
	return t, nil
}

// fakeRunner is a fake MemberRunner. It records the instances it was called with
// (to assert identity binding) and returns a scripted result/error per template
// id.
type fakeRunner struct {
	results map[string]eval.Result
	errs    map[string]error
	seen    []eval.Instance
	seenIDs []string
	// seenOptCount records how many eval.RunOptions were delivered to each engine
	// call, so a test can assert --model passthrough (WithModel appended) without
	// reaching into the unexported runConfig.
	seenOptCount []int
}

func (f *fakeRunner) Run(_ context.Context, tmpl registry.MetricTemplate, inst eval.Instance, opts ...eval.RunOption) (eval.Result, error) {
	f.seen = append(f.seen, inst)
	f.seenIDs = append(f.seenIDs, tmpl.ID)
	f.seenOptCount = append(f.seenOptCount, len(opts))
	if err, ok := f.errs[tmpl.ID]; ok && err != nil {
		return eval.Result{}, err
	}
	return f.results[tmpl.ID], nil
}

func f32(v float32) *float32 { return &v }
func f64(v float64) *float64 { return &v }

// closef reports whether got is within a small tolerance of want; float32
// aggregation over non-exactly-representable decimals (e.g. 0.8, 0.6) makes an
// exact == brittle.
func closef(got, want float32) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}

// tmpl builds a minimal pointwise template with the given id.
func tmpl(id string) *registry.MetricTemplate {
	return &registry.MetricTemplate{ID: id, Kind: registry.KindPointwise}
}

func TestRun_IdentityBinding(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/a": tmpl("p/a"),
	}}
	runner := &fakeRunner{results: map[string]eval.Result{"p/a": {Score: f32(0.9)}}}
	r := New(getter, runner)

	inputs := map[string]eval.AssetRef{
		"asset": {Modality: registry.ModalityText, Text: "hello"},
	}
	set := Set{
		ID:          "p/set",
		Members:     []Member{{MetricID: "p/a", Weight: 1}},
		Aggregation: Aggregation{Method: AggMean},
	}

	_, err := r.Run(context.Background(), set, RunOptions{Inputs: inputs})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.seen) != 1 {
		t.Fatalf("expected 1 engine call, got %d", len(runner.seen))
	}
	if !reflect.DeepEqual(runner.seen[0].Fields, inputs) {
		t.Fatalf("identity binding mismatch: got %#v want %#v", runner.seen[0].Fields, inputs)
	}
}

// TestRun_IdentityBindingPerMember proves the shared set inputs are identity-bound
// into EVERY member's instance (not just the first), and that binding.go copies the
// map so a member run cannot mutate the caller's shared inputs (documented in
// binding.go, otherwise untested).
func TestRun_IdentityBindingPerMember(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/a": tmpl("p/a"), "p/b": tmpl("p/b"),
	}}
	runner := &fakeRunner{results: map[string]eval.Result{
		"p/a": {Score: f32(0.9)}, "p/b": {Score: f32(0.8)},
	}}
	r := New(getter, runner)

	inputs := map[string]eval.AssetRef{
		"asset": {Modality: registry.ModalityText, Text: "hello"},
	}
	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/a", Weight: 1},
			{MetricID: "p/b", Weight: 1},
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	if _, err := r.Run(context.Background(), set, RunOptions{Inputs: inputs}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.seen) != 2 {
		t.Fatalf("expected 2 engine calls, got %d", len(runner.seen))
	}
	for i, inst := range runner.seen {
		if !reflect.DeepEqual(inst.Fields, inputs) {
			t.Fatalf("member %d identity binding mismatch: got %#v want %#v", i, inst.Fields, inputs)
		}
	}
	// The instance must be a copy: mutating a member's fields must not bleed into
	// the shared inputs or into the sibling member's instance.
	runner.seen[0].Fields["asset"] = eval.AssetRef{Text: "mutated"}
	if got := inputs["asset"].Text; got != "hello" {
		t.Fatalf("shared inputs mutated through member instance: asset=%q", got)
	}
	if got := runner.seen[1].Fields["asset"].Text; got != "hello" {
		t.Fatalf("sibling member instance shares backing map: asset=%q", got)
	}
}

func TestRun_ContinueOnError(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/a": tmpl("p/a"), "p/b": tmpl("p/b"), "p/c": tmpl("p/c"),
	}}
	runner := &fakeRunner{
		results: map[string]eval.Result{
			"p/a": {Score: f32(0.8)},
			"p/c": {Score: f32(0.6)},
		},
		errs: map[string]error{"p/b": errors.New("boom")},
	}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/a", Weight: 1},
			{MetricID: "p/b", Weight: 1},
			{MetricID: "p/c", Weight: 1},
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Members) != 3 {
		t.Fatalf("expected 3 member rows, got %d", len(res.Members))
	}
	if res.Members[1].Status != Errored {
		t.Fatalf("member b status = %q, want Errored", res.Members[1].Status)
	}
	if res.Members[2].Status != OK {
		t.Fatalf("member c status = %q, want OK (run should continue)", res.Members[2].Status)
	}
	// Aggregate reflects only scored members a(0.8) and c(0.6) -> mean 0.7.
	if res.Aggregate.Score == nil || !closef(*res.Aggregate.Score, 0.7) {
		t.Fatalf("aggregate = %v, want 0.7", res.Aggregate.Score)
	}
	if res.Aggregate.Scored != 2 {
		t.Fatalf("Scored = %d, want 2", res.Aggregate.Scored)
	}
	if res.Aggregate.Failed != 1 {
		t.Fatalf("Failed = %d, want 1", res.Aggregate.Failed)
	}
}

func TestRun_FailFast(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/a": tmpl("p/a"), "p/b": tmpl("p/b"), "p/c": tmpl("p/c"),
	}}
	runner := &fakeRunner{
		results: map[string]eval.Result{"p/a": {Score: f32(0.8)}, "p/c": {Score: f32(0.6)}},
		errs:    map[string]error{"p/b": errors.New("boom")},
	}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/a", Weight: 1},
			{MetricID: "p/b", Weight: 1},
			{MetricID: "p/c", Weight: 1},
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{FailFast: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Members[1].Status != Errored {
		t.Fatalf("member b status = %q, want Errored", res.Members[1].Status)
	}
	if res.Members[2].Status != Skipped {
		t.Fatalf("member c status = %q, want Skipped (fail-fast)", res.Members[2].Status)
	}
	// c must not have been dispatched to the engine.
	if len(runner.seenIDs) != 2 {
		t.Fatalf("engine calls = %v, want 2 (a,b)", runner.seenIDs)
	}
}

func TestRun_MissingTemplate(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{"p/a": tmpl("p/a")}}
	runner := &fakeRunner{results: map[string]eval.Result{"p/a": {Score: f32(0.9)}}}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/a", Weight: 1},
			{MetricID: "p/missing", Weight: 1},
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Members[1].Status != Missing {
		t.Fatalf("missing member status = %q, want Missing", res.Members[1].Status)
	}
	if res.Members[1].Error == "" {
		t.Fatal("missing member should carry an error message")
	}
}

func TestRun_RequiredMemberFailsForcesFailed(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/a": tmpl("p/a"), "p/b": tmpl("p/b"),
	}}
	runner := &fakeRunner{
		results: map[string]eval.Result{"p/a": {Score: f32(0.95)}},
		errs:    map[string]error{"p/b": errors.New("boom")},
	}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/a", Weight: 1},
			{MetricID: "p/b", Weight: 1, Required: true},
		},
		// No threshold; a(0.95) alone would otherwise pass.
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{}) // continue-on-error
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Verdict != Failed {
		t.Fatalf("verdict = %q, want FAILED (required member failed)", res.Verdict)
	}
}

func TestRun_NonScalarExcludedFromAggregate(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/scalar": tmpl("p/scalar"), "p/pairwise": tmpl("p/pairwise"),
	}}
	runner := &fakeRunner{results: map[string]eval.Result{
		"p/scalar":   {Score: f32(0.5)},
		"p/pairwise": {PairwiseChoice: "A"}, // Score == nil
	}}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/scalar", Weight: 1},
			{MetricID: "p/pairwise", Weight: 1},
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Members) != 2 {
		t.Fatalf("expected 2 rows (non-scalar present), got %d", len(res.Members))
	}
	if res.Members[1].Status != OK {
		t.Fatalf("non-scalar member status = %q, want OK", res.Members[1].Status)
	}
	if res.Aggregate.Scored != 1 {
		t.Fatalf("Scored = %d, want 1 (non-scalar excluded)", res.Aggregate.Scored)
	}
	if res.Aggregate.Score == nil || *res.Aggregate.Score != 0.5 {
		t.Fatalf("aggregate = %v, want 0.5", res.Aggregate.Score)
	}
}

func TestRun_ZeroScoredMembers(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{"p/pw": tmpl("p/pw")}}
	runner := &fakeRunner{results: map[string]eval.Result{"p/pw": {PairwiseChoice: "A"}}}
	r := New(getter, runner)

	set := Set{
		ID:          "p/set",
		Members:     []Member{{MetricID: "p/pw", Weight: 1}},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Aggregate.Score != nil {
		t.Fatalf("aggregate score = %v, want nil (no numeric members)", res.Aggregate.Score)
	}
	if res.Verdict != Passed {
		t.Fatalf("verdict = %q, want PASSED (no threshold, no required failure)", res.Verdict)
	}
}

func TestRun_ThresholdPassAndFail(t *testing.T) {
	build := func(score float32) (*Runner, Set) {
		getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{"p/a": tmpl("p/a")}}
		runner := &fakeRunner{results: map[string]eval.Result{"p/a": {Score: f32(score)}}}
		set := Set{
			ID:          "p/set",
			Members:     []Member{{MetricID: "p/a", Weight: 1}},
			Aggregation: Aggregation{Method: AggMean, Threshold: f64(0.8)},
		}
		return New(getter, runner), set
	}

	t.Run("pass", func(t *testing.T) {
		r, set := build(0.85)
		res, _ := r.Run(context.Background(), set, RunOptions{})
		if res.Verdict != Passed {
			t.Fatalf("verdict = %q, want PASSED", res.Verdict)
		}
		if res.Aggregate.Passed == nil || !*res.Aggregate.Passed {
			t.Fatalf("Aggregate.Passed = %v, want true", res.Aggregate.Passed)
		}
	})
	t.Run("fail", func(t *testing.T) {
		r, set := build(0.7)
		res, _ := r.Run(context.Background(), set, RunOptions{})
		if res.Verdict != Failed {
			t.Fatalf("verdict = %q, want FAILED", res.Verdict)
		}
		if res.Aggregate.Passed == nil || *res.Aggregate.Passed {
			t.Fatalf("Aggregate.Passed = %v, want false", res.Aggregate.Passed)
		}
	})
}

// TestRun_FailFastAbortsOnMissing covers the fail-fast abort triggered by a
// MISSING template (runner.go), complementing TestRun_FailFast which only
// exercises the Errored abort. The member after the missing one must be Skipped
// and never dispatched to the engine, and Aggregate.Failed must count only the
// real failure (Missing), NOT the Skipped member (design §9).
func TestRun_FailFastAbortsOnMissing(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/a": tmpl("p/a"), "p/c": tmpl("p/c"),
		// p/missing is intentionally absent.
	}}
	runner := &fakeRunner{results: map[string]eval.Result{
		"p/a": {Score: f32(0.8)}, "p/c": {Score: f32(0.6)},
	}}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/a", Weight: 1},
			{MetricID: "p/missing", Weight: 1},
			{MetricID: "p/c", Weight: 1},
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{FailFast: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Members[1].Status != Missing {
		t.Fatalf("member[1] status = %q, want Missing", res.Members[1].Status)
	}
	if res.Members[2].Status != Skipped {
		t.Fatalf("member[2] status = %q, want Skipped (fail-fast aborts after missing)", res.Members[2].Status)
	}
	// Only p/a dispatched; p/c never reached the engine.
	if len(runner.seenIDs) != 1 || runner.seenIDs[0] != "p/a" {
		t.Fatalf("engine calls = %v, want [p/a]", runner.seenIDs)
	}
	// Failed counts the Missing member only; the Skipped member is not a failure.
	if res.Aggregate.Failed != 1 {
		t.Fatalf("Aggregate.Failed = %d, want 1 (Missing only, Skipped excluded)", res.Aggregate.Failed)
	}
}

// TestRun_FailedExcludesSkipped proves Aggregate.Failed counts only Errored +
// Missing members and NEVER the Skipped members that fail-fast aborts before
// reaching (design §9). With 1 errored + 2 skipped, Failed must be 1, not 3.
func TestRun_FailedExcludesSkipped(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/a": tmpl("p/a"), "p/b": tmpl("p/b"), "p/c": tmpl("p/c"), "p/d": tmpl("p/d"),
	}}
	runner := &fakeRunner{
		results: map[string]eval.Result{"p/a": {Score: f32(0.9)}},
		errs:    map[string]error{"p/b": errors.New("boom")},
	}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/a", Weight: 1},
			{MetricID: "p/b", Weight: 1}, // errors -> abort
			{MetricID: "p/c", Weight: 1}, // skipped
			{MetricID: "p/d", Weight: 1}, // skipped
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{FailFast: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Members[2].Status != Skipped || res.Members[3].Status != Skipped {
		t.Fatalf("members c,d status = %q,%q, want Skipped,Skipped", res.Members[2].Status, res.Members[3].Status)
	}
	if res.Aggregate.Failed != 1 {
		t.Fatalf("Aggregate.Failed = %d, want 1 (Errored only; 2 Skipped excluded)", res.Aggregate.Failed)
	}
}

// TestRun_NonFiniteScoreExcluded proves a NaN or +Inf member score is recorded in
// the row (Status OK) but excluded from the aggregate, exactly like a non-scalar
// member, so it cannot propagate into the aggregate.
func TestRun_NonFiniteScoreExcluded(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
		"p/ok": tmpl("p/ok"), "p/nan": tmpl("p/nan"), "p/inf": tmpl("p/inf"),
	}}
	runner := &fakeRunner{results: map[string]eval.Result{
		"p/ok":  {Score: f32(0.5)},
		"p/nan": {Score: f32(float32(math.NaN()))},
		"p/inf": {Score: f32(float32(math.Inf(1)))},
	}}
	r := New(getter, runner)

	set := Set{
		ID: "p/set",
		Members: []Member{
			{MetricID: "p/ok", Weight: 1},
			{MetricID: "p/nan", Weight: 1},
			{MetricID: "p/inf", Weight: 1},
		},
		Aggregation: Aggregation{Method: AggMean},
	}

	res, err := r.Run(context.Background(), set, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Members) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(res.Members))
	}
	// Non-finite members ran OK but carry no scalar Score for aggregation.
	if res.Members[1].Status != OK || res.Members[1].Score != nil {
		t.Fatalf("NaN member = {%q, %v}, want {OK, nil score}", res.Members[1].Status, res.Members[1].Score)
	}
	if res.Members[2].Status != OK || res.Members[2].Score != nil {
		t.Fatalf("Inf member = {%q, %v}, want {OK, nil score}", res.Members[2].Status, res.Members[2].Score)
	}
	if res.Aggregate.Scored != 1 {
		t.Fatalf("Scored = %d, want 1 (only the finite member)", res.Aggregate.Scored)
	}
	if res.Aggregate.Score == nil || !closef(*res.Aggregate.Score, 0.5) {
		t.Fatalf("aggregate = %v, want 0.5", res.Aggregate.Score)
	}
	if res.Verdict != Passed {
		t.Fatalf("verdict = %q, want PASSED (no threshold)", res.Verdict)
	}
}

// TestRun_NonFiniteAggregateWithThresholdFails proves that when a threshold is
// set and the only scored member is non-finite (so the aggregate is nil), the
// verdict is FAILED — never a silent PASS from a NaN comparison.
func TestRun_NonFiniteAggregateWithThresholdFails(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{"p/nan": tmpl("p/nan")}}
	runner := &fakeRunner{results: map[string]eval.Result{
		"p/nan": {Score: f32(float32(math.NaN()))},
	}}
	r := New(getter, runner)

	set := Set{
		ID:          "p/set",
		Members:     []Member{{MetricID: "p/nan", Weight: 1}},
		Aggregation: Aggregation{Method: AggMean, Threshold: f64(0.8)},
	}

	res, err := r.Run(context.Background(), set, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Aggregate.Score != nil {
		t.Fatalf("aggregate score = %v, want nil (non-finite excluded)", res.Aggregate.Score)
	}
	if res.Verdict != Failed {
		t.Fatalf("verdict = %q, want FAILED (nil aggregate vs threshold, no silent pass)", res.Verdict)
	}
	if res.Aggregate.Passed == nil || *res.Aggregate.Passed {
		t.Fatalf("Aggregate.Passed = %v, want false", res.Aggregate.Passed)
	}
}

// TestRun_ThresholdZeroScoredFails covers the reviewer's L2 gap: a threshold set
// with zero numeric-scored members (all non-scalar) yields a nil aggregate, which
// must FAIL the threshold end-to-end (not PASS).
func TestRun_ThresholdZeroScoredFails(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{"p/pw": tmpl("p/pw")}}
	runner := &fakeRunner{results: map[string]eval.Result{"p/pw": {PairwiseChoice: "A"}}}
	r := New(getter, runner)

	set := Set{
		ID:          "p/set",
		Members:     []Member{{MetricID: "p/pw", Weight: 1}},
		Aggregation: Aggregation{Method: AggMean, Threshold: f64(0.8)},
	}

	res, err := r.Run(context.Background(), set, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Aggregate.Score != nil {
		t.Fatalf("aggregate score = %v, want nil (no numeric members)", res.Aggregate.Score)
	}
	if res.Verdict != Failed {
		t.Fatalf("verdict = %q, want FAILED (nil aggregate vs threshold)", res.Verdict)
	}
	if res.Aggregate.Passed == nil || *res.Aggregate.Passed {
		t.Fatalf("Aggregate.Passed = %v, want false", res.Aggregate.Passed)
	}
}

// TestRun_ModelPassthrough covers the reviewer's L2 gap: a RunOptions.Model is
// wired into every engine call (as an eval.WithModel RunOption), and no option is
// appended when Model is empty.
func TestRun_ModelPassthrough(t *testing.T) {
	newFixture := func() (*Runner, *fakeRunner, Set) {
		getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{
			"p/a": tmpl("p/a"), "p/b": tmpl("p/b"),
		}}
		runner := &fakeRunner{results: map[string]eval.Result{
			"p/a": {Score: f32(0.8)}, "p/b": {Score: f32(0.9)},
		}}
		set := Set{
			ID:          "p/set",
			Members:     []Member{{MetricID: "p/a", Weight: 1}, {MetricID: "p/b", Weight: 1}},
			Aggregation: Aggregation{Method: AggMean},
		}
		return New(getter, runner), runner, set
	}

	t.Run("model set -> one option per engine call", func(t *testing.T) {
		r, runner, set := newFixture()
		if _, err := r.Run(context.Background(), set, RunOptions{Model: "gemini-2.0-flash"}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(runner.seenOptCount) != 2 {
			t.Fatalf("engine calls = %d, want 2", len(runner.seenOptCount))
		}
		for i, n := range runner.seenOptCount {
			if n != 1 {
				t.Fatalf("member %d received %d run options, want 1 (WithModel)", i, n)
			}
		}
	})

	t.Run("model empty -> no options", func(t *testing.T) {
		r, runner, set := newFixture()
		if _, err := r.Run(context.Background(), set, RunOptions{}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		for i, n := range runner.seenOptCount {
			if n != 0 {
				t.Fatalf("member %d received %d run options, want 0 (no model)", i, n)
			}
		}
	})
}

func TestRun_GateRecorded(t *testing.T) {
	getter := &fakeGetter{templates: map[string]*registry.MetricTemplate{"p/a": tmpl("p/a")}}
	runner := &fakeRunner{results: map[string]eval.Result{"p/a": {Score: f32(0.9)}}}
	r := New(getter, runner)

	set := Set{
		ID:          "p/set",
		Members:     []Member{{MetricID: "p/a", Weight: 1}},
		Aggregation: Aggregation{Method: AggMean, Gate: true},
	}
	res, _ := r.Run(context.Background(), set, RunOptions{})
	if !res.Gate {
		t.Fatal("EvalSetResult.Gate should mirror set.Aggregation.Gate (true)")
	}
}

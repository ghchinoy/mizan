package evalset

import (
	"context"
	"errors"
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
}

func (f *fakeRunner) Run(_ context.Context, tmpl registry.MetricTemplate, inst eval.Instance, _ ...eval.RunOption) (eval.Result, error) {
	f.seen = append(f.seen, inst)
	f.seenIDs = append(f.seenIDs, tmpl.ID)
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

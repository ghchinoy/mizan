package evalset

import (
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
)

// MemberStatus is the per-member run outcome.
type MemberStatus string

const (
	// OK means the member's template resolved and the engine returned a result.
	OK MemberStatus = "OK"
	// Errored means the engine returned an error for the member.
	Errored MemberStatus = "Errored"
	// Missing means the member's template id could not be resolved.
	Missing MemberStatus = "Missing"
	// Skipped means the member was not run (e.g. fail-fast aborted before it).
	Skipped MemberStatus = "Skipped"
)

// SetVerdict is the overall pass/fail outcome of a set run. It is ALWAYS
// computed, independent of whether the set is a gate.
type SetVerdict string

const (
	// Passed means no required member failed and any threshold was met.
	Passed SetVerdict = "PASSED"
	// Failed means a required member did not succeed, or the aggregate score is
	// nil / below the threshold.
	Failed SetVerdict = "FAILED"
)

// MemberResult is one row of the scorecard: the outcome of running one member.
type MemberResult struct {
	MetricID string
	Status   MemberStatus
	Weight   float64
	Required bool
	// Score mirrors Result.Score for convenience; nil for non-scalar members
	// (e.g. pairwise/custom_schema) and for non-OK members.
	Score  *float32
	Result eval.Result
	// Error is the engine or resolution error message, empty on success.
	Error string
}

// Aggregate is the computed scalar aggregation over the numeric-scored members.
type Aggregate struct {
	Method AggregationMethod
	// Score is the aggregate value, or nil when no member produced a numeric
	// score (never NaN).
	Score     *float32
	Threshold *float64
	// Passed reports whether Score met Threshold; nil when there is no threshold.
	Passed *bool
	// Scored is the count of members included in the aggregate (OK + Score!=nil).
	Scored int
	// Failed is the count of members whose Status != OK.
	Failed int
}

// EvalSetResult is the full outcome of running a Set: per-member rows, the
// computed aggregate, and the always-computed verdict. Gate records the set's
// opt-in gate flag; the library does NOT act on it (no os.Exit).
type EvalSetResult struct {
	SetID      string
	SetName    string
	Version    string
	AssetClass string
	Members    []MemberResult
	Aggregate  Aggregate
	Verdict    SetVerdict
	Gate       bool
	StartedAt  time.Time
	Duration   time.Duration
}

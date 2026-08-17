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

// Package evalset holds the RUNNABLE eval-set domain type and its runner.
//
// It is deliberately DISTINCT from internal/registry/pack, which owns the
// on-disk carriage/validation shape (pack.EvalSetDoc). This package converts a
// parsed pack.EvalSetDoc into a runnable Set (FromDoc) and executes its ordered
// members against the eval engine (see runner.go). It imports internal/registry
// (domain model), internal/eval (engine + instance types), and
// internal/registry/pack (the parsed manifest), but NEVER the store
// (internal/registry/sqlite) or cmd/*: the library records results and computes
// a verdict; it does not persist and it does not call os.Exit.
package evalset

import (
	"fmt"

	"github.com/ghchinoy/mizan/internal/registry/pack"
)

// AggregationMethod names a scalar aggregation over the numeric-scored members
// of a set. Phase 1 supports exactly the three below; any other value is
// rejected by FromDoc.
type AggregationMethod string

const (
	// AggMean is the arithmetic mean of the scored members.
	AggMean AggregationMethod = "mean"
	// AggWeightedMean is Σ(w·s)/Σ(w) over the scored members (w = Member.Weight).
	AggWeightedMean AggregationMethod = "weighted-mean"
	// AggMin is the lowest score among the scored members.
	AggMin AggregationMethod = "min"
)

// maxEvalSetMembers caps how many members FromDoc will accept from one manifest.
// The pack codec already bounds a manifest to 1 MiB (pack.maxEvalSetFileBytes),
// but a <=1 MiB manifest can still carry many thousands of members, and the
// runner makes one (billable) engine call per member — an unbounded
// cost/quota-amplification vector (CWE-400/CWE-770). Failing closed here mirrors
// the caps in internal/rubricgen (maxRubrics = 1024) and internal/registry sync
// (maxImportTemplates). 256 is generous for a hand-authored suite.
const maxEvalSetMembers = 256

// Member is one ordered member of a runnable set: a reference to a metric
// template id plus per-member run modifiers.
type Member struct {
	// MetricID is the "<namespace>/<slug>" template id to resolve and run.
	MetricID string
	// Weight is the member's weight in weighted-mean aggregation. It defaults to
	// 1.0 when absent or zero in the manifest (see FromDoc).
	Weight float64
	// Required marks a member whose non-OK outcome forces the set verdict to
	// FAILED, regardless of the aggregate/threshold.
	Required bool
	// Bind is the RESERVED per-member placeholder binding (set input name ->
	// member placeholder name). Phase 1 uses identity binding and carries this map
	// without interpreting it; full bind-alias resolution is a later phase.
	Bind map[string]string
}

// Aggregation is the set-level aggregation config, converted from the manifest.
type Aggregation struct {
	// Method is the aggregation method; FromDoc rejects unsupported methods.
	Method AggregationMethod
	// Threshold, when non-nil, is the pass/fail cutoff compared against the
	// aggregate score in the verdict rule.
	Threshold *float64
	// Gate is opt-in (manifest aggregation.gate:true, default false). The library
	// only records it on the result; a downstream CLI uses Gate+Verdict to decide
	// a process exit code.
	Gate bool
}

// Set is the runnable eval-set domain type, converted from a pack.EvalSetDoc via
// FromDoc.
type Set struct {
	ID           string
	Name         string
	Version      string
	AssetClass   string
	Inputs       map[string]string
	Members      []Member
	Aggregation  Aggregation
	DisplayOrder string
}

// FromDoc converts a parsed pack.EvalSetDoc into a runnable Set. It:
//   - copies identity/metadata,
//   - REJECTS a manifest with more than maxEvalSetMembers members (fail closed),
//   - converts spec.inputs and each member's bind from map[string]any to
//     map[string]string, erroring (naming the key) on any non-string value,
//   - defaults each member's Weight to 1.0 when absent or zero, and REJECTS a
//     NEGATIVE weight (naming the offending member),
//   - reads aggregation.method/threshold/gate, and
//   - ERRORS on an unsupported aggregation method (anything other than mean,
//     weighted-mean, min).
//
// An empty or absent aggregation.method (including a nil aggregation block) is
// normalized EXPLICITLY to AggMean (design §9); a non-empty unsupported method is
// rejected.
func FromDoc(doc *pack.EvalSetDoc) (Set, error) {
	if doc == nil {
		return Set{}, fmt.Errorf("evalset: nil doc")
	}

	if len(doc.Spec.Members) > maxEvalSetMembers {
		return Set{}, fmt.Errorf("evalset: too many members: %d (max %d)", len(doc.Spec.Members), maxEvalSetMembers)
	}

	set := Set{
		ID:         doc.Metadata.ID,
		Name:       doc.Metadata.Name,
		Version:    doc.Metadata.Version,
		AssetClass: doc.Metadata.AssetClass,
	}

	inputs, err := stringMap(doc.Spec.Inputs, "spec.inputs")
	if err != nil {
		return Set{}, err
	}
	set.Inputs = inputs

	for i, m := range doc.Spec.Members {
		bind, err := stringMap(m.Bind, fmt.Sprintf("spec.members[%d].bind", i))
		if err != nil {
			return Set{}, err
		}
		// Weight defaults to 1.0 when absent or zero. A negative weight distorts
		// weighted-mean (and thus the gate verdict), so it is rejected fail-closed,
		// naming the offending member.
		weight := 1.0
		if m.Weight != nil {
			if *m.Weight < 0 {
				return Set{}, fmt.Errorf("evalset: spec.members[%d] (metric %q): weight must be >= 0, got %v", i, m.Metric, *m.Weight)
			}
			if *m.Weight != 0 {
				weight = *m.Weight
			}
		}
		required := false
		if m.Required != nil {
			required = *m.Required
		}
		set.Members = append(set.Members, Member{
			MetricID: m.Metric,
			Weight:   weight,
			Required: required,
			Bind:     bind,
		})
	}

	// Default the aggregation method EXPLICITLY to mean; an empty/absent method
	// (including a nil aggregation block) means "mean" (design §9). A non-empty
	// unsupported method is rejected.
	set.Aggregation.Method = AggMean
	if agg := doc.Spec.Aggregation; agg != nil {
		if agg.Method != "" {
			method := AggregationMethod(agg.Method)
			if !method.supported() {
				return Set{}, fmt.Errorf("evalset: unsupported aggregation method %q (supported: %s, %s, %s)",
					agg.Method, AggMean, AggWeightedMean, AggMin)
			}
			set.Aggregation.Method = method
		}
		set.Aggregation.Threshold = agg.Threshold
		if agg.Gate != nil {
			set.Aggregation.Gate = *agg.Gate
		}
	}

	if doc.Spec.Display != nil {
		if v, ok := doc.Spec.Display["order"]; ok {
			if s, ok := v.(string); ok {
				set.DisplayOrder = s
			} else {
				return Set{}, fmt.Errorf("evalset: spec.display.order must be a string, got %T", v)
			}
		}
	}

	return set, nil
}

// supported reports whether m is one of the three Phase-1 aggregation methods.
func (m AggregationMethod) supported() bool {
	switch m {
	case AggMean, AggWeightedMean, AggMin:
		return true
	default:
		return false
	}
}

// stringMap converts a map[string]any to map[string]string, returning an error
// (naming the offending key with the given context) on any non-string value. A
// nil input yields a nil map.
func stringMap(in map[string]any, ctx string) (map[string]string, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("evalset: %s.%s must be a string, got %T", ctx, k, v)
		}
		out[k] = s
	}
	return out, nil
}

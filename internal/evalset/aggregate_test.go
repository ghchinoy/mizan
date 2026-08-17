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

import "testing"

func okMember(score float32, weight float64) MemberResult {
	return MemberResult{Status: OK, Score: f32(score), Weight: weight}
}

func TestAggregate_Methods(t *testing.T) {
	tests := []struct {
		name    string
		method  AggregationMethod
		members []MemberResult
		want    *float32
	}{
		{
			name:    "mean",
			method:  AggMean,
			members: []MemberResult{okMember(0.6, 1), okMember(0.8, 1), okMember(1.0, 1)},
			want:    f32(0.8),
		},
		{
			name:    "weighted-mean",
			method:  AggWeightedMean,
			members: []MemberResult{okMember(1.0, 3), okMember(0.0, 1)},
			// (3*1.0 + 1*0.0) / 4 = 0.75
			want: f32(0.75),
		},
		{
			name:    "min",
			method:  AggMin,
			members: []MemberResult{okMember(0.9, 1), okMember(0.4, 1), okMember(0.7, 1)},
			want:    f32(0.4),
		},
		{
			name:    "zero scored -> nil",
			method:  AggMean,
			members: []MemberResult{{Status: OK, Score: nil, Weight: 1}},
			want:    nil,
		},
		{
			name:    "non-OK excluded",
			method:  AggMean,
			members: []MemberResult{okMember(1.0, 1), {Status: Errored, Weight: 1}},
			want:    f32(1.0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregate(tc.method, nil, tc.members)
			switch {
			case tc.want == nil && got.Score != nil:
				t.Fatalf("Score = %v, want nil", *got.Score)
			case tc.want != nil && got.Score == nil:
				t.Fatalf("Score = nil, want %v", *tc.want)
			case tc.want != nil && !closef(*got.Score, *tc.want):
				t.Fatalf("Score = %v, want %v", *got.Score, *tc.want)
			}
		})
	}
}

func TestAggregate_ThresholdPassed(t *testing.T) {
	got := aggregate(AggMean, f64(0.8), []MemberResult{okMember(0.9, 1)})
	if got.Passed == nil || !*got.Passed {
		t.Fatalf("Passed = %v, want true", got.Passed)
	}
	got = aggregate(AggMean, f64(0.8), []MemberResult{okMember(0.7, 1)})
	if got.Passed == nil || *got.Passed {
		t.Fatalf("Passed = %v, want false", got.Passed)
	}
	// No threshold -> Passed is nil.
	got = aggregate(AggMean, nil, []MemberResult{okMember(0.7, 1)})
	if got.Passed != nil {
		t.Fatalf("Passed = %v, want nil (no threshold)", *got.Passed)
	}
}

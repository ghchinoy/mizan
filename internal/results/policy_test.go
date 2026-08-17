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

package results

import (
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

func TestPolicyModeFor(t *testing.T) {
	modalities := []registry.Modality{
		registry.ModalityText,
		registry.ModalityImage,
		registry.ModalityAudio,
		registry.ModalityVideo,
	}

	cases := []struct {
		name   string
		policy RetentionPolicy
		want   map[registry.Modality]RetentionMode
	}{
		{
			name:   "inline",
			policy: InlinePolicy{},
			want: map[registry.Modality]RetentionMode{
				registry.ModalityText:  ModeInline,
				registry.ModalityImage: ModeInline,
				registry.ModalityAudio: ModeInline,
				registry.ModalityVideo: ModeInline,
			},
		},
		{
			name:   "reference",
			policy: ReferencePolicy{},
			want: map[registry.Modality]RetentionMode{
				registry.ModalityText:  ModeReference,
				registry.ModalityImage: ModeReference,
				registry.ModalityAudio: ModeReference,
				registry.ModalityVideo: ModeReference,
			},
		},
		{
			name:   "hybrid",
			policy: HybridPolicy{},
			want: map[registry.Modality]RetentionMode{
				registry.ModalityText:  ModeInline,
				registry.ModalityImage: ModeReference,
				registry.ModalityAudio: ModeReference,
				registry.ModalityVideo: ModeReference,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, m := range modalities {
				got := tc.policy.ModeFor(StoredInput{Modality: m})
				if got != tc.want[m] {
					t.Errorf("%s ModeFor(%s) = %q, want %q", tc.name, m, got, tc.want[m])
				}
			}
		})
	}
}

func TestPolicyFor(t *testing.T) {
	cases := []struct {
		in   string
		want RetentionPolicy
	}{
		{"inline", InlinePolicy{}},
		{"reference", ReferencePolicy{}},
		{"hybrid", HybridPolicy{}},
		{"", HybridPolicy{}},
		{"bogus", HybridPolicy{}},
	}
	for _, tc := range cases {
		got := PolicyFor(tc.in)
		if got != tc.want {
			t.Errorf("PolicyFor(%q) = %T, want %T", tc.in, got, tc.want)
		}
	}
}

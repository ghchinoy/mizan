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

import "github.com/ghchinoy/mizan/internal/eval"

// instanceFor builds a member's eval.Instance from the SHARED set inputs by
// IDENTITY binding: a set input named X supplies the member placeholder named X.
// This is the Phase-1 form; full per-member bind-alias resolution (Member.Bind)
// is a later phase. The shared inputs are passed to every member and the engine
// validates placeholder coverage per template, so an unbound placeholder surfaces
// as the engine's own crisp error rather than being masked here.
//
// The map is copied so a member run cannot mutate the caller's shared inputs.
func instanceFor(inputs map[string]eval.AssetRef) eval.Instance {
	if inputs == nil {
		return eval.Instance{}
	}
	fields := make(map[string]eval.AssetRef, len(inputs))
	for k, v := range inputs {
		fields[k] = v
	}
	return eval.Instance{Fields: fields}
}

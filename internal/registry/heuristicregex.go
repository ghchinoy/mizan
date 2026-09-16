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

package registry

import "regexp"

// CompileHeuristicRegex compiles a heuristic regex operand with Go's regexp
// (RE2), which has no catastrophic backtracking — a deliberate ReDoS-safe
// property. When the spec is case-insensitive the RE2 (?i) flag is prepended.
// It is the single source of the pattern-assembly logic shared by the CLI
// authoring path, the pack-validate check, and the eval-run engine, so those
// sites compile identical patterns. The raw compile error is returned unwrapped
// so each caller can frame it in its own message.
func CompileHeuristicRegex(spec *HeuristicSpec) (*regexp.Regexp, error) {
	pattern := spec.Value
	if spec.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

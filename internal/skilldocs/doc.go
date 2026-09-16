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

// Package skilldocs holds the drift gate for the agent-skill docs under
// plugins/. It is the plugins/ analogue of internal/gendocs's byte-equality
// gate for docs/commands/: the skills under plugins/ document Mizan's -o json
// contracts in prose, but a hand-written JSON transcript has no mechanical tie
// to the Go structs the CLI actually serializes — which is exactly how a skill
// silently rots as the CLI moves (the failure mode ghchinoy/binder's
// internal/plugindocs drift test was built to catch after a multi-version
// silent drift).
//
// The gate (see drift_test.go) is HERMETIC by construction: it makes no network
// or ADC calls. A real `mizan eval run` needs a live LLM, which CI does not
// have, so instead of shelling out the test constructs a representative
// eval.Result, renders it through the SAME encoding the CLI's `-o json` path
// uses (encoding/json, mirroring cmd/mizan.printJSON), and asserts KEY-SET
// EQUALITY (path-anchored) against the key set documented in the run-eval
// SKILL.md fenced json block. A reflection pass over eval.Result additionally
// proves every declared (non-json:"-") field is documented, so even an
// omitempty field added to the struct fails the gate until the skill is updated.
//
// This test runs under `go test ./...` and therefore under `make check` and CI
// automatically; no credentials are required.
package skilldocs

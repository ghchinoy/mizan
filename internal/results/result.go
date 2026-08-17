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

// Package results holds the canonical, backend-portable eval Result record and
// the ResultStore seam over it. It mirrors the registry package's seam
// discipline exactly (design/eval-results-store-design.md §4.1): the domain type
// + the interface live here, concrete backends live in sub-packages
// (internal/results/sqlite), and cmd/* depends only on the results.Service
// façade — never on a backend. The store persists what mizan already computes
// (version, template ref, applied autorater, inputs, outcome) so a stored result
// is a self-describing, reproducible record for the hill-climbing loop.
package results

import (
	"time"

	"github.com/ghchinoy/mizan/internal/registry"
)

// RunKind distinguishes a single eval from the (additive, §4.6) scorecard
// member/aggregate shapes. Phase 1 persists single results only; the other
// constants are defined now so the schema is forward-compatible and needs no
// churn when the eval-set runner lands.
type RunKind string

const (
	// RunKindSingle is a single, standalone eval result (the only kind produced
	// in Phase 1).
	RunKindSingle RunKind = "single"
	// RunKindScorecardMember is one member of an eval-set run (additive, §4.6).
	RunKindScorecardMember RunKind = "scorecard-member"
	// RunKindScorecardAggregate is the aggregate record of an eval-set run
	// (additive, §4.6).
	RunKindScorecardAggregate RunKind = "scorecard-aggregate"
)

// RetentionMode selects, per input field, whether the raw content is stored
// inline or only referenced (design §4.4). The ContentHash is stored regardless.
type RetentionMode string

const (
	// ModeInline stores the raw field value in the record (text: the value;
	// media under an inline policy: the reference, since bytes are not copied).
	ModeInline RetentionMode = "inline"
	// ModeReference stores only a hash + URI/FilePath reference, never the bytes.
	ModeReference RetentionMode = "reference"
)

// Result is the canonical, backend-portable record for one eval (design §4.2).
// One record per single eval. Every field is populated from data mizan already
// has at run time.
type Result struct {
	// ── Identity ────────────────────────────────────────────────────────────
	RunID   string    // ULID (sortable-by-time, collision-free) — primary key
	RunAt   time.Time // wall-clock start of the eval (UTC)
	RunKind RunKind   // "single" now; scorecard kinds additive (§4.6)

	// ── Run metadata (reproducibility: WHO/WHAT/WHEN produced this) ──────────
	Mizan      MizanBuild // version.Info: Version, Commit, Date
	Invocation Invocation // command, project, location, host label, actor

	// ── Template ref (reuse RFC-0001 identity model — do NOT invent) ─────────
	Template TemplateRef // ID + Version + ContentHash (the exact-version anchor)

	// ── Autorater-as-applied (RESOLVED, not the template's declared value) ───
	Autorater AppliedAutorater

	// ── Rubric-provenance hook (which rubric variant produced this) ──────────
	Rubric *RubricRef `json:",omitempty"` // nil unless the template is KindRubric

	// ── Inputs (retention-mode configurable per field — §4.4) ────────────────
	Inputs []StoredInput

	// ── Outcome (reuse eval.Result's shape where sensible) ───────────────────
	Outcome Outcome
}

// MizanBuild captures the mizan build that produced a result (version.Info).
type MizanBuild struct {
	Version string
	Commit  string
	Date    string
}

// Invocation records the run context for reproducibility and team attribution.
type Invocation struct {
	Command   string // "eval run" | "eval pairwise" | (later) "eval run --set"
	ProjectID string // cfg.ProjectID at run time
	Location  string // cfg.Location requested (effective host recorded under Autorater)
	HostLabel string // os.Hostname() — coarse machine attribution (not PII-heavy)
	Actor     string // OPTIONAL: os/user or a configured label; policy-gated (§4.4), empty by default
}

// TemplateRef pins the exact template version that produced a result. ContentHash
// (RFC-0001, registry/hash.go) is the exact-version anchor: it is over the full
// canonical spec, so it distinguishes drift even without a semver bump.
type TemplateRef struct {
	ID          string // "<ns>/<slug>"
	Version     string // semver as run
	ContentHash string // registry.MetricTemplate.ContentHash — pins the EXACT spec+rubric variant
	Kind        registry.MetricKind
	Source      string // template provenance (e.g. "pack:google-brand@<origin>")
}

// AppliedAutorater is the results store's OWN denormalized record of the
// autorater as it was actually applied — RESOLVED model + effective host, not the
// template's declared value. It is deliberately DISTINCT from the (future)
// eval.AppliedAutorater the engine returns (PR-1): the caller copies the engine's
// value into this plain-data type, so the results package never depends on that
// engine field. See RecordInput.Applied.
type AppliedAutorater struct {
	Model         string // RESOLVED full/publisher-relative id actually used (post precedence chain)
	SamplingCount int32
	FlipEnabled   bool
	EffectiveHost string // "regional" | "global" (R-GLOBAL routing actually taken)
	Location      string // region the call was made against (e.g. "us-central1" or "global")
	ModelSource   string // "flag" | "template" | "config-default" | "builtin" (why this model)
}

// RubricRef records which rubric variant produced a result so a stored result is
// self-describing without re-resolving the template. The exact variant is ALREADY
// pinned by Template.ContentHash; these fields are a denormalized, human-legible
// echo, not a second source of truth.
//
// RubricProvenance-derived fields (Method=adaptive-generated, generator, recipe,
// per-criterion origins) are read from registry.MetricTemplate.RubricProvenance,
// which round-trips through the default sqlite backend as of PR #69. A nil
// RubricProvenance (hand-authored template) leaves Method="authored" and the
// provenance-derived fields empty — existing records are unaffected.
type RubricRef struct {
	Method         string   // "authored" | "adaptive-generated" (from RubricProvenance.Method; "authored" if nil/empty)
	GeneratorModel string   `json:",omitempty"` // RubricProvenance.GeneratorModel (adaptive-generated)
	Recipe         string   `json:",omitempty"` // RubricProvenance.Recipe, e.g. "general_quality_v1"
	Origins        []string `json:",omitempty"` // distinct per-criterion RubricMeta.Origin values (union-before-freeze audit; nil when none recorded)
	ScaleMin       *int     `json:",omitempty"` // RubricDetail.Scale as applied
	ScaleMax       *int     `json:",omitempty"`
	DetailMode     bool     // whether the per-criterion structured path was used
}

// StoredInput is one evaluation input field, stored per the retention policy
// (§4.4). ContentHash is ALWAYS present (dedup, diffing, integrity); the raw
// value is inline or referenced per Mode.
type StoredInput struct {
	Field       string            // placeholder name
	Modality    registry.Modality //
	ContentHash string            // SHA-256 of the value — ALWAYS present
	Mode        RetentionMode     // "inline" | "reference" (per-field, chosen by policy)
	Inline      string            `json:",omitempty"` // raw text/base64 when Mode==inline
	URI         string            `json:",omitempty"` // gs:// or file ref when Mode==reference
	MimeType    string            `json:",omitempty"`
}

// Outcome reuses eval.Result's shape as a VALUE COPY (not an import-time
// dependency inversion): results depends on eval only for these plain fields.
type Outcome struct {
	Score          *float32       `json:",omitempty"`
	PairwiseChoice string         `json:",omitempty"`
	Explanation    string         `json:",omitempty"`
	CustomOutput   map[string]any `json:",omitempty"`
	RubricDetail   bool           `json:",omitempty"`
	Warnings       []string       `json:",omitempty"`
	DurationNS     int64          // Stats.Duration
	TokenUsage     *TokenUsage    `json:",omitempty"` // genai/custom_schema path only
}

// TokenUsage is the results store's own copy of the genai-path token breakdown
// (distinct from eval.TokenUsage; copied by value in Service.Record).
type TokenUsage struct {
	PromptTokens     int32
	CandidatesTokens int32
	TotalTokens      int32
}

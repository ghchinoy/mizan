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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/version"
)

// ErrNotFound is returned when a result does not exist.
var ErrNotFound = errors.New("results: result not found")

// ResultFilter narrows List. A zero-value filter matches everything. It mirrors
// registry.ListFilter's role for the results query surface (§4.1).
type ResultFilter struct {
	TemplateID      string              // exact "<ns>/<slug>"
	TemplateVersion string              // exact semver (empty = any)
	Namespace       string              // id namespace, e.g. "google-brand"
	Kind            registry.MetricKind // exact kind (empty = any)
	Since           time.Time           // run timestamp >= Since
	Until           time.Time           // run timestamp <= Until (zero = no upper bound)
	ScorecardRunID  string              // members of one eval-set run (additive; §4.6)
	Limit           int                 // 0 = backend default (no limit)
}

// ResultStore is the local/remote persistence interface over stored eval
// results. It mirrors registry.Store precisely (Get/List/Put/Delete + a change
// primitive) so a Firestore-adjacent backend is a constructor swap in
// internal/wire.
type ResultStore interface {
	// Put inserts a result by RunID. Results are immutable: inserting an existing
	// RunID is an error (§4.5).
	Put(ctx context.Context, r *Result) error
	// Get returns the result with the given RunID, or ErrNotFound.
	Get(ctx context.Context, runID string) (*Result, error)
	// List returns results matching the filter, ordered by run timestamp (newest
	// first).
	List(ctx context.Context, f ResultFilter) ([]Result, error)
	// Delete removes a result by RunID, returning ErrNotFound if absent.
	Delete(ctx context.Context, runID string) error
	// ListChangedSince mirrors registry.Store: a cheap sync/incremental primitive.
	// Results are append-only, so this is "created at or after since".
	ListChangedSince(ctx context.Context, since time.Time) ([]Result, error)
}

// Service is the only type cmd/* depends on (mirrors registry.Service). It stamps
// RunID / RunAt / MizanBuild, applies the retention policy (§4.4), and delegates
// to the injected ResultStore.
type Service struct {
	store  ResultStore
	policy RetentionPolicy
	ver    version.Info
}

// Option configures a Service. With no options a Service uses the default hybrid
// retention policy and a zero-value version.Info.
type Option func(*Service)

// WithRetentionPolicy injects the retention policy (design §4.4). A nil policy is
// ignored (the default hybrid policy stays in effect).
func WithRetentionPolicy(p RetentionPolicy) Option {
	return func(s *Service) {
		if p != nil {
			s.policy = p
		}
	}
}

// WithVersion injects the mizan build info stamped onto every recorded Result.
func WithVersion(v version.Info) Option {
	return func(s *Service) { s.ver = v }
}

// NewService returns a Service backed by the given ResultStore. With no options
// it uses the default hybrid retention policy; wire.OpenResultService injects the
// config-derived policy and version.Get().
func NewService(store ResultStore, opts ...Option) *Service {
	s := &Service{store: store, policy: NewHybridPolicy()}
	for _, opt := range opts {
		opt(s)
	}
	if s.policy == nil {
		s.policy = NewHybridPolicy()
	}
	return s
}

// RecordInput is the domain input Service.Record builds a Result from. It carries
// plain data only: the caller (PR-3, later) supplies the applied autorater as
// results' OWN AppliedAutorater type, so this package never depends on PR-1's
// eval.Result.Applied. Outcome fields are copied from the eval.Result that exists
// on main TODAY.
type RecordInput struct {
	Command   string // "eval run" | "eval pairwise"
	ProjectID string // cfg.ProjectID at run time
	Location  string // cfg.Location requested
	HostLabel string // os.Hostname() (optional; empty by default)
	Actor     string // optional, policy-gated (empty by default)

	Template registry.MetricTemplate // the template that was run
	Instance eval.Instance           // for building StoredInput from inst.Fields
	Applied  AppliedAutorater        // results' OWN type; caller fills it (from eval.Result.Applied later)
	Outcome  eval.Result             // plain Outcome fields are copied from it

	RunAt time.Time // optional override; else Service stamps time.Now().UTC()
}

// Record builds a canonical Result from in — stamping RunID (ULID), RunAt, and
// MizanBuild, applying the retention policy per input field, and copying the
// applied autorater / outcome — then persists it via the store.
func (s *Service) Record(ctx context.Context, in RecordInput) (*Result, error) {
	runID, err := NewULID()
	if err != nil {
		return nil, err
	}

	runAt := in.RunAt
	if runAt.IsZero() {
		runAt = time.Now()
	}
	runAt = runAt.UTC()

	r := &Result{
		RunID:   runID,
		RunAt:   runAt,
		RunKind: RunKindSingle,
		Mizan: MizanBuild{
			Version: s.ver.Version,
			Commit:  s.ver.Commit,
			Date:    s.ver.Date,
		},
		Invocation: Invocation{
			Command:   in.Command,
			ProjectID: in.ProjectID,
			Location:  in.Location,
			HostLabel: in.HostLabel,
			Actor:     in.Actor,
		},
		Template: TemplateRef{
			ID:          in.Template.ID,
			Version:     in.Template.Version,
			ContentHash: in.Template.ContentHash,
			Kind:        in.Template.Kind,
			Source:      in.Template.Source,
		},
		Autorater: in.Applied,
		Rubric:    buildRubricRef(in.Template, in.Outcome.RubricDetail),
		Inputs:    s.buildInputs(in.Instance),
		Outcome:   buildOutcome(in.Outcome),
	}

	if err := s.store.Put(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// Get returns the result with the given RunID, or ErrNotFound.
func (s *Service) Get(ctx context.Context, runID string) (*Result, error) {
	return s.store.Get(ctx, runID)
}

// List returns results matching the filter (newest first).
func (s *Service) List(ctx context.Context, f ResultFilter) ([]Result, error) {
	return s.store.List(ctx, f)
}

// Delete removes a result by RunID.
func (s *Service) Delete(ctx context.Context, runID string) error {
	return s.store.Delete(ctx, runID)
}

// buildInputs turns an eval.Instance into []StoredInput, always computing the
// SHA-256 content hash and applying the retention policy per field. Fields are
// emitted in sorted name order for deterministic records.
func (s *Service) buildInputs(inst eval.Instance) []StoredInput {
	if len(inst.Fields) == 0 {
		return nil
	}
	names := make([]string, 0, len(inst.Fields))
	for name := range inst.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]StoredInput, 0, len(names))
	for _, name := range names {
		ref := inst.Fields[name]

		// The "value" is the text for text fields, else the staged/local
		// reference (gs:// URI preferred over a local path). Media bytes are never
		// read here — the hash and reference are over the URI string.
		var value, uri string
		if ref.Modality == registry.ModalityText {
			value = ref.Text
		} else {
			uri = ref.GCSUri
			if uri == "" {
				uri = ref.FilePath
			}
			value = uri
		}

		si := StoredInput{
			Field:       name,
			Modality:    ref.Modality,
			ContentHash: sha256Hex(value),
			MimeType:    ref.MimeType,
		}
		si.Mode = s.policy.ModeFor(si)
		switch si.Mode {
		case ModeInline:
			si.Inline = value
		default: // ModeReference
			si.URI = uri
		}
		out = append(out, si)
	}
	return out
}

// buildRubricRef builds the human-legible rubric reference for a rubric template.
// It returns nil for non-rubric kinds.
//
// Provenance-derived fields are read from tmpl.RubricProvenance, which round-trips
// through the default sqlite backend as of PR #69: Method/GeneratorModel/Recipe
// come from the top-level provenance, and Origins is the distinct set of
// per-criterion RubricMeta.Origin values (the union-before-freeze audit record —
// PR #74). A nil RubricProvenance means the rubric was hand-authored, so Method
// stays "authored" and the provenance-derived fields stay empty. The exact variant
// is ALREADY pinned by Template.ContentHash; these fields are a denormalized,
// human-legible echo, not a second source of truth.
func buildRubricRef(tmpl registry.MetricTemplate, detailMode bool) *RubricRef {
	if tmpl.Kind != registry.KindRubric {
		return nil
	}
	rr := &RubricRef{
		Method:     "authored",
		DetailMode: detailMode,
	}
	if p := tmpl.RubricProvenance; p != nil {
		// Do NOT change RubricProvenance.Method's semantics ("adaptive-generated"
		// etc.) — read it through as-is, falling back to "authored" only when the
		// provenance carries no method.
		if p.Method != "" {
			rr.Method = p.Method
		}
		rr.GeneratorModel = p.GeneratorModel
		rr.Recipe = p.Recipe
		rr.Origins = distinctOrigins(p.RubricMeta)
	}
	if tmpl.RubricDetail != nil && tmpl.RubricDetail.Scale != nil {
		min := tmpl.RubricDetail.Scale.Min
		max := tmpl.RubricDetail.Scale.Max
		rr.ScaleMin = &min
		rr.ScaleMax = &max
	}
	return rr
}

// distinctOrigins returns the distinct, sorted set of non-empty per-criterion
// RubricMeta.Origin values, or nil when none are recorded. A union-before-freeze
// draft (PR #74) carries a mix of "adaptive-generated" and "hand-authored"
// criteria; capturing the distinct set keeps a mixed-origin stored result honest
// without introducing a new top-level Method value (adaptive phase-3 route (i)).
func distinctOrigins(meta []registry.RubricMeta) []string {
	if len(meta) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(meta))
	out := make([]string, 0, len(meta))
	for _, m := range meta {
		if m.Origin == "" {
			continue
		}
		if _, ok := seen[m.Origin]; ok {
			continue
		}
		seen[m.Origin] = struct{}{}
		out = append(out, m.Origin)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// buildOutcome copies the plain outcome fields off eval.Result (the fields that
// exist on main today). It does NOT read eval.Result.Applied (PR-1).
func buildOutcome(res eval.Result) Outcome {
	o := Outcome{
		Score:          res.Score,
		PairwiseChoice: res.PairwiseChoice,
		Explanation:    res.Explanation,
		CustomOutput:   res.CustomOutput,
		RubricDetail:   res.RubricDetail,
		Warnings:       res.Warnings,
		DurationNS:     res.Stats.Duration.Nanoseconds(),
	}
	if tu := res.Stats.TokenUsage; tu != nil {
		o.TokenUsage = &TokenUsage{
			PromptTokens:     tu.PromptTokens,
			CandidatesTokens: tu.CandidatesTokens,
			TotalTokens:      tu.TotalTokens,
		}
	}
	return o
}

// sha256Hex returns the lowercase hex SHA-256 of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

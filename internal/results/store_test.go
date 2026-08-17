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
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/version"
)

// fakeStore is an in-memory ResultStore for exercising Service without SQLite.
type fakeStore struct {
	put []Result
}

func (f *fakeStore) Put(_ context.Context, r *Result) error {
	f.put = append(f.put, *r)
	return nil
}
func (f *fakeStore) Get(context.Context, string) (*Result, error)         { return nil, ErrNotFound }
func (f *fakeStore) List(context.Context, ResultFilter) ([]Result, error) { return nil, nil }
func (f *fakeStore) Delete(context.Context, string) error                 { return nil }
func (f *fakeStore) ListChangedSince(context.Context, time.Time) ([]Result, error) {
	return nil, nil
}

func f32(v float32) *float32 { return &v }

func TestServiceRecordBuildsResult(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs,
		WithVersion(version.Info{Version: "v1.2.3", Commit: "abc1234", Date: "2026-08-16"}),
		// default hybrid policy: text inline, media reference
	)

	runAt := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	in := RecordInput{
		Command:   "eval run",
		ProjectID: "proj-123",
		Location:  "us-central1",
		Template: registry.MetricTemplate{
			ID:          "google-brand/helpfulness",
			Version:     "1.2.3",
			ContentHash: "deadbeef",
			Kind:        registry.KindRubric,
			Source:      "pack:google-brand@origin",
			RubricDetail: &registry.RubricDetail{
				Scale: &registry.RubricScale{Min: 1, Max: 5},
			},
		},
		Instance: eval.Instance{Fields: map[string]eval.AssetRef{
			"response": {Modality: registry.ModalityText, Text: "hello world"},
			"image": {
				Modality: registry.ModalityImage,
				GCSUri:   "gs://bucket/img.png",
				MimeType: "image/png",
			},
		}},
		Applied: AppliedAutorater{
			Model:         "gemini-2.5-flash",
			SamplingCount: 4,
			FlipEnabled:   true,
			EffectiveHost: "regional",
			Location:      "us-central1",
			ModelSource:   "template",
		},
		Outcome: eval.Result{
			Score:        f32(0.9),
			Explanation:  "solid",
			RubricDetail: true,
			Warnings:     []string{"note"},
			Stats: eval.Stats{
				Duration:   1500 * time.Millisecond,
				TokenUsage: &eval.TokenUsage{PromptTokens: 10, CandidatesTokens: 20, TotalTokens: 30},
			},
		},
		RunAt: runAt,
	}

	got, err := svc.Record(context.Background(), in)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(fs.put) != 1 {
		t.Fatalf("store received %d puts, want 1", len(fs.put))
	}

	// Identity + metadata.
	if len(got.RunID) != 26 {
		t.Errorf("RunID = %q, want a 26-char ULID", got.RunID)
	}
	if !got.RunAt.Equal(runAt) {
		t.Errorf("RunAt = %v, want %v", got.RunAt, runAt)
	}
	if got.RunKind != RunKindSingle {
		t.Errorf("RunKind = %q, want %q", got.RunKind, RunKindSingle)
	}
	wantBuild := MizanBuild{Version: "v1.2.3", Commit: "abc1234", Date: "2026-08-16"}
	if got.Mizan != wantBuild {
		t.Errorf("Mizan = %+v, want %+v", got.Mizan, wantBuild)
	}
	if got.Invocation.Command != "eval run" || got.Invocation.ProjectID != "proj-123" || got.Invocation.Location != "us-central1" {
		t.Errorf("Invocation = %+v", got.Invocation)
	}

	// TemplateRef.
	wantTmpl := TemplateRef{
		ID: "google-brand/helpfulness", Version: "1.2.3", ContentHash: "deadbeef",
		Kind: registry.KindRubric, Source: "pack:google-brand@origin",
	}
	if got.Template != wantTmpl {
		t.Errorf("Template = %+v, want %+v", got.Template, wantTmpl)
	}

	// Autorater copied verbatim.
	if got.Autorater != in.Applied {
		t.Errorf("Autorater = %+v, want %+v", got.Autorater, in.Applied)
	}

	// RubricRef: authored + scale from RubricDetail.
	if got.Rubric == nil {
		t.Fatal("Rubric is nil, want non-nil for KindRubric")
	}
	if got.Rubric.Method != "authored" {
		t.Errorf("Rubric.Method = %q, want authored", got.Rubric.Method)
	}
	if got.Rubric.ScaleMin == nil || *got.Rubric.ScaleMin != 1 || got.Rubric.ScaleMax == nil || *got.Rubric.ScaleMax != 5 {
		t.Errorf("Rubric scale = %v..%v, want 1..5", got.Rubric.ScaleMin, got.Rubric.ScaleMax)
	}
	if !got.Rubric.DetailMode {
		t.Error("Rubric.DetailMode = false, want true (Outcome.RubricDetail)")
	}

	// Inputs: sorted by field name; hashing + inline/reference per hybrid policy.
	if len(got.Inputs) != 2 {
		t.Fatalf("Inputs len = %d, want 2", len(got.Inputs))
	}
	// Sorted: "image" before "response".
	img := got.Inputs[0]
	if img.Field != "image" {
		t.Fatalf("Inputs[0].Field = %q, want image (sorted)", img.Field)
	}
	if img.Mode != ModeReference {
		t.Errorf("image Mode = %q, want reference (hybrid)", img.Mode)
	}
	if img.URI != "gs://bucket/img.png" || img.Inline != "" {
		t.Errorf("image URI=%q Inline=%q, want URI set, Inline empty", img.URI, img.Inline)
	}
	if img.ContentHash != sha256Hex("gs://bucket/img.png") {
		t.Errorf("image ContentHash mismatch: %q", img.ContentHash)
	}
	if img.MimeType != "image/png" {
		t.Errorf("image MimeType = %q, want image/png", img.MimeType)
	}
	resp := got.Inputs[1]
	if resp.Field != "response" {
		t.Fatalf("Inputs[1].Field = %q, want response", resp.Field)
	}
	if resp.Mode != ModeInline {
		t.Errorf("response Mode = %q, want inline (hybrid)", resp.Mode)
	}
	if resp.Inline != "hello world" || resp.URI != "" {
		t.Errorf("response Inline=%q URI=%q, want Inline set, URI empty", resp.Inline, resp.URI)
	}
	if resp.ContentHash != sha256Hex("hello world") {
		t.Errorf("response ContentHash mismatch: %q", resp.ContentHash)
	}

	// Outcome copy.
	if got.Outcome.Score == nil || *got.Outcome.Score != 0.9 {
		t.Errorf("Outcome.Score = %v, want 0.9", got.Outcome.Score)
	}
	if got.Outcome.Explanation != "solid" {
		t.Errorf("Outcome.Explanation = %q", got.Outcome.Explanation)
	}
	if got.Outcome.DurationNS != int64(1500*time.Millisecond) {
		t.Errorf("Outcome.DurationNS = %d, want %d", got.Outcome.DurationNS, int64(1500*time.Millisecond))
	}
	if got.Outcome.TokenUsage == nil || got.Outcome.TokenUsage.TotalTokens != 30 {
		t.Errorf("Outcome.TokenUsage = %+v, want TotalTokens 30", got.Outcome.TokenUsage)
	}
	if len(got.Outcome.Warnings) != 1 || got.Outcome.Warnings[0] != "note" {
		t.Errorf("Outcome.Warnings = %v", got.Outcome.Warnings)
	}
}

// TestServiceRecordRubricRefFromProvenance proves the RubricRef reads
// RubricProvenance.Method/GeneratorModel/Recipe (round-trippable as of PR #69)
// instead of unconditionally defaulting Method to "authored".
func TestServiceRecordRubricRefFromProvenance(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs)
	got, err := svc.Record(context.Background(), RecordInput{
		Command: "eval run",
		Template: registry.MetricTemplate{
			ID:   "ns/adaptive",
			Kind: registry.KindRubric,
			RubricProvenance: &registry.RubricProvenance{
				Method:         "adaptive-generated",
				GeneratorModel: "gemini-2.5-pro",
				Recipe:         "general_quality_v1",
			},
		},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got.Rubric == nil {
		t.Fatal("Rubric is nil, want non-nil for KindRubric")
	}
	if got.Rubric.Method != "adaptive-generated" {
		t.Errorf("Rubric.Method = %q, want adaptive-generated (from RubricProvenance, not the authored default)", got.Rubric.Method)
	}
	if got.Rubric.GeneratorModel != "gemini-2.5-pro" {
		t.Errorf("Rubric.GeneratorModel = %q, want gemini-2.5-pro", got.Rubric.GeneratorModel)
	}
	if got.Rubric.Recipe != "general_quality_v1" {
		t.Errorf("Rubric.Recipe = %q, want general_quality_v1", got.Rubric.Recipe)
	}
}

// TestServiceRecordRubricRefAuthoredWhenNoProvenance proves a hand-authored rubric
// (nil RubricProvenance) still records Method="authored" — the existing default
// semantics are unchanged for templates without provenance.
func TestServiceRecordRubricRefAuthoredWhenNoProvenance(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs)
	got, err := svc.Record(context.Background(), RecordInput{
		Command:  "eval run",
		Template: registry.MetricTemplate{ID: "ns/authored", Kind: registry.KindRubric},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got.Rubric == nil || got.Rubric.Method != "authored" {
		t.Errorf("Rubric = %+v, want Method=authored for a nil-provenance rubric", got.Rubric)
	}
	if len(got.Rubric.Origins) != 0 {
		t.Errorf("Rubric.Origins = %v, want empty for a nil-provenance rubric", got.Rubric.Origins)
	}
}

// TestServiceRecordRubricRefMixedOrigins proves a union-before-freeze draft
// (mixed per-criterion RubricMeta.Origin — PR #74) surfaces the distinct, sorted
// origin set on the stored RubricRef, keeping a mixed-origin record honest.
func TestServiceRecordRubricRefMixedOrigins(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs)
	got, err := svc.Record(context.Background(), RecordInput{
		Command: "eval run",
		Template: registry.MetricTemplate{
			ID:   "ns/union",
			Kind: registry.KindRubric,
			RubricProvenance: &registry.RubricProvenance{
				Method: "adaptive-generated",
				RubricMeta: []registry.RubricMeta{
					{Group: "g", Criterion: "c1", Origin: registry.OriginAdaptiveGenerated},
					{Group: "g", Criterion: "c2", Origin: registry.OriginHandAuthored},
					// A duplicate origin must be deduplicated.
					{Group: "g", Criterion: "c3", Origin: registry.OriginAdaptiveGenerated},
					// An empty origin must be ignored.
					{Group: "g", Criterion: "c4"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got.Rubric == nil {
		t.Fatal("Rubric is nil, want non-nil")
	}
	want := []string{registry.OriginAdaptiveGenerated, registry.OriginHandAuthored} // sorted
	if len(got.Rubric.Origins) != len(want) {
		t.Fatalf("Rubric.Origins = %v, want %v", got.Rubric.Origins, want)
	}
	for i := range want {
		if got.Rubric.Origins[i] != want[i] {
			t.Errorf("Rubric.Origins[%d] = %q, want %q (distinct + sorted)", i, got.Rubric.Origins[i], want[i])
		}
	}
}

func TestServiceRecordNonRubricNoRubricRef(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs)
	got, err := svc.Record(context.Background(), RecordInput{
		Command:  "eval run",
		Template: registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got.Rubric != nil {
		t.Errorf("Rubric = %+v, want nil for non-rubric kind", got.Rubric)
	}
}

func TestServiceRecordStampsRunAtWhenZero(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs)
	before := time.Now().UTC()
	got, err := svc.Record(context.Background(), RecordInput{Template: registry.MetricTemplate{ID: "ns/x"}})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got.RunAt.Before(before) || got.RunAt.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("RunAt = %v, want a recent stamped time", got.RunAt)
	}
	if got.RunAt.Location() != time.UTC {
		t.Errorf("RunAt location = %v, want UTC", got.RunAt.Location())
	}
}

func TestServiceRecordReferencePolicyDropsInline(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs, WithRetentionPolicy(ReferencePolicy{}))
	got, err := svc.Record(context.Background(), RecordInput{
		Template: registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
		Instance: eval.Instance{Fields: map[string]eval.AssetRef{
			"response": {Modality: registry.ModalityText, Text: "secret"},
		}},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(got.Inputs) != 1 {
		t.Fatalf("Inputs len = %d, want 1", len(got.Inputs))
	}
	si := got.Inputs[0]
	if si.Mode != ModeReference {
		t.Errorf("Mode = %q, want reference", si.Mode)
	}
	if si.Inline != "" {
		t.Errorf("Inline = %q, want empty under reference policy", si.Inline)
	}
	// ContentHash is ALWAYS present, even under reference.
	if si.ContentHash != sha256Hex("secret") {
		t.Errorf("ContentHash = %q, want sha256(secret)", si.ContentHash)
	}
}

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
	"errors"
	"testing"
	"time"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// storingFake is a ResultStore that actually retains what it is given, so the
// Service's Get/List/Delete passthroughs can be exercised directly (the primary
// fakeStore in store_test.go only records Puts).
type storingFake struct {
	byID        map[string]Result
	putErr      error
	lastFilter  ResultFilter
	lastGet     string
	lastDeleted string
}

func newStoringFake() *storingFake { return &storingFake{byID: map[string]Result{}} }

func (f *storingFake) Put(_ context.Context, r *Result) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.byID[r.RunID] = *r
	return nil
}

func (f *storingFake) Get(_ context.Context, runID string) (*Result, error) {
	f.lastGet = runID
	r, ok := f.byID[runID]
	if !ok {
		return nil, ErrNotFound
	}
	return &r, nil
}

func (f *storingFake) List(_ context.Context, filter ResultFilter) ([]Result, error) {
	f.lastFilter = filter
	out := make([]Result, 0, len(f.byID))
	for _, r := range f.byID {
		out = append(out, r)
	}
	return out, nil
}

func (f *storingFake) Delete(_ context.Context, runID string) error {
	f.lastDeleted = runID
	if _, ok := f.byID[runID]; !ok {
		return ErrNotFound
	}
	delete(f.byID, runID)
	return nil
}

func (f *storingFake) ListChangedSince(context.Context, time.Time) ([]Result, error) {
	return nil, nil
}

// TestServiceGetListDeletePassthrough proves the Service delegates Get/List/
// Delete verbatim to the injected store (arguments forwarded, results/errors
// returned unchanged) — the public read/delete surface cmd/* relies on.
func TestServiceGetListDeletePassthrough(t *testing.T) {
	fs := newStoringFake()
	svc := NewService(fs)
	ctx := context.Background()

	rec, err := svc.Record(ctx, RecordInput{Template: registry.MetricTemplate{ID: "ns/x"}})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Get forwards the RunID and returns the stored record.
	got, err := svc.Get(ctx, rec.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fs.lastGet != rec.RunID {
		t.Errorf("store.Get called with %q, want %q", fs.lastGet, rec.RunID)
	}
	if got.RunID != rec.RunID {
		t.Errorf("Get RunID = %q, want %q", got.RunID, rec.RunID)
	}

	// Get on a missing id surfaces ErrNotFound unchanged.
	if _, err := svc.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) err = %v, want ErrNotFound", err)
	}

	// List forwards the filter and returns the store's rows.
	want := ResultFilter{TemplateID: "ns/x", Limit: 5}
	list, err := svc.List(ctx, want)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if fs.lastFilter != want {
		t.Errorf("store.List filter = %+v, want %+v", fs.lastFilter, want)
	}
	if len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}

	// Delete forwards the RunID and removes the record.
	if err := svc.Delete(ctx, rec.RunID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if fs.lastDeleted != rec.RunID {
		t.Errorf("store.Delete called with %q, want %q", fs.lastDeleted, rec.RunID)
	}
	if _, err := svc.Get(ctx, rec.RunID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete err = %v, want ErrNotFound", err)
	}
	// Delete of an absent id returns ErrNotFound unchanged.
	if err := svc.Delete(ctx, rec.RunID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(absent) err = %v, want ErrNotFound", err)
	}
}

// TestServiceRecordPropagatesPutError proves a store failure surfaces from
// Record (the record is not silently dropped) and no result is returned.
func TestServiceRecordPropagatesPutError(t *testing.T) {
	sentinel := errors.New("boom")
	fs := &storingFake{byID: map[string]Result{}, putErr: sentinel}
	svc := NewService(fs)

	got, err := svc.Record(context.Background(), RecordInput{Template: registry.MetricTemplate{ID: "ns/x"}})
	if !errors.Is(err, sentinel) {
		t.Errorf("Record err = %v, want the store's Put error", err)
	}
	if got != nil {
		t.Errorf("Record result = %+v, want nil on store failure", got)
	}
}

// TestServiceRecordMediaModalitiesReferenced proves the default hybrid policy
// routes EACH media modality (image/audio/video) to reference mode with the raw
// bytes dropped, while text stays inline — exercising Record end-to-end across
// all modalities (the policy is unit-tested separately in policy_test.go).
func TestServiceRecordMediaModalitiesReferenced(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs) // default hybrid

	in := RecordInput{
		Template: registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
		Instance: eval.Instance{Fields: map[string]eval.AssetRef{
			"a_text":  {Modality: registry.ModalityText, Text: "hello"},
			"b_image": {Modality: registry.ModalityImage, GCSUri: "gs://b/i.png", MimeType: "image/png"},
			"c_audio": {Modality: registry.ModalityAudio, GCSUri: "gs://b/a.mp3", MimeType: "audio/mpeg"},
			"d_video": {Modality: registry.ModalityVideo, GCSUri: "gs://b/v.mp4", MimeType: "video/mp4"},
		}},
	}
	got, err := svc.Record(context.Background(), in)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	byField := map[string]StoredInput{}
	for _, si := range got.Inputs {
		byField[si.Field] = si
	}

	// Text: inline, value retained.
	if txt := byField["a_text"]; txt.Mode != ModeInline || txt.Inline != "hello" || txt.URI != "" {
		t.Errorf("text input = %+v, want inline with value retained", txt)
	}
	// Each media modality: reference, URI kept, raw bytes/inline dropped, hash present.
	for _, tc := range []struct {
		field string
		uri   string
	}{
		{"b_image", "gs://b/i.png"},
		{"c_audio", "gs://b/a.mp3"},
		{"d_video", "gs://b/v.mp4"},
	} {
		si := byField[tc.field]
		if si.Mode != ModeReference {
			t.Errorf("%s Mode = %q, want reference (hybrid)", tc.field, si.Mode)
		}
		if si.URI != tc.uri || si.Inline != "" {
			t.Errorf("%s URI=%q Inline=%q, want URI=%q, Inline empty", tc.field, si.URI, si.Inline, tc.uri)
		}
		if si.ContentHash != sha256Hex(tc.uri) {
			t.Errorf("%s ContentHash mismatch, want sha256 over the reference", tc.field)
		}
	}
}

// TestServiceRecordFilePathFallback proves that when a media field has no GCS
// URI, the local FilePath is used as the reference (and the content hash is
// computed over that FilePath) — the else-branch of buildInputs's reference
// selection.
func TestServiceRecordFilePathFallback(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs) // hybrid → media referenced

	got, err := svc.Record(context.Background(), RecordInput{
		Template: registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
		Instance: eval.Instance{Fields: map[string]eval.AssetRef{
			"image": {Modality: registry.ModalityImage, FilePath: "/local/img.png", MimeType: "image/png"},
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
	if si.URI != "/local/img.png" {
		t.Errorf("URI = %q, want the FilePath fallback", si.URI)
	}
	if si.ContentHash != sha256Hex("/local/img.png") {
		t.Errorf("ContentHash = %q, want sha256 over the FilePath", si.ContentHash)
	}
}

// TestNewServiceNilPolicyDefaultsToHybrid proves both nil-policy guards: a nil
// WithRetentionPolicy is ignored, and the default remains the hybrid policy
// (text inline, media referenced).
func TestNewServiceNilPolicyDefaultsToHybrid(t *testing.T) {
	fs := &fakeStore{}
	svc := NewService(fs, WithRetentionPolicy(nil)) // nil must be ignored

	got, err := svc.Record(context.Background(), RecordInput{
		Template: registry.MetricTemplate{ID: "ns/x", Kind: registry.KindPointwise},
		Instance: eval.Instance{Fields: map[string]eval.AssetRef{
			"response": {Modality: registry.ModalityText, Text: "kept"},
			"image":    {Modality: registry.ModalityImage, GCSUri: "gs://b/i.png"},
		}},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	byField := map[string]StoredInput{}
	for _, si := range got.Inputs {
		byField[si.Field] = si
	}
	if m := byField["response"].Mode; m != ModeInline {
		t.Errorf("text Mode = %q, want inline (hybrid default survived nil policy)", m)
	}
	if m := byField["image"].Mode; m != ModeReference {
		t.Errorf("image Mode = %q, want reference (hybrid default survived nil policy)", m)
	}
}

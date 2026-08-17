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

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// These tests cover the GAP B CLI plumbing: a media pairwise must be expressible
// by filling the baseline/candidate slots via --gcs/--file (buildInstance +
// requirePairwiseFields), and a media-looking value in a TEXT slot must be a hard
// error (guardTextSlot). Text pairwise and the pointwise/run path stay unchanged.

// --- 1. plumbing: --gcs / --file fill baseline/candidate, no dup-key -----------

func TestBuildInstance_MediaViaGCSFillsFields(t *testing.T) {
	inst, err := buildInstance(nil, nil, []string{
		"baseline_ad=gs://bucket/a.mp4",
		"candidate_ad=gs://bucket/b.mp4",
	})
	if err != nil {
		t.Fatalf("buildInstance returned error: %v", err)
	}
	b, ok := inst.Fields["baseline_ad"]
	if !ok {
		t.Fatalf("baseline_ad missing from fields")
	}
	if b.GCSUri != "gs://bucket/a.mp4" {
		t.Errorf("baseline_ad GCSUri = %q, want gs://bucket/a.mp4", b.GCSUri)
	}
	if b.Text != "" || b.Modality != "" {
		t.Errorf("baseline_ad should be a media ref, got Text=%q Modality=%q", b.Text, b.Modality)
	}
	c, ok := inst.Fields["candidate_ad"]
	if !ok || c.GCSUri != "gs://bucket/b.mp4" {
		t.Errorf("candidate_ad GCSUri = %q (ok=%v), want gs://bucket/b.mp4", c.GCSUri, ok)
	}
}

func TestBuildInstance_MediaViaFileFillsFields(t *testing.T) {
	inst, err := buildInstance(nil, []string{
		"baseline_ad=/tmp/a.mp4",
		"candidate_ad=/tmp/b.mp4",
	}, nil)
	if err != nil {
		t.Fatalf("buildInstance returned error: %v", err)
	}
	if got := inst.Fields["baseline_ad"].FilePath; got != "/tmp/a.mp4" {
		t.Errorf("baseline_ad FilePath = %q, want /tmp/a.mp4", got)
	}
	if got := inst.Fields["candidate_ad"].FilePath; got != "/tmp/b.mp4" {
		t.Errorf("candidate_ad FilePath = %q, want /tmp/b.mp4", got)
	}
}

func TestBuildInstance_DuplicateKeyStillErrors(t *testing.T) {
	// Reusing a key across a text flag and --gcs still collides — a slot is either
	// text OR media, never both.
	_, err := buildInstance(
		[]string{"baseline_ad=some text"},
		nil,
		[]string{"baseline_ad=gs://bucket/a.mp4"},
	)
	if err == nil {
		t.Fatal("expected duplicate-key error, got nil")
	}
	if got := err.Error(); got != `duplicate field key "baseline_ad"` {
		t.Errorf("error = %q, want %q", got, `duplicate field key "baseline_ad"`)
	}
}

func TestBuildInstance_TextFieldUnchanged(t *testing.T) {
	inst, err := buildInstance([]string{"answer=42 is the answer"}, nil, nil)
	if err != nil {
		t.Fatalf("buildInstance returned error: %v", err)
	}
	ref := inst.Fields["answer"]
	if ref.Modality != registry.ModalityText {
		t.Errorf("modality = %q, want %q", ref.Modality, registry.ModalityText)
	}
	if ref.Text != "42 is the answer" {
		t.Errorf("text = %q, want %q", ref.Text, "42 is the answer")
	}
}

// --- 2a. guard: gs:// in a TEXT slot is ALWAYS a hard error --------------------

func TestGuardTextSlot_GSURIErrors(t *testing.T) {
	err := guardTextSlot("baseline", "baseline_ad", "gs://bucket/google_home_celebrity_ad.mp4")
	if err == nil {
		t.Fatal("expected error for gs:// value in text slot, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"--baseline baseline_ad=<value> looks like a gs:// media URI passed as TEXT",
		"Use --gcs baseline_ad=gs://bucket/google_home_celebrity_ad.mp4",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestBuildInstance_GSURIInTextFieldErrors(t *testing.T) {
	// The same guard fires for --field (the pointwise/extra text slot).
	_, err := buildInstance([]string{"clip=gs://bucket/x.mp4"}, nil, nil)
	if err == nil {
		t.Fatal("expected error for gs:// in --field, got nil")
	}
	if !strings.Contains(err.Error(), "--field clip=<value> looks like a gs:// media URI passed as TEXT") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- 2b. guard: a local media path in a TEXT slot is a hard error --------------

func TestGuardTextSlot_LocalMediaPathErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(p, []byte("not really a video"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	err := guardTextSlot("candidate", "candidate_ad", p)
	if err == nil {
		t.Fatal("expected error for existing local media file in text slot, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "--candidate candidate_ad=<value> looks like a local media file") {
		t.Errorf("unexpected error: %v", msg)
	}
	if !strings.Contains(msg, "Use --file candidate_ad="+p) {
		t.Errorf("error %q should point to --file with the path", msg)
	}
}

// --- 2c. guard: NO false positives on ordinary prose --------------------------

func TestGuardTextSlot_NoFalsePositiveOnProse(t *testing.T) {
	cases := []string{
		"The quick brown fox jumps over the lazy dog.",
		"See the attached diagram.png for details.", // mentions a filename, not a path
		"nonexistent.mp4", // media extension but no such file on disk
		"A well-written answer explaining big O notation.",
		"Use gsutil to copy files (not a gs:// URI).",
		"", // empty text is legitimate here (requirement check handles absence)
	}
	for _, v := range cases {
		if err := guardTextSlot("field", "answer", v); err != nil {
			t.Errorf("guardTextSlot false-positive on %q: %v", v, err)
		}
	}
}

func TestGuardTextSlot_NewlyAddedExtensionsErrorWhenFileExists(t *testing.T) {
	// The extension set was expanded (Consider-1): each newly-added extension must
	// hard-error when it names an EXISTING local file in a text slot, and must NOT
	// false-positive when the path does not exist.
	dir := t.TempDir()
	// key is chosen to exercise all three text slots (--baseline/--candidate/--field).
	slots := []struct{ flag, key string }{
		{"baseline", "baseline_ad"},
		{"candidate", "candidate_ad"},
		{"field", "clip"},
	}
	newExts := []string{".tif", ".tiff", ".heic", ".heif", ".opus", ".mpeg", ".mpg", ".3gp", ".wmv", ".flv"}
	for i, ext := range newExts {
		slot := slots[i%len(slots)]
		p := filepath.Join(dir, "asset"+ext)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		// existing file with a media extension -> hard error
		if err := guardTextSlot(slot.flag, slot.key, p); err == nil {
			t.Errorf("guardTextSlot(%s, %s) should error for existing %s file", slot.flag, slot.key, ext)
		} else if !strings.Contains(err.Error(), "looks like a local media file") {
			t.Errorf("ext %s: unexpected error: %v", ext, err)
		}
		// non-existent path with the same extension -> no false positive
		missing := filepath.Join(dir, "does-not-exist"+ext)
		if err := guardTextSlot(slot.flag, slot.key, missing); err != nil {
			t.Errorf("guardTextSlot false-positive on non-existent %s path %q: %v", ext, missing, err)
		}
	}
}

func TestGuardTextSlot_DirectoryWithMediaExtNotBlocked(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "album.mp3") // a DIRECTORY whose name ends .mp3
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := guardTextSlot("field", "note", sub); err != nil {
		t.Errorf("guardTextSlot should not block a directory: %v", err)
	}
}

// --- 3. requirePairwiseFields: satisfied by ANY source ------------------------

func pairwiseTmpl() registry.MetricTemplate {
	return registry.MetricTemplate{
		BaselineFieldName:  "baseline_ad",
		CandidateFieldName: "candidate_ad",
	}
}

func TestRequirePairwiseFields_TextSatisfies(t *testing.T) {
	inst := eval.Instance{Fields: map[string]eval.AssetRef{
		"baseline_ad":  {Modality: registry.ModalityText, Text: "A"},
		"candidate_ad": {Modality: registry.ModalityText, Text: "B"},
	}}
	if err := requirePairwiseFields(pairwiseTmpl(), inst); err != nil {
		t.Errorf("text pairwise should satisfy requirement: %v", err)
	}
}

func TestRequirePairwiseFields_MediaSatisfies(t *testing.T) {
	inst := eval.Instance{Fields: map[string]eval.AssetRef{
		"baseline_ad":  {GCSUri: "gs://b/a.mp4"},
		"candidate_ad": {GCSUri: "gs://b/b.mp4"},
	}}
	if err := requirePairwiseFields(pairwiseTmpl(), inst); err != nil {
		t.Errorf("media pairwise should satisfy requirement: %v", err)
	}
}

func TestRequirePairwiseFields_MixedTextAndMediaSatisfies(t *testing.T) {
	// One text slot, one media slot — supported.
	inst := eval.Instance{Fields: map[string]eval.AssetRef{
		"baseline_ad":  {Modality: registry.ModalityText, Text: "A text response"},
		"candidate_ad": {GCSUri: "gs://b/b.mp4"},
	}}
	if err := requirePairwiseFields(pairwiseTmpl(), inst); err != nil {
		t.Errorf("mixed pairwise should satisfy requirement: %v", err)
	}
}

func TestRequirePairwiseFields_MissingBaselineErrors(t *testing.T) {
	inst := eval.Instance{Fields: map[string]eval.AssetRef{
		"candidate_ad": {GCSUri: "gs://b/b.mp4"},
	}}
	err := requirePairwiseFields(pairwiseTmpl(), inst)
	if err == nil {
		t.Fatal("expected error when baseline field absent, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `baseline field "baseline_ad"`) {
		t.Errorf("error %q should name the missing baseline field", msg)
	}
	if strings.Contains(msg, "candidate field") {
		t.Errorf("error %q should not report candidate as missing", msg)
	}
	if !strings.Contains(msg, "--gcs KEY=gs://") || !strings.Contains(msg, "--baseline/--candidate KEY=value") {
		t.Errorf("error %q should point to both text and media sources", msg)
	}
}

func TestRequirePairwiseFields_MissingBothErrors(t *testing.T) {
	err := requirePairwiseFields(pairwiseTmpl(), eval.Instance{Fields: map[string]eval.AssetRef{}})
	if err == nil {
		t.Fatal("expected error when both fields absent, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, `baseline field "baseline_ad"`) || !strings.Contains(msg, `candidate field "candidate_ad"`) {
		t.Errorf("error %q should name both missing fields", msg)
	}
}

func TestRequirePairwiseFields_EmptyFieldNamesDeferredToEngine(t *testing.T) {
	// A malformed template (empty field names) is the engine's guard to enforce;
	// the CLI must not invent a spurious error naming an empty field.
	if err := requirePairwiseFields(registry.MetricTemplate{}, eval.Instance{Fields: map[string]eval.AssetRef{}}); err != nil {
		t.Errorf("empty field names should be deferred to the engine, got: %v", err)
	}
}

// --- 4. pointwise/run path unaffected -----------------------------------------

func TestBuildInstance_PointwiseMixedSourcesUnaffected(t *testing.T) {
	inst, err := buildInstance(
		[]string{"question=How do I speed up Python?"},
		[]string{"local=/tmp/pic.png"},
		[]string{"remote=gs://bucket/vid.mp4"},
	)
	if err != nil {
		t.Fatalf("buildInstance returned error: %v", err)
	}
	if inst.Fields["question"].Text != "How do I speed up Python?" {
		t.Errorf("text field altered: %+v", inst.Fields["question"])
	}
	if inst.Fields["local"].FilePath != "/tmp/pic.png" {
		t.Errorf("file field altered: %+v", inst.Fields["local"])
	}
	if inst.Fields["remote"].GCSUri != "gs://bucket/vid.mp4" {
		t.Errorf("gcs field altered: %+v", inst.Fields["remote"])
	}
}

func TestParseFields_TextOnlyUnchanged(t *testing.T) {
	inst, err := parseFields([]string{"a=1", "b=two"})
	if err != nil {
		t.Fatalf("parseFields returned error: %v", err)
	}
	if inst.Fields["a"].Text != "1" || inst.Fields["b"].Text != "two" {
		t.Errorf("parseFields altered text values: %+v", inst.Fields)
	}
}

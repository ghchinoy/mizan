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

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestGitPackBackendLoadPacksTree loads a repo/tree that contains a packs/ dir.
func TestGitPackBackendLoadPacksTree(t *testing.T) {
	b := NewGitPackBackend("testdata", NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ts) != 1 {
		t.Fatalf("loaded %d templates, want 1", len(ts))
	}
	if ts[0].ID != "google-brand/video-brand-alignment" {
		t.Errorf("ID = %q", ts[0].ID)
	}
	if got := b.Describe(); got.Type != "local" || got.Origin != "testdata" {
		t.Errorf("Describe() = %+v", got)
	}
}

// TestGitPackBackendLoadSinglePackDir loads a single pack directory (one that
// contains templates/, with no enclosing packs/).
func TestGitPackBackendLoadSinglePackDir(t *testing.T) {
	b := NewGitPackBackend("testdata/packs/google-brand", NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ts) != 1 {
		t.Fatalf("loaded %d templates, want 1", len(ts))
	}
}

func TestGitPackBackendLoadRejectsNonDir(t *testing.T) {
	b := NewGitPackBackend(fixtureTemplate, NewYAMLCodec(), SyncConfig{})
	if _, err := b.Load(context.Background()); err == nil {
		t.Fatal("expected error loading a non-directory source")
	}
}

// TestGitPackBackendSaveWritesTemplates covers P2.4 export write: Save creates
// templates/ and writes one file per template, deriving the filename from the
// validated slug (never the raw id), and rejects an id that fails the shape
// guard. (The full Service.Export + round-trip acceptance lives in export_test.go.)
func TestGitPackBackendSaveWritesTemplates(t *testing.T) {
	dst := t.TempDir()
	b := NewGitPackBackend(dst, NewYAMLCodec(), SyncConfig{})

	// nil templates: creates the dir, writes nothing, no error.
	if err := b.Save(context.Background(), nil); err != nil {
		t.Fatalf("Save(nil): %v", err)
	}

	if err := b.Save(context.Background(), []MetricTemplate{
		{ID: "acme/quality", Kind: KindPointwise},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "templates", "quality.yaml")); err != nil {
		t.Fatalf("expected templates/quality.yaml: %v", err)
	}

	// A malformed id is rejected at the path-derivation chokepoint (audit rec#3).
	if err := b.Save(context.Background(), []MetricTemplate{
		{ID: "../evil", Kind: KindPointwise},
	}); err == nil {
		t.Fatal("Save accepted a malformed id; want rejection")
	}
}

// TestGitCloneSeamIsInjectable proves the git shell-out seam is a swappable
// package-level var so P2.5 unit tests substitute a fake and never touch the
// network. It restores the real functions after the test.
func TestGitCloneSeamIsInjectable(t *testing.T) {
	origClone, origPull := gitClone, gitPull
	t.Cleanup(func() { gitClone, gitPull = origClone, origPull })

	var cloned, pulled bool
	gitClone = func(_ context.Context, _, _ string) error { cloned = true; return nil }
	gitPull = func(_ context.Context, _ string) error { pulled = true; return nil }

	if err := gitClone(context.Background(), "https://example.com/x.git", t.TempDir()); err != nil {
		t.Fatalf("fake gitClone: %v", err)
	}
	if err := gitPull(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("fake gitPull: %v", err)
	}
	if !cloned || !pulled {
		t.Fatalf("seam not exercised: cloned=%v pulled=%v", cloned, pulled)
	}
}

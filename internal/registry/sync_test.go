package registry

import (
	"context"
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

func TestGitPackBackendSaveNotImplemented(t *testing.T) {
	b := NewGitPackBackend("testdata", NewYAMLCodec(), SyncConfig{})
	if err := b.Save(context.Background(), nil); err == nil {
		t.Fatal("Save should report not-implemented in P2.1")
	}
}

// TestGitCloneSeamNotWired proves the git shell-out seam exists but is inert in
// P2.1 (local-only): calling it returns a clear not-implemented error, and no
// production path invokes it.
func TestGitCloneSeamNotWired(t *testing.T) {
	if err := gitClone(context.Background(), "https://example.com/x.git", t.TempDir()); err == nil {
		t.Fatal("gitClone should be a not-implemented stub in P2.1")
	}
}

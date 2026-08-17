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
	"runtime"
	"strings"
	"testing"
)

// writePackTemplate writes a minimal valid template file at
// <root>/packs/<pack>/templates/<file>.
func writePackTemplate(t *testing.T, root, pack, file, id string) {
	t.Helper()
	dir := filepath.Join(root, "packs", pack, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), packYAML(id, ""), 0o644); err != nil {
		t.Fatalf("write %q: %v", file, err)
	}
}

// --- Item 8: multi-template / multi-pack Load coverage -----------------------

// TestLoadMultiPackSortedAndTemplatelessPack loads a packs/ tree with more than
// one template across more than one pack, asserts the deterministic sort-by-id
// ordering, and confirms a pack with no templates/ dir contributes zero
// templates without erroring.
func TestLoadMultiPackSortedAndTemplatelessPack(t *testing.T) {
	root := t.TempDir()
	// pack "bravo" has two templates; deliberately write them out of order.
	writePackTemplate(t, root, "bravo", "zeta.yaml", "bravo/zeta")
	writePackTemplate(t, root, "bravo", "alpha.yaml", "bravo/alpha")
	// pack "alpha" has one template — it must sort ahead of bravo/*.
	writePackTemplate(t, root, "alpha", "one.yaml", "alpha/one")
	// pack "empty" has NO templates/ dir → zero templates, no error.
	if err := os.MkdirAll(filepath.Join(root, "packs", "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty pack: %v", err)
	}

	b := NewGitPackBackend(root, NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := make([]string, len(ts))
	for i, tp := range ts {
		got[i] = tp.ID
	}
	want := []string{"alpha/one", "bravo/alpha", "bravo/zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Load ids = %v, want sorted %v", got, want)
	}
}

// TestLoadTemplatelessSinglePack: a single pack dir with no templates/ yields
// zero templates and no error.
func TestLoadTemplatelessSinglePack(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b := NewGitPackBackend(root, NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ts) != 0 {
		t.Errorf("loaded %d templates, want 0", len(ts))
	}
}

// --- MED-1: file-size cap ----------------------------------------------------

func TestLoadRejectsOversizedTemplate(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "packs", "big", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A file one byte over the cap must be rejected (padded YAML comment so the
	// content itself is otherwise valid-ish; the size guard fires first).
	blob := append(packYAML("big/one", ""), []byte("\n# "+strings.Repeat("x", int(MaxTemplateFileBytes)))...)
	if err := os.WriteFile(filepath.Join(dir, "one.yaml"), blob, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := NewGitPackBackend(root, NewYAMLCodec(), SyncConfig{})
	_, err := b.Load(context.Background())
	if err == nil {
		t.Fatal("expected oversized template to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "size cap") {
		t.Errorf("error = %q, want it to mention the size cap", err)
	}
}

// --- MED-2: symlink escape ---------------------------------------------------

func TestLoadSkipsSymlinkedTemplate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "packs", "sym", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A real, valid template that MUST be loaded.
	if err := os.WriteFile(filepath.Join(dir, "real.yaml"), packYAML("sym/real", ""), 0o644); err != nil {
		t.Fatalf("write real: %v", err)
	}
	// A target OUTSIDE the pack tree with valid template YAML, reachable only via
	// a symlink inside templates/. The symlink must be SKIPPED, not followed.
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, packYAML("evil/outside", ""), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(dir, "escape.yaml")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}

	b := NewGitPackBackend(root, NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ts) != 1 || ts[0].ID != "sym/real" {
		ids := make([]string, len(ts))
		for i, tp := range ts {
			ids[i] = tp.ID
		}
		t.Errorf("Load ids = %v, want [sym/real] (symlink must be skipped)", ids)
	}
}

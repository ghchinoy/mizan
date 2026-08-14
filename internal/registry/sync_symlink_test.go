package registry

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadSkipsSymlinkedFileEscape proves a per-file symlink inside a cloned
// (untrusted) templates/ dir pointing OUT OF TREE is not followed: the
// out-of-tree template is never read/imported (CWE-59/CWE-22 containment).
func TestLoadSkipsSymlinkedFileEscape(t *testing.T) {
	base := t.TempDir()

	// An out-of-tree template the attacker wants us to read.
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(outside, "secret.yaml")
	writeTemplateFile(t, secretPath, sampleTemplate("evil/secret"))

	// The cloned tree: one legit template + a symlink to the out-of-tree file.
	clone := filepath.Join(base, "clone")
	templatesDir := filepath.Join(clone, "packs", "goodpack", "templates")
	if err := os.MkdirAll(templatesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTemplateFile(t, filepath.Join(templatesDir, "good.yaml"), sampleTemplate("goodpack/good"))
	if err := os.Symlink(secretPath, filepath.Join(templatesDir, "link.yaml")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	b := NewGitPackBackend(clone, NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(ctx())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, x := range ts {
		if x.ID == "evil/secret" {
			t.Fatalf("Load followed a symlink out of the pack tree and read %q", x.ID)
		}
	}
	if len(ts) != 1 || ts[0].ID != "goodpack/good" {
		t.Fatalf("loaded = %+v; want only goodpack/good", ids(ts))
	}
}

// TestLoadSkipsSymlinkedTemplatesDirEscape proves a symlinked templates/ DIR
// pointing out of tree is not followed (Lstat), so its files are not read.
func TestLoadSkipsSymlinkedTemplatesDirEscape(t *testing.T) {
	base := t.TempDir()

	// An out-of-tree directory full of templates.
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTemplateFile(t, filepath.Join(outside, "secret.yaml"), sampleTemplate("evil/secret"))

	// A pack whose templates/ is a symlink to that out-of-tree dir.
	clone := filepath.Join(base, "clone")
	packDir := filepath.Join(clone, "packs", "evilpack")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(packDir, "templates")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	b := NewGitPackBackend(clone, NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(ctx())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ts) != 0 {
		t.Fatalf("loaded %+v; a symlinked templates/ dir must not be read", ids(ts))
	}
}

// TestLoadSkipsSymlinkedPacksDirEscape proves a symlinked packs/ dir is not
// followed at discovery time.
func TestLoadSkipsSymlinkedPacksDirEscape(t *testing.T) {
	base := t.TempDir()

	// An out-of-tree "packs" tree.
	outsidePacks := filepath.Join(base, "outside", "packs")
	td := filepath.Join(outsidePacks, "evilpack", "templates")
	if err := os.MkdirAll(td, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTemplateFile(t, filepath.Join(td, "secret.yaml"), sampleTemplate("evil/secret"))

	clone := filepath.Join(base, "clone")
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePacks, filepath.Join(clone, "packs")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	b := NewGitPackBackend(clone, NewYAMLCodec(), SyncConfig{})
	ts, err := b.Load(ctx())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, x := range ts {
		if x.ID == "evil/secret" {
			t.Fatalf("Load followed a symlinked packs/ dir and read %q", x.ID)
		}
	}
}

func writeTemplateFile(t *testing.T, path string, tmpl MetricTemplate) {
	t.Helper()
	data, err := NewYAMLCodec().Marshal(&tmpl)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func ids(ts []MetricTemplate) []string {
	out := make([]string, len(ts))
	for i, x := range ts {
		out[i] = x.ID
	}
	return out
}

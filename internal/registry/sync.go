package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SyncConfig carries the ambient sync settings the composition root injects into
// the Service (design §3.1). It is deliberately tiny: source selection is a
// per-operation string argument, not config, so switching source needs no wiring
// change (the seam-pivot property).
type SyncConfig struct {
	PackCacheDir         string // where git-URL checkouts are cached (P2.5)
	DefaultTemplatesRepo string // used when Import is called with an empty src (P2.5)
}

// SourceInfo describes where a SyncBackend loaded from, for provenance stamping
// and reporting.
type SourceInfo struct {
	Type   string // "local" now; "git" lands in P2.5
	Origin string // the resolved path or url
}

// SyncBackend is the contribution-channel seam (design §3.1). In P2.1 the only
// implementation is GitPackBackend reading a LOCAL packs/ tree; Save (pack
// write) is P2.4 and git-URL clone is P2.5.
type SyncBackend interface {
	Load(ctx context.Context) ([]MetricTemplate, error)
	Save(ctx context.Context, ts []MetricTemplate) error
	Describe() SourceInfo
}

// gitClone is the injectable git shell-out seam (design §3.11). It is a
// package-level var ONLY so a later phase (P2.5) can wire real cloning and unit
// tests can substitute a fake without touching the network. It is NOT called in
// P2.1 — local paths never reach it.
var gitClone = func(ctx context.Context, url, dest string) error {
	return errors.New("registry: git-url import is not implemented yet (P2.5); pass a local checkout path")
}

// GitPackBackend loads templates from a pack tree on the local filesystem. The
// "Git" name reflects its eventual role (reading a git checkout); in P2.1 it is
// purely a local-filesystem reader — no clone, no network.
type GitPackBackend struct {
	src   string // repo/tree with packs/, OR a single pack dir
	codec Codec
	cfg   SyncConfig
}

// NewGitPackBackend constructs a backend over a local source path. It is used
// internally by Service.Import; cmd/* never constructs it (that would drag the
// sync package into cmd/*, breaking the seam — design §3.1).
func NewGitPackBackend(src string, codec Codec, cfg SyncConfig) *GitPackBackend {
	if codec == nil {
		codec = NewYAMLCodec()
	}
	return &GitPackBackend{src: src, codec: codec, cfg: cfg}
}

// Describe reports the resolved source. In P2.1 every source is a local path.
func (b *GitPackBackend) Describe() SourceInfo {
	return SourceInfo{Type: "local", Origin: b.src}
}

// Save is P2.4 (export). It returns a clear not-implemented error so the seam is
// present and callers get a useful message rather than a silent no-op.
func (b *GitPackBackend) Save(ctx context.Context, ts []MetricTemplate) error {
	return errors.New("registry: pack export (Save) is not implemented yet (P2.4)")
}

// Load reads every metric-template file under the source's pack tree. It accepts
// either a repo/tree that contains a top-level packs/ directory (each subdir a
// pack) or a single pack directory (one that contains templates/). Templates are
// returned sorted by ID for deterministic import ordering. LOCAL PATHS ONLY in
// P2.1 — a git URL is rejected via the gitClone seam until P2.5.
func (b *GitPackBackend) Load(ctx context.Context) ([]MetricTemplate, error) {
	fi, err := os.Stat(b.src)
	if err != nil {
		return nil, fmt.Errorf("registry: source %q: %w", b.src, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("registry: source %q is not a directory (git-url import is P2.5)", b.src)
	}

	packDirs, err := b.discoverPackDirs()
	if err != nil {
		return nil, err
	}

	var out []MetricTemplate
	for _, pd := range packDirs {
		ts, err := b.loadPack(pd)
		if err != nil {
			return nil, err
		}
		out = append(out, ts...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// discoverPackDirs returns the pack directories to read: either every immediate
// subdirectory of <src>/packs, or <src> itself when it is a single pack dir.
func (b *GitPackBackend) discoverPackDirs() ([]string, error) {
	packsRoot := filepath.Join(b.src, "packs")
	if fi, err := os.Stat(packsRoot); err == nil && fi.IsDir() {
		entries, err := os.ReadDir(packsRoot)
		if err != nil {
			return nil, fmt.Errorf("registry: read packs dir %q: %w", packsRoot, err)
		}
		var dirs []string
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(packsRoot, e.Name()))
			}
		}
		return dirs, nil
	}
	// No packs/ subtree → treat src as a single pack dir.
	return []string{b.src}, nil
}

// loadPack reads all template files under <packDir>/templates. A pack with no
// templates/ dir yields no templates (it may be evalsets-only — P2.2), which is
// not an error.
func (b *GitPackBackend) loadPack(packDir string) ([]MetricTemplate, error) {
	templatesDir := filepath.Join(packDir, "templates")
	if fi, err := os.Stat(templatesDir); err != nil || !fi.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		return nil, fmt.Errorf("registry: read templates dir %q: %w", templatesDir, err)
	}
	var out []MetricTemplate
	for _, e := range entries {
		if e.IsDir() || !isYAMLFile(e.Name()) {
			continue
		}
		path := filepath.Join(templatesDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("registry: read template %q: %w", path, err)
		}
		t, err := b.codec.Unmarshal(data)
		if err != nil {
			return nil, fmt.Errorf("registry: %q: %w", path, err)
		}
		out = append(out, *t)
	}
	return out, nil
}

// isYAMLFile reports whether name has a .yaml/.yml extension.
func isYAMLFile(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".yaml" || ext == ".yml"
}

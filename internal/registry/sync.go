package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	yaml "gopkg.in/yaml.v3"
)

// packNamespacePattern is the required shape of a pack namespace (metadata.name)
// and of a template id's namespace segment: one or more lowercase letters,
// digits, and hyphens. It mirrors a single segment of templateIDPattern
// (validate.go).
var packNamespacePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// MaxTemplateFileBytes bounds the size of a single template file read on import.
// Template/rubric/schema documents are small config files; this 1 MiB cap is
// defense-in-depth so a hostile pack cannot ship a multi-GB file (or a symlink to
// an unbounded source like /dev/zero) and exhaust memory on `registry import`
// (CWE-400). It is the ONE source of truth for that bound — cmd/mizan's --*-file
// flags reference it too, so the CLI and the import reader stay in lockstep.
const MaxTemplateFileBytes int64 = 1 << 20 // 1 MiB

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

// Save writes each template to the backend's LOCAL pack dir as one file per
// template under <src>/templates/ (design §3.3, globbed one-per-file). It is the
// export write side; committing and opening a PR is the human's job — Save never
// shells out to git (the gitClone seam is untouched, that is P2.5).
//
// Path derivation is the security-critical part (P2.1 audit rec#3): the output
// filename is derived from a VALIDATED slug — the trailing segment of a
// "<namespace>/<slug>" id that passes validateTemplateID — and NEVER from the
// raw id. That guard forbids a second '/', any "..", and any character outside
// [a-z0-9-], so a hostile or malformed id cannot escape the templates/ dir
// (CWE-22). An id that fails the shape guard is rejected here rather than
// written; Service.Export screens ids first and records skips, so this is the
// defensive last line. Two templates whose slugs collide within one Save are
// rejected too, so an export can never silently drop a template.
func (b *GitPackBackend) Save(_ context.Context, ts []MetricTemplate) error {
	templatesDir := filepath.Join(b.src, "templates")
	if err := os.MkdirAll(templatesDir, 0o700); err != nil {
		return fmt.Errorf("registry: create templates dir %q: %w", templatesDir, err)
	}
	ext := b.codec.Ext()
	written := map[string]string{} // filename -> id, for in-call collision detection
	for i := range ts {
		t := ts[i]
		slug, err := slugForID(t.ID)
		if err != nil {
			return err
		}
		name := slug + "." + ext
		if prev, ok := written[name]; ok {
			return fmt.Errorf("registry: templates %q and %q both map to file %q; export them to separate pack dirs", prev, t.ID, name)
		}
		data, err := b.codec.Marshal(&t)
		if err != nil {
			return err
		}
		path := filepath.Join(templatesDir, name)
		if err := writeFileNoFollow(path, data, 0o600); err != nil {
			return fmt.Errorf("registry: write template %q: %w", path, err)
		}
		written[name] = t.ID
	}
	return nil
}

// slugForID returns the trailing "<slug>" of a validated "<namespace>/<slug>"
// template id. It routes through validateTemplateID (the shared P2.1 ingest
// guard) so the returned slug is always a single path-safe component — the only
// value an output filename may be built from (audit rec#3). It never touches the
// filesystem.
func slugForID(id string) (string, error) {
	if err := validateTemplateID(id); err != nil {
		return "", err
	}
	// validateTemplateID guarantees exactly one '/', so the slug is everything
	// after it and matches [a-z0-9-]+ (no traversal, no separators).
	return id[strings.IndexByte(id, '/')+1:], nil
}

// packManifestFile is the on-disk shape of a pack's mizan-pack.yaml (design §3.3
// layout; mirrors the scaffolded google-brand manifest). It is written by
// scaffoldPack; templates are globbed from templates/ and are NOT enumerated
// here (so adding a template is never a manifest merge conflict).
type packManifestFile struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   packManifestMeta `yaml:"metadata"`
	Spec       packManifestSpec `yaml:"spec"`
}

type packManifestMeta struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Description string   `yaml:"description,omitempty"`
	Maintainers []string `yaml:"maintainers,omitempty"`
	License     string   `yaml:"license,omitempty"`
}

type packManifestSpec struct {
	RequiresAPIVersion string `yaml:"requiresApiVersion"`
}

// packManifestKind is the manifest kind for a pack directory's mizan-pack.yaml.
const packManifestKind = "Pack"

// scaffoldPack creates an empty, valid pack directory at dir: a mizan-pack.yaml
// manifest whose metadata.name is the namespace, an empty templates/ dir, and an
// empty evalsets/ dir (design §3.3 — the §3.4a EvalSet carriage hook is
// scaffolded, though EvalSet authoring itself is manual/generator-driven in P2).
// It deliberately emits NO CI workflow: the validate-packs workflow lives once in
// the mizan-templates repo, not in every scaffolded pack (design §6/P2.4). It
// refuses to overwrite an existing manifest so re-running init never clobbers an
// authored pack.
func scaffoldPack(dir, namespace string) error {
	if err := validatePackNamespace(namespace); err != nil {
		return err
	}
	manifestPath := filepath.Join(dir, "mizan-pack.yaml")
	if _, err := os.Stat(manifestPath); err == nil {
		return fmt.Errorf("registry: pack manifest %q already exists; refusing to overwrite", manifestPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("registry: stat %q: %w", manifestPath, err)
	}
	for _, sub := range []string{"templates", "evalsets"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return fmt.Errorf("registry: create %q: %w", filepath.Join(dir, sub), err)
		}
	}
	mf := packManifestFile{
		APIVersion: packAPIVersion,
		Kind:       packManifestKind,
		Metadata: packManifestMeta{
			Name:    namespace,
			Version: "0.1.0",
		},
		Spec: packManifestSpec{RequiresAPIVersion: packAPIVersion},
	}
	data, err := yaml.Marshal(mf)
	if err != nil {
		return fmt.Errorf("registry: marshal pack manifest: %w", err)
	}
	if err := writeFileNoFollow(manifestPath, data, 0o600); err != nil {
		return fmt.Errorf("registry: write pack manifest %q: %w", manifestPath, err)
	}
	return nil
}

// writeFileNoFollow writes data to path, refusing to follow a pre-existing
// symlink at that path (O_NOFOLLOW). It brings the export WRITE side to parity
// with the import READ side's symlink hardening (CWE-59/CWE-22, audit LOW): the
// filename component is already a validated slug so no write can NAME a target
// outside templates/, but a symlink seeded at the target — e.g. an attacker who
// pre-creates <dst>/templates/x.yaml -> ~/.bashrc — would otherwise redirect the
// write through the link to a file outside the pack dir. O_NOFOLLOW makes that
// open fail (ELOOP) rather than clobbering the link target. It uses O_TRUNC and
// NOT O_EXCL, so a legitimate re-export still overwrites the pack's own prior
// files. O_NOFOLLOW is portable across the Linux/macOS targets and keeps the
// build cgo-free.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, perm) //nolint:gosec // G304: path is built from a validated slug (slugForID) or a fixed manifest name, never raw input; O_NOFOLLOW additionally refuses a pre-seeded symlink.
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// validatePackNamespace enforces that a pack namespace is a single path-safe
// slug segment: the same [a-z0-9-]+ shape a template id's namespace segment must
// satisfy (validate.go). A pack's metadata.name must equal its directory name
// and every template id begins "<namespace>/", so a malformed namespace would
// both break that invariant and could seed a traversal in a later join.
func validatePackNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("registry: pack namespace is required")
	}
	if !packNamespacePattern.MatchString(ns) {
		return fmt.Errorf("registry: invalid pack namespace %q: must be lowercase letters, digits, and hyphens", ns)
	}
	return nil
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
		// Skip non-regular entries (symlinks, devices, FIFOs). os.DirEntry.Info
		// does NOT follow the link, so a symlinked templates/x.yaml is skipped here
		// rather than followed out of the pack tree at read time — closing the
		// symlink-escape / arbitrary-local-file-read / /dev/zero-hang vector
		// (CWE-59/CWE-22). Filenames are single base components from ReadDir, so no
		// '../' traversal reaches this join.
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("registry: stat template %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := readCappedFile(path)
		if err != nil {
			return nil, err
		}
		t, err := b.codec.Unmarshal(data)
		if err != nil {
			return nil, fmt.Errorf("registry: %q: %w", path, err)
		}
		out = append(out, *t)
	}
	return out, nil
}

// readCappedFile reads a template file with a hard size bound via io.LimitReader
// (MaxTemplateFileBytes), so a genuinely huge committed file — or an unbounded
// source reached through a link that slipped the IsRegular guard — cannot exhaust
// memory (CWE-400). It reads one byte past the cap to distinguish an
// exactly-at-cap file from an oversized one and rejects the latter with a clear
// error rather than silently truncating.
func readCappedFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("registry: read template %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxTemplateFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("registry: read template %q: %w", path, err)
	}
	if int64(len(data)) > MaxTemplateFileBytes {
		return nil, fmt.Errorf("registry: template %q exceeds the %d-byte size cap", path, MaxTemplateFileBytes)
	}
	return data, nil
}

// isYAMLFile reports whether name has a .yaml/.yml extension.
func isYAMLFile(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".yaml" || ext == ".yml"
}

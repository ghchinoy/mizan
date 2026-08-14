package registry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

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

// maxImportTemplates and maxImportTotalBytes are the AGGREGATE read caps applied
// across a whole import, complementing the per-file MaxTemplateFileBytes cap. A
// hostile pack could otherwise ship an enormous NUMBER of individually-bounded
// files and still exhaust memory on `registry import` (CWE-400); Load fails
// closed with a clear error naming the limit once either budget is exceeded.
// They are vars (not consts) solely so tests can lower them to trip the budget
// cheaply — treat them as constants in production. Defaults (10,000 templates /
// 64 MiB total) sit comfortably above any real pack yet well below a
// memory-exhaustion threshold.
var (
	maxImportTemplates        = 10_000
	maxImportTotalBytes int64 = 64 << 20 // 64 MiB
)

// loadBudget tracks the running aggregate read cost of a single Load so the
// per-import caps are enforced across ALL packs, not merely per file. add records
// one more template of n bytes and returns a fail-closed error if either
// aggregate cap is exceeded.
type loadBudget struct {
	templates int
	bytes     int64
}

func (bg *loadBudget) add(n int64) error {
	bg.templates++
	bg.bytes += n
	if bg.templates > maxImportTemplates {
		return fmt.Errorf("registry: import exceeds the aggregate template-count cap (%d); refusing", maxImportTemplates)
	}
	if bg.bytes > maxImportTotalBytes {
		return fmt.Errorf("registry: import exceeds the aggregate size cap (%d bytes); refusing", maxImportTotalBytes)
	}
	return nil
}

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

// gitTimeout bounds how long a single git clone/pull may run. A hostile or
// unreachable remote must not hang `registry import` forever (CWE-400): the
// context passed to the git process is capped at this so the shell-out is
// always time-bounded even if the caller passed a context.Background().
const gitTimeout = 5 * time.Minute

// allowedGitSchemes is the scheme allow-list for a git remote URL. Anything
// outside it (file://, javascript:, data:, etc.) is rejected before we shell out
// — a URL scheme is attacker-influenced input and must not select an arbitrary
// git transport or a local-file read.
var allowedGitSchemes = map[string]bool{
	"https": true,
	"http":  true,
	"ssh":   true,
	"git":   true,
}

// gitHostPattern is the permitted shape of a URL host[:port]. It is deliberately
// strict — letters, digits, dot, hyphen, and an optional numeric port — so a
// host component can never smuggle a shell metacharacter, whitespace, or a
// leading '-' that git would read as an option (argument injection).
var gitHostPattern = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]+)?$`)

// gitPathSegmentPattern is the permitted shape of a single path segment of a git
// URL (owner, repo, …). It forbids '.', '..', and any separator, so a segment
// can neither traverse (`..`) nor inject an option, and it is safe to use as a
// cache-dir path component.
var gitPathSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// gitEnv returns the environment for a git subprocess with all interactive
// credential/prompt paths disabled. This prevents a clone of a private or
// tampered remote from blocking on a terminal prompt (which would look like a
// hang) and keeps git from invoking an askpass helper that could echo a secret.
// It inherits the parent environment (so git and its helpers are found on PATH)
// and only overrides the prompt-related knobs.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never prompt on the controlling terminal
		"GIT_ASKPASS=",          // no GUI/CLI askpass helper
		"SSH_ASKPASS=",          // no ssh askpass helper
		"GCM_INTERACTIVE=never", // git-credential-manager: never prompt
	)
}

// gitClone is the injectable git shell-out seam for cloning a remote into a
// local cache dir (design §3.11). It is a package-level var so unit tests
// substitute a fake and never touch the network. SECURITY: git is invoked via
// exec.Command with an explicit argument slice — never `sh -c` or a
// string-built command line — so no shell interpolation is possible, and the
// end-of-options `--` separator guarantees the (already validated) URL and dest
// are treated as operands, not options (argument-injection defense). Output is
// redacted for any embedded credentials before it reaches an error message.
var gitClone = func(ctx context.Context, remote, dest string) error {
	cmd := exec.CommandContext(ctx, "git",
		// Belt-and-suspenders transport lockdown (defense-in-depth alongside the
		// scheme allow-list): forbid the ext:: (arbitrary-command) and file::
		// (local-path) transports outright so a remote can never select them even
		// if a future change loosened normalizeGitURL.
		"-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=never",
		"clone", "--depth", "1", "--single-branch", "--no-tags",
		"--", remote, dest)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("registry: git clone %s failed: %w: %s", redactURL(remote), err, redactBytes(out, remote))
	}
	return nil
}

// gitPull is the injectable git shell-out seam for refreshing an existing cache
// checkout. Same security discipline as gitClone: explicit arg slice, no shell,
// prompts disabled, output redacted. --ff-only refuses a divergent history
// rather than creating a merge commit in the cache.
var gitPull = func(ctx context.Context, dest string) error {
	cmd := exec.CommandContext(ctx, "git",
		"-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=never",
		"-C", dest, "pull", "--ff-only", "--no-tags")
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("registry: git pull in cache failed: %w: %s", err, redactBytes(out, ""))
	}
	return nil
}

// redactURL strips any userinfo (user:token@) from a URL so a credential a user
// embedded in the remote never lands in an error message, a log line, or the
// stored provenance Source. It handles a scheme-less input too (e.g.
// "user:tok@host/o/r"), which url.Parse would not expose userinfo for, by
// retrying with a synthetic scheme and stripping it back off. A value that
// carries no parseable userinfo is returned unchanged.
func redactURL(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		u.User = url.User("redacted")
		return u.String()
	}
	// Scheme-less input does not parse userinfo; retry with a synthetic scheme so
	// an embedded credential is still redacted, then strip the scheme we added.
	if !strings.Contains(raw, "://") {
		if u, err := url.Parse("https://" + raw); err == nil && u.User != nil {
			u.User = url.User("redacted")
			return strings.TrimPrefix(u.String(), "https://")
		}
	}
	return raw
}

// redactGitErr scrubs any of the given remote strings (and their redacted forms)
// from an error surfaced by the git shell-out seam. It is applied at
// resolveSource so that even a substituted (test) gitClone/gitPull whose error
// echoes a credential-bearing URL cannot leak it — defense-in-depth on top of
// the redaction the real gitClone/gitPull already perform on git's own output.
func redactGitErr(err error, remotes ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, r := range remotes {
		if r == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, r, redactURL(r))
	}
	return errors.New(msg)
}

// warnWriter is where the non-TLS transport warning is emitted. It is a package
// var (defaulting to os.Stderr) so a test can capture the warning without
// touching the real stderr.
var warnWriter io.Writer = os.Stderr

// warnIfInsecureTransport emits a one-line stderr warning when a normalized git
// remote uses a cleartext/unauthenticated transport (http or git://). These
// transports are intentionally still allowed (local-mirror use cases), but any
// URL-embedded credentials travel in the clear over them, so the user is warned
// once per import. The URL is redacted before it is printed.
func warnIfInsecureTransport(normalizedURL string) {
	u, err := url.Parse(normalizedURL)
	if err != nil {
		return
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "git":
		fmt.Fprintf(warnWriter, "registry: WARNING: %s uses a cleartext/unauthenticated transport (%s); any URL-embedded credentials travel in the clear\n",
			redactURL(normalizedURL), strings.ToLower(u.Scheme))
	}
}

// redactBytes removes a raw remote string (and its redacted form) from git's
// combined output before it is surfaced in an error, closing the chance that a
// credential-bearing URL echoed by git leaks into logs.
func redactBytes(out []byte, remote string) string {
	s := string(out)
	if remote != "" {
		s = strings.ReplaceAll(s, remote, redactURL(remote))
	}
	return strings.TrimSpace(s)
}

// isGitSource reports whether src should be treated as a git remote rather than
// a local filesystem path. A src with an explicit "scheme://" is always a
// remote; otherwise an EXISTING local path wins (so a real directory is never
// misread as a URL), and a non-existent "host.tld/owner/repo"-shaped value is
// treated as a scheme-less remote (the shape of the shipped DefaultTemplatesRepo,
// github.com/ghchinoy/mizan-templates).
//
// BOUNDARY (intentional): the scheme-less classifier keys on the first path
// segment looking like a hostname — it contains a dot and does not begin with
// one. So a NON-EXISTENT dotted-first-segment value such as "my.thing/x" is
// classified as a remote, while "./x", "../x", and any path that EXISTS on disk
// stay local. The existing-path check runs first, so a real local directory
// named "my.thing" is always read as a local path regardless of its dot.
func isGitSource(src string) bool {
	if strings.Contains(src, "://") {
		return true
	}
	if _, err := os.Stat(src); err == nil {
		return false // an existing local path is never a URL
	}
	first, rest, ok := strings.Cut(src, "/")
	if !ok || rest == "" {
		return false
	}
	// A scheme-less remote's first segment is a hostname: it contains a dot and
	// does not begin with one (so "./x" and "../x" stay local).
	return strings.Contains(first, ".") && !strings.HasPrefix(first, ".")
}

// normalizeGitURL validates a git remote URL and returns its canonical form.
// SECURITY (the risk center of P2.5): it rejects an argument-injection URL (a
// leading '-' git would read as an option), enforces the scheme allow-list,
// enforces a strict host charset, and rejects any path segment that could
// traverse ('..') or inject an option. A scheme-less "host/owner/repo" is
// normalized to https.
func normalizeGitURL(src string) (string, error) {
	if strings.HasPrefix(src, "-") {
		return "", fmt.Errorf("registry: refusing git remote %q: leading '-' could be read as a git option (argument injection)", redactURL(src))
	}
	raw := src
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("registry: invalid git remote %q: %w", redactURL(src), err)
	}
	scheme := strings.ToLower(u.Scheme)
	if !allowedGitSchemes[scheme] {
		return "", fmt.Errorf("registry: refusing git remote %q: scheme %q is not in the allow-list (https, http, ssh, git)", redactURL(src), u.Scheme)
	}
	if u.Host == "" || !gitHostPattern.MatchString(u.Host) {
		return "", fmt.Errorf("registry: refusing git remote %q: invalid host %q", redactURL(src), u.Host)
	}
	segs := pathSegments(u.Path)
	if len(segs) < 1 {
		return "", fmt.Errorf("registry: refusing git remote %q: missing repository path", redactURL(src))
	}
	for _, seg := range segs {
		if seg == "." || seg == ".." || !gitPathSegmentPattern.MatchString(seg) {
			return "", fmt.Errorf("registry: refusing git remote %q: invalid path segment %q", redactURL(src), seg)
		}
	}
	u.Scheme = scheme
	return u.String(), nil
}

// pathSegments splits a URL path into its non-empty segments.
func pathSegments(p string) []string {
	var out []string
	for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// cacheDirFor derives the on-disk cache location for a validated remote URL:
// $PackCacheDir/<host>/<owner>/<repo> (design §6/P2.5). SECURITY: every
// component is re-validated against the safe segment charset (no '..', no
// separators) and the final path is asserted to stay WITHIN PackCacheDir
// (fail-closed), so a crafted host/repo can never escape the cache root
// (CWE-22).
func cacheDirFor(cacheRoot, normalizedURL string) (string, error) {
	if strings.TrimSpace(cacheRoot) == "" {
		return "", fmt.Errorf("registry: pack cache dir is not configured (set pack-cache); cannot import from a git URL")
	}
	u, err := url.Parse(normalizedURL)
	if err != nil {
		return "", fmt.Errorf("registry: invalid git remote %q: %w", redactURL(normalizedURL), err)
	}
	host := u.Hostname() // host without port; a port would not be a safe dir component
	if host == "" || !gitPathSegmentPattern.MatchString(host) {
		return "", fmt.Errorf("registry: refusing cache dir for host %q", u.Host)
	}
	segs := pathSegments(u.Path)
	if len(segs) < 1 {
		return "", fmt.Errorf("registry: refusing cache dir: missing repository path in %q", redactURL(normalizedURL))
	}
	// Strip a trailing ".git" from the final repo segment for a tidy cache path.
	segs[len(segs)-1] = strings.TrimSuffix(segs[len(segs)-1], ".git")
	components := append([]string{host}, segs...)
	for _, c := range components {
		if c == "" || c == "." || c == ".." || !gitPathSegmentPattern.MatchString(c) {
			return "", fmt.Errorf("registry: refusing unsafe cache path component %q", c)
		}
	}
	rootAbs, err := filepath.Abs(cacheRoot)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(append([]string{rootAbs}, components...)...)
	// Belt-and-suspenders containment: the joined+cleaned dest must remain under
	// the cache root. The component guards already forbid '..', so this can only
	// fail on a pathological input, but we assert it rather than trust it.
	rel, err := filepath.Rel(rootAbs, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("registry: refusing cache dir %q: escapes pack cache root %q", dest, rootAbs)
	}
	return dest, nil
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

// Describe reports the source kind and a stable, credential-redacted origin used
// for provenance stamping (Source = "pack:<ns>@<origin>"). A git remote reports
// Type "git" and the redacted URL; a local path reports Type "local" and the
// path. It performs no I/O and never blocks, so it is safe to call before Load.
func (b *GitPackBackend) Describe() SourceInfo {
	if isGitSource(b.src) {
		// Stamp the NORMALIZED (and redacted) URL so provenance matches the
		// cache-dir derivation and the seam-pivot dedup (a scheme-less src and its
		// https form resolve to one origin). It never fails or does I/O: if the URL
		// does not normalize, fall back to the redacted raw src.
		origin := redactURL(b.src)
		if normalized, err := normalizeGitURL(b.src); err == nil {
			origin = redactURL(normalized)
		}
		return SourceInfo{Type: "git", Origin: origin}
	}
	return SourceInfo{Type: "local", Origin: b.src}
}

// resolveSource returns the local directory to read pack files from. For a local
// path it is the path itself. For a git remote it validates the URL, derives the
// $PackCacheDir/<host>/<owner>/<repo> cache dir (containment-checked), and clones
// (fresh) or pulls (existing) into it via the injectable gitClone/gitPull seam.
// The git process runs under a bounded context (gitTimeout) so an unreachable or
// hostile remote cannot hang the import.
func (b *GitPackBackend) resolveSource(ctx context.Context) (string, error) {
	if !isGitSource(b.src) {
		return b.src, nil
	}
	normalized, err := normalizeGitURL(b.src)
	if err != nil {
		return "", err
	}
	warnIfInsecureTransport(normalized)
	dest, err := cacheDirFor(b.cfg.PackCacheDir, normalized)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	if isGitCheckout(dest) {
		if err := gitPull(ctx, dest); err != nil {
			return "", redactGitErr(err, normalized, b.src)
		}
		return dest, nil
	}
	// A non-empty dest that is NOT a valid checkout (an interrupted clone, or dirty
	// leftovers) would wedge the cache: git pull has no .git to work with and git
	// clone refuses a non-empty target, so `import` would fail until a human runs
	// `rm`. Remove the partial dest before re-cloning. dest is containment-checked
	// under the cache root by cacheDirFor, so this RemoveAll cannot escape it.
	if _, err := os.Stat(dest); err == nil {
		if err := os.RemoveAll(dest); err != nil {
			return "", fmt.Errorf("registry: clear stale cache dir: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", fmt.Errorf("registry: create cache dir: %w", err)
	}
	if err := gitClone(ctx, normalized, dest); err != nil {
		return "", redactGitErr(err, normalized, b.src)
	}
	return dest, nil
}

// isGitCheckout reports whether dest is an existing directory that already holds
// a git checkout (a .git entry), so resolveSource pulls rather than re-clones.
func isGitCheckout(dest string) bool {
	if fi, err := os.Stat(dest); err != nil || !fi.IsDir() {
		return false
	}
	_, err := os.Stat(filepath.Join(dest, ".git"))
	return err == nil
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
// a local path OR a git remote URL (P2.5); a git remote is cloned/pulled into the
// pack cache first (resolveSource), then read exactly like a local checkout. The
// source may be a repo/tree that contains a top-level packs/ directory (each
// subdir a pack) or a single pack directory (one that contains templates/).
// Templates are returned sorted by ID for deterministic import ordering.
//
// SECURITY (P2.2 containment discipline, carried forward for the untrusted
// cloned tree): the read root is resolved ONCE (symlinks followed) and every
// template file's fully-resolved path must stay WITHIN that root; a symlinked
// packs/ or templates/ dir is not followed (Lstat), and a per-file symlink,
// device, or FIFO is skipped. A cloned repo is untrusted content — it must not
// read out-of-tree files (CWE-59/CWE-22) or an unbounded source (CWE-400,
// readCappedFile).
func (b *GitPackBackend) Load(ctx context.Context) ([]MetricTemplate, error) {
	root, err := b.resolveSource(ctx)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("registry: source %q: %w", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("registry: source %q is not a directory", root)
	}

	// Resolve the read root once; every file read below must stay under it.
	resolvedRoot, err := resolveReal(root)
	if err != nil {
		return nil, fmt.Errorf("registry: resolve source root %q: %w", root, err)
	}

	packDirs, err := discoverPackDirs(root)
	if err != nil {
		return nil, err
	}

	budget := &loadBudget{}
	var out []MetricTemplate
	for _, pd := range packDirs {
		ts, err := b.loadPack(resolvedRoot, pd, budget)
		if err != nil {
			return nil, err
		}
		out = append(out, ts...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// discoverPackDirs returns the pack directories to read: either every immediate
// subdirectory of <root>/packs, or <root> itself when it is a single pack dir.
//
// packsRoot is stat'd with os.Lstat (NOT os.Stat) so a symlinked packs/ dir in
// an untrusted clone is not followed out of the tree (mirrors the validate.go
// discovery hardening, CWE-59/CWE-22).
func discoverPackDirs(root string) ([]string, error) {
	packsRoot := filepath.Join(root, "packs")
	if fi, err := os.Lstat(packsRoot); err == nil && fi.IsDir() {
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
	// No packs/ subtree → treat root as a single pack dir.
	return []string{root}, nil
}

// loadPack reads all template files under <packDir>/templates. A pack with no
// templates/ dir yields no templates (it may be evalsets-only — P2.2), which is
// not an error. resolvedRoot is the once-resolved read root every file must stay
// within (containment gate).
//
// The templates/ dir is stat'd with os.Lstat so a symlinked templates/ dir is
// not followed; each entry is skipped unless it is a regular file whose resolved
// path is contained under resolvedRoot. This applies the P2.2 symlink-containment
// discipline to the untrusted cloned tree: escapes and non-regular entries are
// skipped (defensive), and a path that cannot be resolved (dangling symlink) is
// fail-closed skipped.
func (b *GitPackBackend) loadPack(resolvedRoot, packDir string, budget *loadBudget) ([]MetricTemplate, error) {
	templatesDir := filepath.Join(packDir, "templates")
	if fi, err := os.Lstat(templatesDir); err != nil || !fi.IsDir() {
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
		// Containment gate (P2.2 discipline): the file's fully-resolved path must
		// stay under the resolved read root. Fail-closed: an escape or an
		// unresolvable path is skipped, never read.
		if ok, err := containedPath(resolvedRoot, path); err != nil || !ok {
			continue
		}
		data, err := readCappedFile(path)
		if err != nil {
			return nil, err
		}
		// Aggregate read cap (across all packs in this import): fail closed once the
		// running template count or total bytes exceeds the budget (CWE-400).
		if err := budget.add(int64(len(data))); err != nil {
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

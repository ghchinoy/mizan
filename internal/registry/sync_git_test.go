package registry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- test helpers -----------------------------------------------------------

// swapFakeGit replaces the gitClone/gitPull seam with fakes that populate the
// clone destination from a set of packs, so every git-URL unit test runs with NO
// network. It restores the real functions on cleanup. The fake clone writes each
// pack as <dest>/packs/<ns>/{mizan-pack.yaml,templates/<slug>.yaml} using the
// production YAML codec, so Load reads exactly what a real checkout would carry.
func swapFakeGit(t *testing.T, templates ...MetricTemplate) {
	t.Helper()
	origClone, origPull := gitClone, gitPull
	t.Cleanup(func() { gitClone, gitPull = origClone, origPull })

	gitClone = func(_ context.Context, _, dest string) error {
		return writePacksTree(dest, templates...)
	}
	gitPull = func(_ context.Context, dest string) error {
		return writePacksTree(dest, templates...)
	}
}

// writePacksTree writes a valid packs/ tree at root (a .git marker, plus one pack
// dir per namespace with a manifest and one file per template).
func writePacksTree(root string, templates ...MetricTemplate) error {
	// A .git marker makes isGitCheckout(dest) true so a second resolve pulls.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		return err
	}
	codec := NewYAMLCodec()
	for i := range templates {
		t := templates[i]
		ns := namespaceOf(t.ID)
		packDir := filepath.Join(root, "packs", ns)
		templatesDir := filepath.Join(packDir, "templates")
		if err := os.MkdirAll(templatesDir, 0o700); err != nil {
			return err
		}
		slug, err := slugForID(t.ID)
		if err != nil {
			return err
		}
		data, err := codec.Marshal(&t)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(templatesDir, slug+".yaml"), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func sampleTemplate(id string) MetricTemplate {
	return MetricTemplate{
		ID:                   id,
		Name:                 "Sample " + id,
		Kind:                 KindPointwise,
		Version:              "1.0.0",
		Modalities:           []Modality{ModalityText},
		Inputs:               []InputSpec{{Name: "response", Modality: ModalityText, Required: true}},
		MetricPromptTemplate: "Rate {{response}} on a 1-5 scale.",
		AutoraterModel:       "gemini-2.5-pro",
		SamplingCount:        4,
	}
}

// --- git-URL import via the fake seam ---------------------------------------

func TestImportGitURLViaFakeClone(t *testing.T) {
	swapFakeGit(t, sampleTemplate("acme/quality"))
	store := newFakeStore()
	svc := NewService(store, WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))

	report, err := svc.Import(ctx(), "https://github.com/acme/templates", ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1 {
		t.Fatalf("report = %d inserted; want 1", report.Inserted)
	}
	if report.Source.Type != "git" {
		t.Errorf("Source.Type = %q, want git", report.Source.Type)
	}
	got := store.items["acme/quality"]
	if got == nil {
		t.Fatal("template not stored")
	}
	// Provenance stamped with the git origin, not the local cache path.
	wantSource := "pack:acme@https://github.com/acme/templates"
	if got.Source != wantSource {
		t.Errorf("Source = %q, want %q", got.Source, wantSource)
	}
}

// TestImportSchemelessGitURL covers the shipped DefaultTemplatesRepo shape
// (github.com/owner/repo, no scheme): it is treated as a git remote (normalized
// to https), cloned via the fake, and imported.
func TestImportSchemelessGitURL(t *testing.T) {
	swapFakeGit(t, sampleTemplate("google-brand/x"))
	store := newFakeStore()
	svc := NewService(store, WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))

	report, err := svc.Import(ctx(), "github.com/ghchinoy/mizan-templates", ImportOptions{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1 || report.Source.Type != "git" {
		t.Fatalf("report = %+v", report)
	}
}

// TestImportBareUsesDefaultRepo proves a bare import (no src) resolves to
// Config.DefaultTemplatesRepo, wired through SyncConfig — no cmd/backend change.
func TestImportBareUsesDefaultRepo(t *testing.T) {
	swapFakeGit(t, sampleTemplate("google-brand/x"))
	store := newFakeStore()
	svc := NewService(store, WithSyncConfig(SyncConfig{
		PackCacheDir:         t.TempDir(),
		DefaultTemplatesRepo: "github.com/ghchinoy/mizan-templates",
	}))

	report, err := svc.Import(ctx(), "", ImportOptions{})
	if err != nil {
		t.Fatalf("bare Import: %v", err)
	}
	if report.Inserted != 1 {
		t.Fatalf("bare import inserted %d; want 1", report.Inserted)
	}
}

func TestImportBareNoDefaultErrors(t *testing.T) {
	svc := NewService(newFakeStore()) // zero-value SyncConfig: no default repo
	if _, err := svc.Import(ctx(), "", ImportOptions{}); err == nil {
		t.Fatal("bare import with no default repo: expected error")
	}
}

// TestImportNamespaceFilter imports only the requested namespace even though the
// cloned tree carries several packs.
func TestImportNamespaceFilter(t *testing.T) {
	swapFakeGit(t,
		sampleTemplate("alpha/one"),
		sampleTemplate("beta/two"),
		sampleTemplate("beta/three"),
	)
	store := newFakeStore()
	svc := NewService(store, WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))

	report, err := svc.Import(ctx(), "https://example.com/x", ImportOptions{Namespace: "beta"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 2 {
		t.Fatalf("inserted %d; want 2 (only beta/*)", report.Inserted)
	}
	if store.items["alpha/one"] != nil {
		t.Error("alpha/one imported despite --namespace beta")
	}
	if store.items["beta/two"] == nil || store.items["beta/three"] == nil {
		t.Error("beta/* templates not imported")
	}
}

// TestImportGitReconciliationFlowsThroughP23 confirms a git-URL import reuses the
// existing P2.3 reconcile path: re-import of an unchanged clone is a no-op.
func TestImportGitReconciliationFlowsThroughP23(t *testing.T) {
	swapFakeGit(t, sampleTemplate("acme/quality"))
	store := newFakeStore()
	svc := NewService(store, WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))

	if _, err := svc.Import(ctx(), "https://example.com/x", ImportOptions{}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	puts := store.putCalls
	report, err := svc.Import(ctx(), "https://example.com/x", ImportOptions{})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if report.Unchanged != 1 || report.Inserted != 0 {
		t.Errorf("re-import report = %+v; want 1 unchanged, 0 inserted", report)
	}
	if store.putCalls != puts {
		t.Errorf("re-import wrote to store (%d extra puts); should be a no-op", store.putCalls-puts)
	}
}

// TestSeamPivotSourceSwitch is the design acceptance seam-pivot check: switching
// the source (git repo -> fork -> local tree) is a src-string change ONLY — the
// same Service.Import + GitPackBackend handle all three, no cmd/backend change.
func TestSeamPivotSourceSwitch(t *testing.T) {
	swapFakeGit(t, sampleTemplate("acme/quality"))
	cacheDir := t.TempDir()

	// 1) canonical git repo
	svc := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	if r, err := svc.Import(ctx(), "https://github.com/acme/templates", ImportOptions{}); err != nil || r.Inserted != 1 {
		t.Fatalf("repo import: r=%+v err=%v", r, err)
	}

	// 2) a fork URL — same code path, only the string differs
	svc = NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	if r, err := svc.Import(ctx(), "https://github.com/somefork/templates", ImportOptions{}); err != nil || r.Inserted != 1 {
		t.Fatalf("fork import: r=%+v err=%v", r, err)
	}

	// 3) a local tree — no clone at all, still Service.Import(src)
	local := t.TempDir()
	if err := writePacksTree(local, sampleTemplate("acme/quality")); err != nil {
		t.Fatalf("write local tree: %v", err)
	}
	svc = NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	if r, err := svc.Import(ctx(), local, ImportOptions{}); err != nil || r.Inserted != 1 {
		t.Fatalf("local import: r=%+v err=%v", r, err)
	}
	if got := NewGitPackBackend(local, NewYAMLCodec(), SyncConfig{}).Describe(); got.Type != "local" {
		t.Errorf("local Describe().Type = %q, want local", got.Type)
	}
}

// --- security: URL classification + validation ------------------------------

func TestIsGitSource(t *testing.T) {
	// A real local dir is never a URL.
	if isGitSource("testdata") {
		t.Error("existing local dir classified as git")
	}
	if isGitSource("./relative") || isGitSource("../up") {
		t.Error("relative path classified as git")
	}
	cases := map[string]bool{
		"https://github.com/a/b":        true,
		"http://example.com/a/b":        true,
		"ssh://git@host/a/b":            true,
		"github.com/ghchinoy/templates": true,
		"/abs/local/path":               false,
		"plainname":                     false,
		"no.dot.here.but.no.slash":      false, // needs host/path shape
	}
	for in, want := range cases {
		if got := isGitSource(in); got != want {
			t.Errorf("isGitSource(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestNormalizeGitURLRejectsArgumentInjection is the core security test: a URL
// that git could read as an option must be rejected before any shell-out.
func TestNormalizeGitURLRejectsArgumentInjection(t *testing.T) {
	bad := []string{
		"--upload-pack=touch /tmp/pwn",
		"-oProxyCommand=evil",
		"--config=core.something",
		"ext::sh -c evil",              // no scheme, ext:: transport is not host/path shaped
		"file:///etc/passwd",           // scheme not in allow-list
		"javascript:alert(1)",          // scheme not in allow-list
		"https:// /a/b",                // invalid host
		"https://ho st.com/a/b",        // space in host
		"https://github.com/a/../../b", // path traversal segment
		"https://github.com",           // missing repo path
	}
	for _, in := range bad {
		if _, err := normalizeGitURL(in); err == nil {
			t.Errorf("normalizeGitURL(%q) accepted a hostile URL; want rejection", in)
		}
	}
}

func TestNormalizeGitURLAccepts(t *testing.T) {
	good := map[string]string{
		"github.com/ghchinoy/mizan-templates": "https://github.com/ghchinoy/mizan-templates",
		"https://github.com/a/b.git":          "https://github.com/a/b.git",
		"http://example.com/a/b":              "http://example.com/a/b",
	}
	for in, want := range good {
		got, err := normalizeGitURL(in)
		if err != nil {
			t.Errorf("normalizeGitURL(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeGitURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCacheDirForContainment proves the derived cache dir stays under PackCacheDir
// and that a missing cache root is rejected.
func TestCacheDirForContainment(t *testing.T) {
	root := t.TempDir()
	dest, err := cacheDirFor(root, "https://github.com/ghchinoy/mizan-templates.git")
	if err != nil {
		t.Fatalf("cacheDirFor: %v", err)
	}
	wantSuffix := filepath.Join("github.com", "ghchinoy", "mizan-templates")
	if !strings.HasSuffix(dest, wantSuffix) {
		t.Errorf("dest = %q, want suffix %q", dest, wantSuffix)
	}
	rel, err := filepath.Rel(root, dest)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("dest %q escapes cache root %q (rel=%q)", dest, root, rel)
	}

	if _, err := cacheDirFor("", "https://github.com/a/b"); err == nil {
		t.Error("cacheDirFor with empty cache root should error")
	}
}

func TestImportGitURLRejectedWithoutCache(t *testing.T) {
	// A git URL with no configured pack cache must fail closed (never clone into
	// an unbounded/unknown location).
	swapFakeGit(t, sampleTemplate("acme/quality"))
	svc := NewService(newFakeStore()) // no PackCacheDir
	if _, err := svc.Import(ctx(), "https://github.com/acme/templates", ImportOptions{}); err == nil {
		t.Fatal("git-URL import with no pack cache: expected error")
	}
}

func TestImportArgumentInjectionURLRejected(t *testing.T) {
	svc := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))
	// This looks host/path-shaped so it is classified as a git remote, then the
	// normalizer must reject it before any shell-out.
	if _, err := svc.Import(ctx(), "evil.com/--upload-pack=x/repo", ImportOptions{}); err == nil {
		t.Fatal("argument-injection-shaped URL: expected rejection")
	}
}

func TestRedactURL(t *testing.T) {
	if got := redactURL("https://user:secret@github.com/a/b"); strings.Contains(got, "secret") {
		t.Errorf("redactURL leaked a credential: %q", got)
	}
	if got := redactURL("https://github.com/a/b"); got != "https://github.com/a/b" {
		t.Errorf("redactURL altered a clean URL: %q", got)
	}
}

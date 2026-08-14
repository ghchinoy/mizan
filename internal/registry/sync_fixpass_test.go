package registry

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitCallCounts records how many times the clone/pull seam was invoked so a test
// can assert the fresh-clone vs cache-hit-pull decision (fix-pass MUST-3).
type gitCallCounts struct{ clone, pull int }

// swapCountingGit installs fakes that BOTH populate the destination (so Load
// still reads a real tree) AND count invocations, restoring the real functions
// on cleanup. No network is touched.
func swapCountingGit(t *testing.T, templates ...MetricTemplate) *gitCallCounts {
	t.Helper()
	c := &gitCallCounts{}
	origClone, origPull := gitClone, gitPull
	t.Cleanup(func() { gitClone, gitPull = origClone, origPull })
	gitClone = func(_ context.Context, _, dest string) error {
		c.clone++
		return writePacksTree(dest, templates...)
	}
	gitPull = func(_ context.Context, dest string) error {
		c.pull++
		return writePacksTree(dest, templates...)
	}
	return c
}

// --- MUST-3: clone-vs-pull decision -----------------------------------------

func TestCloneOnFreshCachePullOnHit(t *testing.T) {
	counts := swapCountingGit(t, sampleTemplate("acme/quality"))
	cacheDir := t.TempDir()

	// Fresh cache dir → clone exactly once, no pull.
	svc := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	if _, err := svc.Import(ctx(), "https://github.com/acme/templates", ImportOptions{}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if counts.clone != 1 || counts.pull != 0 {
		t.Fatalf("fresh cache: clone=%d pull=%d; want clone=1 pull=0", counts.clone, counts.pull)
	}

	// Existing valid checkout (.git present from the fake clone) → pull once, no
	// further clone.
	svc2 := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	if _, err := svc2.Import(ctx(), "https://github.com/acme/templates", ImportOptions{}); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if counts.clone != 1 || counts.pull != 1 {
		t.Fatalf("cache hit: clone=%d pull=%d; want clone=1 pull=1", counts.clone, counts.pull)
	}
}

// --- MUST-4: cache-wedge recovery -------------------------------------------

func TestWedgedCacheRecoversOnReClone(t *testing.T) {
	counts := swapCountingGit(t, sampleTemplate("acme/quality"))
	cacheDir := t.TempDir()

	// Simulate a prior interrupted clone: the dest exists and is non-empty but has
	// no .git, so it is neither pullable nor cloneable-in-place.
	normalized, err := normalizeGitURL("https://github.com/acme/templates")
	if err != nil {
		t.Fatal(err)
	}
	dest, err := cacheDirFor(cacheDir, normalized)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dest, "leftover.txt")
	if err := os.WriteFile(stale, []byte("partial clone"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	r, err := svc.Import(ctx(), "https://github.com/acme/templates", ImportOptions{})
	if err != nil {
		t.Fatalf("wedged-cache import should recover, got: %v", err)
	}
	if r.Inserted != 1 {
		t.Fatalf("inserted %d; want 1", r.Inserted)
	}
	if counts.clone != 1 || counts.pull != 0 {
		t.Errorf("recovery should re-clone: clone=%d pull=%d; want clone=1 pull=0", counts.clone, counts.pull)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale partial dest was not removed before re-clone")
	}
}

// --- MUST-2: git-output redaction on the error path -------------------------

func TestGitCloneErrorNeverLeaksSecret(t *testing.T) {
	const secret = "ghp_CLONESECRET"
	remote := "https://user:" + secret + "@github.com/acme/templates"
	origClone, origPull := gitClone, gitPull
	t.Cleanup(func() { gitClone, gitPull = origClone, origPull })
	// A hostile/misbehaving git echoes the credential-bearing remote in its error.
	gitClone = func(_ context.Context, r, _ string) error {
		return fmt.Errorf("fatal: authentication failed for %s", r)
	}

	svc := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))
	_, err := svc.Import(ctx(), remote, ImportOptions{})
	if err == nil {
		t.Fatal("expected a clone error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("clone error leaked the secret: %v", err)
	}
}

func TestGitPullErrorNeverLeaksSecret(t *testing.T) {
	const secret = "ghp_PULLSECRET"
	remote := "https://user:" + secret + "@github.com/acme/templates"
	cacheDir := t.TempDir()

	// Seed a valid cache checkout so the next import pulls.
	swapFakeGit(t, sampleTemplate("acme/quality"))
	svc := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	if _, err := svc.Import(ctx(), remote, ImportOptions{}); err != nil {
		t.Fatalf("seed import: %v", err)
	}
	// Now make the pull fail with the secret in its output.
	gitPull = func(_ context.Context, _ string) error {
		return fmt.Errorf("fatal: could not read from %s", remote)
	}
	svc2 := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: cacheDir}))
	_, err := svc2.Import(ctx(), remote, ImportOptions{})
	if err == nil {
		t.Fatal("expected a pull error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("pull error leaked the secret: %v", err)
	}
}

// TestRejectionErrorsNeverLeakToken is the auditor's negative test: every
// normalizeGitURL / cacheDirFor rejection whose input carries a credential must
// scrub it from the returned error (fix-pass MUST-1/MUST-2).
func TestRejectionErrorsNeverLeakToken(t *testing.T) {
	const secret = "ghp_LEAKTOKEN"
	normalizeCases := []string{
		"sftp://user:" + secret + "@github.com/o/r",        // scheme not allowed
		"https://user:" + secret + "@github.com/o/../../r", // traversal segment
		"https://user:" + secret + "@bad_host!/o/r",        // invalid host
		"https://user:" + secret + "@github.com",           // missing repo path
	}
	for _, in := range normalizeCases {
		_, err := normalizeGitURL(in)
		if err == nil {
			t.Errorf("normalizeGitURL(%q) accepted a hostile URL; want rejection", in)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("normalizeGitURL leaked the token: %v", err)
		}
	}

	// cacheDirFor rejection path that prints the URL must redact it too.
	if _, err := cacheDirFor(t.TempDir(), "https://user:"+secret+"@github.com"); err == nil {
		t.Error("cacheDirFor accepted a path-less URL; want rejection")
	} else if strings.Contains(err.Error(), secret) {
		t.Errorf("cacheDirFor leaked the token: %v", err)
	}
}

// --- SHOULD-5: aggregate read caps ------------------------------------------

func TestLoadAggregateTemplateCountCapTrips(t *testing.T) {
	orig := maxImportTemplates
	t.Cleanup(func() { maxImportTemplates = orig })
	maxImportTemplates = 2

	root := t.TempDir()
	if err := writePacksTree(root,
		sampleTemplate("acme/a"),
		sampleTemplate("acme/b"),
		sampleTemplate("acme/c"),
	); err != nil {
		t.Fatal(err)
	}
	_, err := NewGitPackBackend(root, NewYAMLCodec(), SyncConfig{}).Load(ctx())
	if err == nil {
		t.Fatal("expected the aggregate template-count cap to trip")
	}
	if !strings.Contains(err.Error(), "template-count cap") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadAggregateByteCapTrips(t *testing.T) {
	orig := maxImportTotalBytes
	t.Cleanup(func() { maxImportTotalBytes = orig })
	maxImportTotalBytes = 10 // any real template marshals to more than 10 bytes

	root := t.TempDir()
	if err := writePacksTree(root, sampleTemplate("acme/quality")); err != nil {
		t.Fatal(err)
	}
	_, err := NewGitPackBackend(root, NewYAMLCodec(), SyncConfig{}).Load(ctx())
	if err == nil {
		t.Fatal("expected the aggregate byte cap to trip")
	}
	if !strings.Contains(err.Error(), "size cap") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- SHOULD-7: cleartext-transport warning ----------------------------------

func TestInsecureTransportWarning(t *testing.T) {
	cases := []struct {
		url  string
		warn bool
	}{
		{"http://example.com/a/b", true},
		{"git://example.com/a/b", true},
		{"https://github.com/a/b", false},
		{"ssh://git@host/a/b", false},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		orig := warnWriter
		warnWriter = &buf
		warnIfInsecureTransport(tc.url)
		warnWriter = orig
		if got := buf.Len() > 0; got != tc.warn {
			t.Errorf("warnIfInsecureTransport(%q) warned=%v, want %v (%q)", tc.url, got, tc.warn, buf.String())
		}
		if buf.Len() > 0 && !strings.Contains(buf.String(), "cleartext") {
			t.Errorf("warning for %q missing the cleartext note: %q", tc.url, buf.String())
		}
	}
}

// --- SHOULD-8: provenance carries the normalized + redacted URL -------------

func TestProvenanceUsesNormalizedRedactedURL(t *testing.T) {
	swapFakeGit(t, sampleTemplate("acme/quality"))
	store := newFakeStore()
	svc := NewService(store, WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))

	const secret = "ghp_PROVSECRET"
	// Scheme-less src WITH embedded credentials: provenance must normalize to https
	// AND redact the credential.
	if _, err := svc.Import(ctx(), "user:"+secret+"@github.com/acme/templates", ImportOptions{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	got := store.items["acme/quality"]
	if got == nil {
		t.Fatal("template not stored")
	}
	if strings.Contains(got.Source, secret) {
		t.Fatalf("provenance leaked the secret: %q", got.Source)
	}
	want := "pack:acme@https://redacted@github.com/acme/templates"
	if got.Source != want {
		t.Errorf("Source = %q, want %q", got.Source, want)
	}
}

// --- SHOULD-9: isGitSource dotted-first-segment boundary --------------------

func TestIsGitSourceDottedFirstSegmentBoundary(t *testing.T) {
	// A NON-EXISTENT dotted-first-segment value is classified as a remote.
	if !isGitSource("my.thing/x") {
		t.Error("isGitSource(\"my.thing/x\") = false; want true (scheme-less remote shape)")
	}
	// But an EXISTING local dir with a dotted name always wins as local.
	p := filepath.Join(t.TempDir(), "my.thing")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if isGitSource(p) {
		t.Error("existing dotted local dir misclassified as a remote")
	}
}

// --- SHOULD-10: coverage fill -----------------------------------------------

func TestGitEnvDisablesPrompts(t *testing.T) {
	env := gitEnv()
	for _, want := range []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GCM_INTERACTIVE=never",
	} {
		found := false
		for _, e := range env {
			if e == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("gitEnv() missing %q", want)
		}
	}
}

// TestResolveSourceBoundsContext proves the git shell-out always runs under a
// deadline even when the caller passed an unbounded context (gitTimeout).
func TestResolveSourceBoundsContext(t *testing.T) {
	var sawDeadline bool
	origClone, origPull := gitClone, gitPull
	t.Cleanup(func() { gitClone, gitPull = origClone, origPull })
	gitClone = func(c context.Context, _, dest string) error {
		_, sawDeadline = c.Deadline()
		return writePacksTree(dest) // empty tree: no templates, no error
	}
	gitPull = func(c context.Context, dest string) error {
		_, sawDeadline = c.Deadline()
		return writePacksTree(dest)
	}

	svc := NewService(newFakeStore(), WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))
	// ctx() is context.Background() — unbounded — yet the git process must be capped.
	if _, err := svc.Import(ctx(), "https://github.com/a/b", ImportOptions{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if !sawDeadline {
		t.Error("git shell-out ran without a bounded deadline")
	}
}

func TestCacheDirForRejectsBadInputs(t *testing.T) {
	root := t.TempDir()
	bad := []string{
		"https://github.com", // missing repository path
		"https:///o/r",       // empty host
	}
	for _, u := range bad {
		if _, err := cacheDirFor(root, u); err == nil {
			t.Errorf("cacheDirFor(%q) accepted; want rejection", u)
		}
	}
}

func TestImportEmptyNamespaceImportsAll(t *testing.T) {
	swapFakeGit(t,
		sampleTemplate("alpha/one"),
		sampleTemplate("beta/two"),
	)
	store := newFakeStore()
	svc := NewService(store, WithSyncConfig(SyncConfig{PackCacheDir: t.TempDir()}))

	r, err := svc.Import(ctx(), "https://example.com/x", ImportOptions{Namespace: ""})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if r.Inserted != 2 {
		t.Fatalf("empty --namespace inserted %d; want 2 (all)", r.Inserted)
	}
}

package registry

// export_test.go covers P2.4: Service.Export + Selector, GitPackBackend.Save,
// pack scaffolding (scaffoldPack/InitPack), the audit-rec#3 path-from-validated-
// slug enforcement, and the HARD acceptance: export→import→export byte-identity
// (§9.2) including a custom_schema template whose responseSchema JSON is stored
// with UNSORTED keys (the P2.1 review #2 canonicalization fold-in).

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

// updateGolden rewrites the committed export golden testdata instead of asserting
// against it: `go test ./internal/registry -run TestExportMatchesGolden -update-golden`.
var updateGolden = flag.Bool("update-golden", false, "rewrite export golden testdata files")

// readTemplatesDir returns filename->bytes for every file under <packDir>/templates.
func readTemplatesDir(t *testing.T, packDir string) map[string][]byte {
	t.Helper()
	dir := filepath.Join(packDir, "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read templates dir %q: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %q: %v", e.Name(), err)
		}
		out[e.Name()] = b
	}
	return out
}

// seedTemplates returns a diverse set of templates covering every kind, incl. a
// custom_schema template whose responseSchema JSON keys are UNSORTED.
func seedTemplates() []MetricTemplate {
	unsorted := `{"zeta":{"type":"number"},"alpha":{"type":"string"},"nested":{"y":1,"x":2},"required":["zeta","alpha"]}`
	return []MetricTemplate{
		{
			ID:                   "acme/pointwise-quality",
			Name:                 "Pointwise Quality",
			Description:          "Scores response quality.",
			Version:              "1.0.0",
			Authors:              []Author{{Name: "Jane Doe", Email: "jane@example.com"}},
			Maintainers:          []string{"acme-team"},
			License:              "Apache-2.0",
			Tags:                 []string{"quality", "text"},
			Kind:                 KindPointwise,
			Modalities:           []Modality{ModalityText},
			Inputs:               []InputSpec{{Name: "response", Modality: ModalityText, Required: true}},
			MetricPromptTemplate: "Rate the response: {{response}}",
			SystemInstruction:    "Be strict.",
			AutoraterModel:       "gemini-2.5-pro",
			SamplingCount:        4,
		},
		{
			ID:                 "acme/pairwise-better",
			Name:               "Pairwise Better",
			Version:            "2.1.0",
			Kind:               KindPairwise,
			Modalities:         []Modality{ModalityText},
			CandidateFieldName: "candidate",
			BaselineFieldName:  "baseline",
			FlipEnabled:        true,
			SamplingCount:      2,
		},
		{
			ID:           "acme/rubric-brand",
			Name:         "Rubric Brand",
			Version:      "1.2.3",
			Kind:         KindRubric,
			Modalities:   []Modality{ModalityText},
			RubricGroups: map[string][]string{"clarity": {"clear", "concise"}, "tone": {"on-brand"}},
			RatingRubric: map[string]map[string]string{"clarity": {"1": "poor", "5": "great"}},
			RubricDetail: &RubricDetail{Scale: &RubricScale{Min: 1, Max: 5}},
		},
		{
			ID:             "acme/custom-audit",
			Name:           "Custom Audit",
			Version:        "0.9.0",
			Kind:           KindCustomSchema,
			Modalities:     []Modality{ModalityText},
			ResponseSchema: &Schema{JSON: unsorted}, // deliberately unsorted keys
		},
	}
}

func putAll(t *testing.T, s *Service, ts []MetricTemplate) {
	t.Helper()
	for i := range ts {
		if err := s.Create(context.Background(), ts[i]); err != nil {
			t.Fatalf("create %q: %v", ts[i].ID, err)
		}
	}
}

// TestExportRoundTripByteIdentity is the HARD §9.2 acceptance: export → import →
// export is byte-identical (pack files carry no computed `updated` field, so the
// comparison is exact), with NO field loss, including a custom_schema template
// whose responseSchema JSON was authored with unsorted keys.
func TestExportRoundTripByteIdentity(t *testing.T) {
	ctx := context.Background()

	svc1 := NewService(newFakeStore())
	putAll(t, svc1, seedTemplates())

	pack1 := filepath.Join(t.TempDir(), "pack1")
	rep1, err := svc1.Export(ctx, pack1, Selector{All: true})
	if err != nil {
		t.Fatalf("export 1: %v", err)
	}
	if rep1.Written != 4 || rep1.Skipped != 0 {
		t.Fatalf("export 1 report = %d written / %d skipped, want 4/0", rep1.Written, rep1.Skipped)
	}
	files1 := readTemplatesDir(t, pack1)

	// Import the exported pack into a fresh store.
	svc2 := NewService(newFakeStore())
	imp, err := svc2.Import(ctx, pack1, ImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.Inserted != 4 {
		t.Fatalf("import inserted = %d, want 4", imp.Inserted)
	}

	// Re-export from the fresh store.
	pack2 := filepath.Join(t.TempDir(), "pack2")
	if _, err := svc2.Export(ctx, pack2, Selector{All: true}); err != nil {
		t.Fatalf("export 2: %v", err)
	}
	files2 := readTemplatesDir(t, pack2)

	if !reflect.DeepEqual(files1, files2) {
		names := func(m map[string][]byte) []string {
			var n []string
			for k := range m {
				n = append(n, k)
			}
			sort.Strings(n)
			return n
		}
		t.Fatalf("export→import→export not byte-identical:\n pack1 files=%v\n pack2 files=%v", names(files1), names(files2))
	}

	// No field loss: the imported custom_schema template's responseSchema must be
	// present and be valid, canonical JSON (keys sorted).
	got, err := svc2.Get(ctx, "acme/custom-audit")
	if err != nil {
		t.Fatalf("get custom-audit: %v", err)
	}
	if got.ResponseSchema == nil || got.ResponseSchema.JSON == "" {
		t.Fatal("custom-audit lost its responseSchema on round-trip")
	}
	wantCanon := canonicalizeJSON(seedTemplates()[3].ResponseSchema.JSON)
	if got.ResponseSchema.JSON != wantCanon {
		t.Fatalf("responseSchema not canonical after import:\n got=%s\nwant=%s", got.ResponseSchema.JSON, wantCanon)
	}
}

// TestExportContentHashStableAcrossRoundTrip proves the canonicalization
// fold-in: a CLI-authored custom_schema template with UNSORTED responseSchema
// JSON hashes identically to the same template after export→import (whose JSON
// is canonical), so contentHash does not drift purely from key ordering.
func TestExportContentHashStableAcrossRoundTrip(t *testing.T) {
	ctx := context.Background()

	orig := seedTemplates()[3] // acme/custom-audit, unsorted JSON
	hOrig := contentHash(&orig)

	svc1 := NewService(newFakeStore())
	if err := svc1.Create(ctx, orig); err != nil {
		t.Fatalf("create: %v", err)
	}
	pack := filepath.Join(t.TempDir(), "pack")
	if _, err := svc1.Export(ctx, pack, Selector{ID: orig.ID}); err != nil {
		t.Fatalf("export: %v", err)
	}
	svc2 := NewService(newFakeStore())
	if _, err := svc2.Import(ctx, pack, ImportOptions{}); err != nil {
		t.Fatalf("import: %v", err)
	}
	imported, err := svc2.Get(ctx, orig.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if imported.ContentHash != hOrig {
		t.Fatalf("contentHash drifted across round-trip:\n orig    =%s\n imported=%s", hOrig, imported.ContentHash)
	}
}

func TestExportSelectorExactlyOne(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeStore())
	dst := t.TempDir()

	cases := []Selector{
		{},                                     // none
		{ID: "a/b", All: true},                 // two
		{Namespace: "a", All: true},            // two
		{ID: "a/b", Namespace: "a"},            // two
		{ID: "a/b", Namespace: "a", All: true}, // three
	}
	for _, sel := range cases {
		if _, err := svc.Export(ctx, dst, sel); err == nil {
			t.Errorf("Export(%+v) = nil error, want exactly-one-selector error", sel)
		}
	}
}

func TestExportByNamespace(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeStore())
	putAll(t, svc, []MetricTemplate{
		{ID: "acme/one", Kind: KindPointwise},
		{ID: "acme/two", Kind: KindPointwise},
		{ID: "other/three", Kind: KindPointwise},
	})
	pack := t.TempDir()
	rep, err := svc.Export(ctx, pack, Selector{Namespace: "acme"})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if rep.Written != 2 {
		t.Fatalf("written = %d, want 2 (namespace filter)", rep.Written)
	}
	files := readTemplatesDir(t, pack)
	if _, ok := files["three.yaml"]; ok {
		t.Fatal("namespace export leaked other/three")
	}
	if _, ok := files["one.yaml"]; !ok {
		t.Fatal("namespace export missing acme/one -> one.yaml")
	}
}

func TestExportMissingDestination(t *testing.T) {
	svc := NewService(newFakeStore())
	if _, err := svc.Export(context.Background(), "  ", Selector{All: true}); err == nil {
		t.Fatal("Export with blank dst = nil error, want required-destination error")
	}
}

// TestExportRejectsMalformedID is the audit-rec#3 guard: a stored id that fails
// the "<namespace>/<slug>" shape guard is SKIPPED (never written), so no path
// outside templates/ can be derived from a raw id.
func TestExportRejectsMalformedID(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	// Bypass Create's timestamps — inject hostile/malformed ids straight into the
	// store to model rows a future bug or a pre-guard import might have left.
	for _, id := range []string{"ok/good", "../../etc/passwd", "acme/sub/evil", "Acme/Bad"} {
		id := id
		store.items[id] = &MetricTemplate{ID: id, Kind: KindPointwise}
	}
	svc := NewService(store)
	pack := t.TempDir()
	rep, err := svc.Export(ctx, pack, Selector{All: true})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if rep.Written != 1 || rep.Skipped != 3 {
		t.Fatalf("report = %d written / %d skipped, want 1/3", rep.Written, rep.Skipped)
	}
	files := readTemplatesDir(t, pack)
	if len(files) != 1 {
		t.Fatalf("wrote %d files, want 1 (only the valid slug)", len(files))
	}
	if _, ok := files["good.yaml"]; !ok {
		t.Fatalf("expected only good.yaml, got %v", files)
	}
	// The pack dir must contain nothing but templates/good.yaml — no traversal.
	var stray []string
	_ = filepath.Walk(pack, func(p string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			rel, _ := filepath.Rel(pack, p)
			stray = append(stray, rel)
		}
		return nil
	})
	if len(stray) != 1 || stray[0] != filepath.Join("templates", "good.yaml") {
		t.Fatalf("unexpected files written: %v", stray)
	}
}

// TestExportSlugCollisionRejected proves Save never silently drops a template:
// two ids in different namespaces sharing a slug map to the same filename and
// must be rejected rather than one overwriting the other.
func TestExportSlugCollisionRejected(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeStore())
	putAll(t, svc, []MetricTemplate{
		{ID: "a/dup", Kind: KindPointwise},
		{ID: "b/dup", Kind: KindPointwise},
	})
	if _, err := svc.Export(ctx, t.TempDir(), Selector{All: true}); err == nil {
		t.Fatal("Export of colliding slugs = nil error, want collision rejection")
	}
}

func TestSlugForID(t *testing.T) {
	ok, err := slugForID("acme/video-brand-alignment")
	if err != nil || ok != "video-brand-alignment" {
		t.Fatalf("slugForID valid = (%q,%v), want (video-brand-alignment,nil)", ok, err)
	}
	for _, bad := range []string{"noslash", "a/b/c", "../x", "UP/case", "a/"} {
		if _, err := slugForID(bad); err == nil {
			t.Errorf("slugForID(%q) = nil error, want rejection", bad)
		}
	}
}

// TestExportMatchesGolden pins the exported WIRE FORMAT against committed golden
// files (review Opt#1). Unlike TestExportRoundTripByteIdentity — which proves
// export→import→export idempotency and would stay green if Marshal changed both
// halves identically — this test catches an UNINTENDED format drift: a change to
// the codec that shifts the bytes on disk breaks it. The golden also pins the
// responseSchema canonicalization (custom-audit's keys are sorted on write).
//
// Regenerate after an intentional format change with:
//
//	go test ./internal/registry -run TestExportMatchesGolden -update-golden
func TestExportMatchesGolden(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeStore())
	putAll(t, svc, seedTemplates())

	pack := filepath.Join(t.TempDir(), "pack")
	if _, err := svc.Export(ctx, pack, Selector{All: true}); err != nil {
		t.Fatalf("export: %v", err)
	}
	got := readTemplatesDir(t, pack)

	goldenTemplates := filepath.Join("testdata", "golden", "templates")
	if *updateGolden {
		if err := os.MkdirAll(goldenTemplates, 0o755); err != nil { //nolint:gosec // test fixture dir
			t.Fatalf("mkdir golden: %v", err)
		}
		for name, data := range got {
			if err := os.WriteFile(filepath.Join(goldenTemplates, name), data, 0o644); err != nil { //nolint:gosec // test fixture file
				t.Fatalf("write golden %q: %v", name, err)
			}
		}
		t.Logf("rewrote %d golden files under %s", len(got), goldenTemplates)
		return
	}

	want := readTemplatesDir(t, filepath.Join("testdata", "golden"))
	if !reflect.DeepEqual(got, want) {
		for name, data := range got {
			if !reflect.DeepEqual(data, want[name]) {
				t.Errorf("golden mismatch for %q:\n got:\n%s\nwant:\n%s", name, data, want[name])
			}
		}
		for name := range want {
			if _, ok := got[name]; !ok {
				t.Errorf("golden has %q but export did not produce it", name)
			}
		}
		t.Fatal("exported bytes do not match committed golden; if the format change is intentional, re-run with -update-golden")
	}
}

// TestExportedFileValidatesAgainstSchema checks that every exported template file
// satisfies the strict MetricTemplate JSON Schema (schema/metrictemplate.json)
// DIRECTLY, via the real JSON-Schema validator (ValidateTemplateSchema) now that
// the P2.2 schema is on main (test G1). This validates the actual export/`pack
// add` write bytes against the schema, rather than only proving they re-import.
func TestExportedFileValidatesAgainstSchema(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeStore())
	putAll(t, svc, seedTemplates())

	pack := filepath.Join(t.TempDir(), "pack")
	if _, err := svc.Export(ctx, pack, Selector{All: true}); err != nil {
		t.Fatalf("export: %v", err)
	}
	for name, data := range readTemplatesDir(t, pack) {
		t.Run(name, func(t *testing.T) {
			if err := ValidateTemplateSchema(data); err != nil {
				t.Fatalf("exported file %q is not schema-valid: %v\n%s", name, err, data)
			}
		})
	}
}

// TestExportMissingID covers the --id selector pointing at a template that does
// not exist: the store's ErrNotFound propagates out of Export (test G2).
func TestExportMissingID(t *testing.T) {
	svc := NewService(newFakeStore())
	if _, err := svc.Export(context.Background(), t.TempDir(), Selector{ID: "acme/nope"}); err == nil {
		t.Fatal("Export(--id acme/nope) on empty store = nil error, want not-found error")
	}
}

// TestExportSaveMkdirError covers the Save I/O error branch (test G4): when the
// destination pack path is an existing regular file, creating <dst>/templates
// fails and the error surfaces. The report must claim no writes (review Opt#2).
func TestExportSaveMkdirError(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeStore())
	putAll(t, svc, []MetricTemplate{{ID: "acme/quality", Kind: KindPointwise}})

	// A regular file where the pack dir should be: MkdirAll(<file>/templates) fails.
	dst := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(dst, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	rep, err := svc.Export(ctx, dst, Selector{All: true})
	if err == nil {
		t.Fatal("Export into a file path = nil error, want mkdir failure")
	}
	if rep.Written != 0 {
		t.Fatalf("report.Written = %d on Save failure, want 0 (Opt#2)", rep.Written)
	}
}

// TestExportRefusesSymlinkTarget is the audit-LOW write-side symlink guard: a
// pre-existing symlink at the export target is refused (O_NOFOLLOW), not
// followed, so a write cannot be redirected outside the pack dir. The link
// target must be left untouched.
func TestExportRefusesSymlinkTarget(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newFakeStore())
	putAll(t, svc, []MetricTemplate{{ID: "acme/quality", Kind: KindPointwise}})

	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "templates"), 0o700); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	// A sensitive file outside the pack dir, and a symlink to it at the target name.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	const sentinel = "DO NOT OVERWRITE"
	if err := os.WriteFile(outside, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	link := filepath.Join(pack, "templates", "quality.yaml")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := svc.Export(ctx, pack, Selector{All: true}); err == nil {
		t.Fatal("Export through a pre-existing symlink = nil error, want O_NOFOLLOW refusal")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(got) != sentinel {
		t.Fatalf("symlink target was overwritten (%q); the write followed the link", got)
	}
}

// TestScaffoldPackWriteError covers the scaffoldPack stat error branch (test G4):
// when the pack dir path is an existing regular file, stat of <file>/mizan-pack.yaml
// fails with a non-IsNotExist error and surfaces rather than silently proceeding.
func TestScaffoldPackWriteError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	svc := &Service{store: newFakeStore(), codec: NewYAMLCodec()}
	if err := svc.InitPack(context.Background(), file, "acme"); err == nil {
		t.Fatal("InitPack into a regular-file path = nil error, want stat/mkdir failure")
	}
}

func TestScaffoldPack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "packs", "google-brand")
	if err := (&Service{store: newFakeStore(), codec: NewYAMLCodec()}).InitPack(context.Background(), dir, "google-brand"); err != nil {
		t.Fatalf("InitPack: %v", err)
	}
	// manifest present and parseable as a Pack manifest.
	manifest := filepath.Join(dir, "mizan-pack.yaml")
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var mf packManifestFile
	if err := yaml.Unmarshal(b, &mf); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if mf.Kind != packManifestKind || mf.Metadata.Name != "google-brand" || mf.APIVersion != packAPIVersion {
		t.Fatalf("manifest = %+v, want kind=Pack name=google-brand apiVersion=%s", mf, packAPIVersion)
	}
	// empty templates/ and evalsets/ dirs both exist.
	for _, sub := range []string{"templates", "evalsets"} {
		fi, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s/ not scaffolded: err=%v", sub, err)
		}
	}
	// NO CI workflow scaffolded.
	if _, err := os.Stat(filepath.Join(dir, ".github")); !os.IsNotExist(err) {
		t.Fatalf("pack init emitted a .github dir (should emit no CI workflow): err=%v", err)
	}
	// Re-init refuses to overwrite.
	if err := scaffoldPack(dir, "google-brand"); err == nil {
		t.Fatal("re-init overwrote an existing manifest; want refusal")
	}
	// Bad namespace rejected.
	if err := scaffoldPack(filepath.Join(t.TempDir(), "p"), "Bad/NS"); err == nil {
		t.Fatal("scaffoldPack accepted a malformed namespace")
	}
}

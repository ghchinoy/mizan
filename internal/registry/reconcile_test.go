package registry

// reconcile_test.go exercises the full §3.8 import conflict matrix (design/
// collaboration-design.md §3.8) as a table: every (local state × incoming ×
// strategy) row is asserted against Service.Import, plus dirty protection, the
// no-op hash short-circuit, and --dry-run.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// tmpl returns a minimal, round-trip-stable pointwise template for reconciliation
// tests. Callers vary version/prompt to drive the matrix.
func tmpl(id, version, prompt string) MetricTemplate {
	return MetricTemplate{
		ID:                   id,
		Name:                 "A",
		Version:              version,
		Kind:                 KindPointwise,
		Modalities:           []Modality{ModalityText},
		Inputs:               []InputSpec{{Name: "response", Modality: ModalityText, Required: true}},
		MetricPromptTemplate: prompt,
		AutoraterModel:       "gemini-2.5-flash",
	}
}

// writePack marshals t into a temp pack tree (packs/<ns>/templates/<slug>.yaml)
// and returns the tree root suitable for Service.Import.
func writePack(t *testing.T, tm MetricTemplate) string {
	t.Helper()
	root := t.TempDir()
	ns := namespaceOf(tm.ID)
	slug := tm.ID[len(ns)+1:]
	dir := filepath.Join(root, "packs", ns, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := NewYAMLCodec().Marshal(&tm)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".yaml"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

// writeMultiPack writes several templates into ONE pack tree (each under
// packs/<ns>/templates/<slug>.yaml) so a single Import processes a mixed batch.
func writeMultiPack(t *testing.T, tmpls ...MetricTemplate) string {
	t.Helper()
	root := t.TempDir()
	for _, tm := range tmpls {
		ns := namespaceOf(tm.ID)
		slug := tm.ID[len(ns)+1:]
		dir := filepath.Join(root, "packs", ns, "templates")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		data, err := NewYAMLCodec().Marshal(&tm)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, slug+".yaml"), data, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

// loadedForm returns the template exactly as an import would produce it (codec
// round-trip), so a seeded "local" copy hashes identically to a re-import.
func loadedForm(t *testing.T, tm MetricTemplate) MetricTemplate {
	t.Helper()
	ts, err := NewGitPackBackend(writePack(t, tm), NewYAMLCodec(), SyncConfig{}).Load(ctx())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(ts) != 1 {
		t.Fatalf("loaded %d templates, want 1", len(ts))
	}
	return ts[0]
}

func TestImportMatrix(t *testing.T) {
	const id = "ns/a"
	forkID := forkedID(id) // ns-fork/a

	v1 := func() MetricTemplate { return tmpl(id, "1.0.0", "score {{response}}") }
	v2 := func() MetricTemplate { return tmpl(id, "2.0.0", "score {{response}}") }
	v1diff := func() MetricTemplate { return tmpl(id, "1.0.0", "SCORE {{response}} differently") }

	// localState builds the seeded local template for a row (nil == absent).
	type localState func(t *testing.T) *MetricTemplate
	absent := localState(func(*testing.T) *MetricTemplate { return nil })
	sameAsV1 := localState(func(t *testing.T) *MetricTemplate { l := loadedForm(t, v1()); return &l })
	olderV1 := sameAsV1 // local at v1, incoming will be v2 (upgrade)
	newerV2 := localState(func(t *testing.T) *MetricTemplate { l := loadedForm(t, v2()); return &l })
	dirtyV1 := localState(func(t *testing.T) *MetricTemplate {
		l := loadedForm(t, v1())
		l.Dirty = true
		return &l
	})

	cases := []struct {
		name     string
		local    localState
		incoming MetricTemplate
		strategy ImportStrategy
		want     ImportAction
		// forked: incoming lands under forkID and local id is untouched.
	}{
		// absent -> insert (all strategies)
		{"absent/newer", absent, v1(), StrategyNewer, ActionInserted},
		{"absent/skip", absent, v1(), StrategySkip, ActionInserted},
		{"absent/overwrite", absent, v1(), StrategyOverwrite, ActionInserted},
		{"absent/fork", absent, v1(), StrategyFork, ActionInserted},

		// present, hash equal -> no-op (all strategies)
		{"equal/newer", sameAsV1, v1(), StrategyNewer, ActionUnchanged},
		{"equal/skip", sameAsV1, v1(), StrategySkip, ActionUnchanged},
		{"equal/overwrite", sameAsV1, v1(), StrategyOverwrite, ActionUnchanged},
		{"equal/fork", sameAsV1, v1(), StrategyFork, ActionUnchanged},

		// present, incoming version > local
		{"upgrade/newer", olderV1, v2(), StrategyNewer, ActionUpdated},
		{"upgrade/skip", olderV1, v2(), StrategySkip, ActionSkipped},
		{"upgrade/overwrite", olderV1, v2(), StrategyOverwrite, ActionUpdated},
		{"upgrade/fork", olderV1, v2(), StrategyFork, ActionUpdated},

		// present, incoming version < local
		{"older/newer", newerV2, v1(), StrategyNewer, ActionSkipped},
		{"older/skip", newerV2, v1(), StrategySkip, ActionSkipped},
		{"older/overwrite", newerV2, v1(), StrategyOverwrite, ActionUpdated},
		{"older/fork", newerV2, v1(), StrategyFork, ActionSkipped},

		// present, version equal, hash differs -> conflict
		{"conflict/newer", sameAsV1, v1diff(), StrategyNewer, ActionConflicted},
		{"conflict/skip", sameAsV1, v1diff(), StrategySkip, ActionSkipped},
		{"conflict/overwrite", sameAsV1, v1diff(), StrategyOverwrite, ActionUpdated},
		{"conflict/fork", sameAsV1, v1diff(), StrategyFork, ActionForked},

		// present, locally dirty (incoming is even a newer version -> dirty still wins)
		{"dirty/newer", dirtyV1, v2(), StrategyNewer, ActionSkipped},
		{"dirty/skip", dirtyV1, v2(), StrategySkip, ActionSkipped},
		{"dirty/overwrite", dirtyV1, v2(), StrategyOverwrite, ActionUpdated},
		{"dirty/fork", dirtyV1, v2(), StrategyFork, ActionForked},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			local := tc.local(t)
			if local != nil {
				cp := *local
				store.items[id] = &cp
			}
			svc := NewService(store)

			putsBefore := store.putCalls
			report, err := svc.Import(ctx(), writePack(t, tc.incoming), ImportOptions{Strategy: tc.strategy})
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if len(report.Entries) != 1 || report.Entries[0].Action != tc.want {
				t.Fatalf("action = %+v, want %s", report.Entries, tc.want)
			}

			switch tc.want {
			case ActionInserted, ActionUpdated:
				got := store.items[id]
				if got == nil {
					t.Fatalf("template %s not written", id)
				}
				if got.MetricPromptTemplate != tc.incoming.MetricPromptTemplate || got.Version != tc.incoming.Version {
					t.Errorf("stored content not the incoming one: %+v", got)
				}
				if got.Dirty {
					t.Error("imported template must not be dirty")
				}
				if got.ContentHash == "" || got.ImportedAt.IsZero() {
					t.Error("provenance not stamped")
				}
				if tc.want == ActionUpdated && local != nil && !got.CreatedAt.Equal(local.CreatedAt) {
					t.Errorf("update did not preserve CreatedAt: got %v want %v", got.CreatedAt, local.CreatedAt)
				}
			case ActionForked:
				forked := store.items[forkID]
				if forked == nil {
					t.Fatalf("forked template %s not written", forkID)
				}
				if forked.MetricPromptTemplate != tc.incoming.MetricPromptTemplate {
					t.Errorf("fork content wrong: %+v", forked)
				}
				// The local copy must be preserved unchanged.
				if got := store.items[id]; got == nil || got.MetricPromptTemplate != local.MetricPromptTemplate {
					t.Errorf("local copy was modified by fork: %+v", got)
				}
			case ActionSkipped, ActionConflicted:
				if store.putCalls != putsBefore {
					t.Errorf("a %s outcome wrote to the store (%d Put calls)", tc.want, store.putCalls-putsBefore)
				}
				if got := store.items[id]; got == nil || got.MetricPromptTemplate != local.MetricPromptTemplate || got.Version != local.Version {
					t.Errorf("local copy changed on %s: %+v", tc.want, got)
				}
			case ActionUnchanged:
				if store.putCalls != putsBefore {
					t.Errorf("an unchanged re-import wrote to the store (%d Put calls)", store.putCalls-putsBefore)
				}
			}
		})
	}
}

// TestImportDryRunWritesNothing asserts --dry-run computes the full report but
// makes no store writes, for both an insert and an update.
func TestImportDryRunWritesNothing(t *testing.T) {
	const id = "ns/a"

	t.Run("insert", func(t *testing.T) {
		store := newFakeStore()
		svc := NewService(store)
		report, err := svc.Import(ctx(), writePack(t, tmpl(id, "1.0.0", "p")), ImportOptions{DryRun: true})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Inserted != 1 || !report.DryRun {
			t.Fatalf("report = %+v; want 1 inserted, DryRun true", report)
		}
		if store.putCalls != 0 || len(store.items) != 0 {
			t.Errorf("dry run wrote to the store: %d puts, %d items", store.putCalls, len(store.items))
		}
	})

	t.Run("update", func(t *testing.T) {
		store := newFakeStore()
		local := loadedForm(t, tmpl(id, "1.0.0", "p"))
		store.items[id] = &local
		svc := NewService(store)
		report, err := svc.Import(ctx(), writePack(t, tmpl(id, "2.0.0", "p")), ImportOptions{DryRun: true})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Updated != 1 {
			t.Fatalf("report = %+v; want 1 updated", report)
		}
		if store.putCalls != 0 {
			t.Errorf("dry run wrote to the store: %d puts", store.putCalls)
		}
		if got := store.items[id]; got.Version != "1.0.0" {
			t.Errorf("dry run mutated the store: version = %q", got.Version)
		}
	})
}

// TestImportUnknownStrategyErrors rejects an unrecognized --strategy up front.
func TestImportUnknownStrategyErrors(t *testing.T) {
	svc := NewService(newFakeStore())
	_, err := svc.Import(ctx(), writePack(t, tmpl("ns/a", "1.0.0", "p")), ImportOptions{Strategy: "bogus"})
	if err == nil {
		t.Fatal("unknown strategy: expected error")
	}
}

// TestImportDirtyProtectionEndToEnd is the §9.4 acceptance in unit form: an
// imported template that is then edited (Update -> dirty) is NOT clobbered by a
// default re-import even when upstream is unchanged.
func TestImportDirtyProtectionEndToEnd(t *testing.T) {
	const id = "ns/a"
	store := newFakeStore()
	svc := NewService(store)
	src := writePack(t, tmpl(id, "1.0.0", "score {{response}}"))

	if _, err := svc.Import(ctx(), src, ImportOptions{}); err != nil {
		t.Fatalf("initial import: %v", err)
	}
	// Local edit marks it dirty.
	edited := store.items[id]
	edited.MetricPromptTemplate = "my local tweak {{response}}"
	if err := svc.Update(ctx(), *edited); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !store.items[id].Dirty {
		t.Fatal("Update did not mark the template dirty")
	}

	// Re-import upstream (unchanged) under default newer -> dirty protection skips.
	report, err := svc.Import(ctx(), src, ImportOptions{})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if report.Skipped != 1 || report.Entries[0].Action != ActionSkipped {
		t.Fatalf("report = %+v; want 1 skipped (dirty protection)", report)
	}
	if store.items[id].MetricPromptTemplate != "my local tweak {{response}}" {
		t.Error("dirty local edit was clobbered by re-import")
	}
}

// TestForkedIDAndNamespaceOf covers the pure id helpers, including the
// namespace-less (no "/") branch that a real (id-validated) import never reaches
// but which the helpers must still handle safely.
func TestForkedIDAndNamespaceOf(t *testing.T) {
	if got := forkedID("ns/a"); got != "ns-fork/a" {
		t.Errorf(`forkedID("ns/a") = %q, want "ns-fork/a"`, got)
	}
	if got := forkedID("bare"); got != "bare-fork" {
		t.Errorf(`forkedID("bare") = %q, want "bare-fork"`, got)
	}
	if got := namespaceOf("ns/a"); got != "ns" {
		t.Errorf(`namespaceOf("ns/a") = %q, want "ns"`, got)
	}
	if got := namespaceOf("bare"); got != "bare" {
		t.Errorf(`namespaceOf("bare") = %q, want "bare"`, got)
	}
}

// TestImportForkOverExistingForkTarget locks the R1 fix: the fork TARGET is never
// silently clobbered. An existing fork target that is dirty or a different
// version is left intact and reported as a conflict; an identical one is a no-op.
func TestImportForkOverExistingForkTarget(t *testing.T) {
	const id = "ns/a"
	forkID := forkedID(id)
	// Incoming upstream: equal version but differing content from the local ->
	// a genuine conflict that the fork strategy resolves by forking.
	incoming := func() MetricTemplate { return tmpl(id, "1.0.0", "upstream content") }

	seedLocalConflict := func(store *fakeStore) {
		local := loadedForm(t, tmpl(id, "1.0.0", "my local content"))
		store.items[id] = &local
	}

	t.Run("existing dirty fork target is not clobbered", func(t *testing.T) {
		store := newFakeStore()
		seedLocalConflict(store)
		fork := loadedForm(t, tmpl(forkID, "1.0.0", "edited fork content"))
		fork.Dirty = true
		store.items[forkID] = &fork
		putsBefore := store.putCalls
		svc := NewService(store)

		report, err := svc.Import(ctx(), writePack(t, incoming()), ImportOptions{Strategy: StrategyFork})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Conflicted != 1 || report.Forked != 0 {
			t.Fatalf("report = %+v; want 1 conflicted, 0 forked", report)
		}
		if store.putCalls != putsBefore {
			t.Errorf("conflicting fork target wrote to the store (%d puts)", store.putCalls-putsBefore)
		}
		if got := store.items[forkID]; got.MetricPromptTemplate != "edited fork content" || !got.Dirty {
			t.Errorf("dirty fork target was clobbered: %+v", got)
		}
	})

	t.Run("existing higher-version fork target is not clobbered", func(t *testing.T) {
		store := newFakeStore()
		seedLocalConflict(store)
		fork := loadedForm(t, tmpl(forkID, "2.0.0", "newer fork content"))
		store.items[forkID] = &fork
		svc := NewService(store)

		report, err := svc.Import(ctx(), writePack(t, incoming()), ImportOptions{Strategy: StrategyFork})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Conflicted != 1 || report.Forked != 0 {
			t.Fatalf("report = %+v; want 1 conflicted, 0 forked", report)
		}
		if got := store.items[forkID]; got.Version != "2.0.0" || got.MetricPromptTemplate != "newer fork content" {
			t.Errorf("higher-version fork target was clobbered: %+v", got)
		}
	})

	t.Run("identical fork target is a no-op", func(t *testing.T) {
		store := newFakeStore()
		seedLocalConflict(store)
		// The fork target already holds exactly what the fork would write.
		fork := loadedForm(t, tmpl(forkID, "1.0.0", "upstream content"))
		store.items[forkID] = &fork
		putsBefore := store.putCalls
		svc := NewService(store)

		report, err := svc.Import(ctx(), writePack(t, incoming()), ImportOptions{Strategy: StrategyFork})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Unchanged != 1 || report.Forked != 0 || report.Conflicted != 0 {
			t.Fatalf("report = %+v; want 1 unchanged", report)
		}
		if store.putCalls != putsBefore {
			t.Errorf("no-op fork target wrote to the store (%d puts)", store.putCalls-putsBefore)
		}
	})

	t.Run("single run: earlier fork target in same pack is not clobbered", func(t *testing.T) {
		const base = "acme/x"
		fid := forkedID(base) // acme-fork/x, which sorts BEFORE acme/x
		store := newFakeStore()
		local := loadedForm(t, tmpl(base, "1.0.0", "local x"))
		store.items[base] = &local
		src := writeMultiPack(t,
			tmpl(fid, "1.0.0", "unrelated fork template"),
			tmpl(base, "1.0.0", "upstream x differs"),
		)
		svc := NewService(store)

		report, err := svc.Import(ctx(), src, ImportOptions{Strategy: StrategyFork})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		// acme-fork/x is inserted first; acme/x then tries to fork onto it but it
		// exists and differs -> conflicted, not overwritten.
		if report.Inserted != 1 || report.Conflicted != 1 || report.Forked != 0 {
			t.Fatalf("report = %+v; want 1 inserted, 1 conflicted, 0 forked", report)
		}
		if got := store.items[fid]; got.MetricPromptTemplate != "unrelated fork template" {
			t.Errorf("fork target clobbered within one import: %+v", got)
		}
	})
}

// TestImportMixedBatch imports one pack whose templates hit every outcome and
// asserts the ImportReport counts partition the batch exactly. Run under the fork
// strategy so a conflict with a free fork id forks while a conflict whose fork
// target is occupied is reported as a conflict (exercising the R1 guard in bulk).
func TestImportMixedBatch(t *testing.T) {
	store := newFakeStore()
	seed := func(id, ver, prompt string) {
		l := loadedForm(t, tmpl(id, ver, prompt))
		store.items[id] = &l
	}
	seed("ns/upg", "1.0.0", "p")     // -> updated (incoming v2)
	seed("ns/old", "2.0.0", "p")     // -> skipped (incoming v1, older)
	seed("ns/same", "1.0.0", "p")    // -> unchanged (identical incoming)
	seed("ns/frk", "1.0.0", "local") // -> forked (conflict, free fork id)
	seed("ns/cfl", "1.0.0", "local") // -> conflicted (conflict, occupied fork id)
	// Occupy ns/cfl's fork target with differing content so the fork is refused.
	occupied := loadedForm(t, tmpl(forkedID("ns/cfl"), "1.0.0", "occupied"))
	store.items[forkedID("ns/cfl")] = &occupied

	src := writeMultiPack(t,
		tmpl("ns/ins", "1.0.0", "p"),            // absent -> inserted
		tmpl("ns/upg", "2.0.0", "p"),            // upgrade -> updated
		tmpl("ns/old", "1.0.0", "p"),            // older -> skipped
		tmpl("ns/same", "1.0.0", "p"),           // identical -> unchanged
		tmpl("ns/frk", "1.0.0", "upstream frk"), // conflict, free fork id -> forked
		tmpl("ns/cfl", "1.0.0", "upstream cfl"), // conflict, occupied fork id -> conflicted
	)
	svc := NewService(store)

	report, err := svc.Import(ctx(), src, ImportOptions{Strategy: StrategyFork})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Inserted != 1 || report.Updated != 1 || report.Skipped != 1 ||
		report.Unchanged != 1 || report.Forked != 1 || report.Conflicted != 1 {
		t.Fatalf("count partition wrong: %+v", report)
	}
	if n := len(report.Entries); n != 6 {
		t.Errorf("entries = %d, want 6 (one per template)", n)
	}
}

// TestImportStoreErrors covers the store-error propagation paths inside the
// reconcile helpers and the backend load, using fakeStore error injection.
func TestImportStoreErrors(t *testing.T) {
	sentinel := errors.New("store boom")

	t.Run("insert put error", func(t *testing.T) {
		store := newFakeStore()
		store.putErr = sentinel
		svc := NewService(store)
		_, err := svc.Import(ctx(), writePack(t, tmpl("ns/a", "1.0.0", "p")), ImportOptions{})
		if !errors.Is(err, sentinel) {
			t.Fatalf("got %v, want %v", err, sentinel)
		}
	})

	t.Run("update put error", func(t *testing.T) {
		store := newFakeStore()
		local := loadedForm(t, tmpl("ns/a", "1.0.0", "p"))
		store.items["ns/a"] = &local
		store.putErr = sentinel
		svc := NewService(store)
		_, err := svc.Import(ctx(), writePack(t, tmpl("ns/a", "2.0.0", "p")), ImportOptions{})
		if !errors.Is(err, sentinel) {
			t.Fatalf("got %v, want %v", err, sentinel)
		}
	})

	t.Run("fork put error", func(t *testing.T) {
		store := newFakeStore()
		local := loadedForm(t, tmpl("ns/a", "1.0.0", "local"))
		store.items["ns/a"] = &local
		store.putErr = sentinel
		svc := NewService(store)
		_, err := svc.Import(ctx(), writePack(t, tmpl("ns/a", "1.0.0", "upstream")), ImportOptions{Strategy: StrategyFork})
		if !errors.Is(err, sentinel) {
			t.Fatalf("got %v, want %v", err, sentinel)
		}
	})

	t.Run("reconcile get error is propagated", func(t *testing.T) {
		store := newFakeStore()
		store.getErr = sentinel // non-ErrNotFound Get failure
		svc := NewService(store)
		_, err := svc.Import(ctx(), writePack(t, tmpl("ns/a", "1.0.0", "p")), ImportOptions{})
		if !errors.Is(err, sentinel) {
			t.Fatalf("got %v, want %v", err, sentinel)
		}
	})

	t.Run("backend load error", func(t *testing.T) {
		store := newFakeStore()
		svc := NewService(store)
		_, err := svc.Import(ctx(), filepath.Join(t.TempDir(), "does-not-exist"), ImportOptions{})
		if err == nil {
			t.Fatal("expected a load error for a missing source path")
		}
	})
}

// TestImportDryRunForkAndOverwrite asserts --dry-run writes nothing for the fork
// and overwrite outcomes (the insert/update dry runs are covered separately).
func TestImportDryRunForkAndOverwrite(t *testing.T) {
	const id = "ns/a"

	t.Run("fork", func(t *testing.T) {
		store := newFakeStore()
		local := loadedForm(t, tmpl(id, "1.0.0", "local"))
		store.items[id] = &local
		putsBefore := store.putCalls
		svc := NewService(store)

		report, err := svc.Import(ctx(), writePack(t, tmpl(id, "1.0.0", "upstream")), ImportOptions{Strategy: StrategyFork, DryRun: true})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Forked != 1 {
			t.Fatalf("report = %+v; want 1 forked", report)
		}
		if store.putCalls != putsBefore {
			t.Errorf("dry-run fork wrote to the store (%d puts)", store.putCalls-putsBefore)
		}
		if _, ok := store.items[forkedID(id)]; ok {
			t.Error("dry-run fork created the fork target")
		}
	})

	t.Run("overwrite conflict", func(t *testing.T) {
		store := newFakeStore()
		local := loadedForm(t, tmpl(id, "1.0.0", "local"))
		store.items[id] = &local
		putsBefore := store.putCalls
		svc := NewService(store)

		report, err := svc.Import(ctx(), writePack(t, tmpl(id, "1.0.0", "upstream")), ImportOptions{Strategy: StrategyOverwrite, DryRun: true})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Updated != 1 {
			t.Fatalf("report = %+v; want 1 updated", report)
		}
		if store.putCalls != putsBefore {
			t.Errorf("dry-run overwrite wrote to the store (%d puts)", store.putCalls-putsBefore)
		}
		if got := store.items[id]; got.MetricPromptTemplate != "local" {
			t.Errorf("dry-run overwrite mutated the store: %+v", got)
		}
	})
}

// TestImportDirtyBeatsOlderAndEqual checks the dirty row wins for non-upgrade
// incomings too (the matrix's "dirty -> any" cell), not only for an upgrade.
func TestImportDirtyBeatsOlderAndEqual(t *testing.T) {
	const id = "ns/a"

	t.Run("dirty local, older incoming", func(t *testing.T) {
		store := newFakeStore()
		local := loadedForm(t, tmpl(id, "2.0.0", "local edit"))
		local.Dirty = true
		store.items[id] = &local
		putsBefore := store.putCalls
		svc := NewService(store)

		report, err := svc.Import(ctx(), writePack(t, tmpl(id, "1.0.0", "upstream older")), ImportOptions{})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Skipped != 1 {
			t.Fatalf("report = %+v; want 1 skipped", report)
		}
		if store.putCalls != putsBefore {
			t.Error("dirty+older wrote to the store")
		}
		if got := store.items[id]; got.MetricPromptTemplate != "local edit" {
			t.Errorf("dirty local clobbered: %+v", got)
		}
	})

	t.Run("dirty local, equal version differing incoming", func(t *testing.T) {
		store := newFakeStore()
		local := loadedForm(t, tmpl(id, "1.0.0", "local edit"))
		local.Dirty = true
		store.items[id] = &local
		putsBefore := store.putCalls
		svc := NewService(store)

		report, err := svc.Import(ctx(), writePack(t, tmpl(id, "1.0.0", "upstream differs")), ImportOptions{})
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if report.Skipped != 1 {
			t.Fatalf("report = %+v; want 1 skipped", report)
		}
		if store.putCalls != putsBefore {
			t.Error("dirty+equal-differs wrote to the store")
		}
		if got := store.items[id]; got.MetricPromptTemplate != "local edit" {
			t.Errorf("dirty local clobbered: %+v", got)
		}
	})
}

// TestImportRejectsMalformedID confirms the ingest id-shape guard still runs in
// the reconciliation Import path: a pack whose metadata.id is not a valid
// "<namespace>/<slug>" is rejected before anything is written.
func TestImportRejectsMalformedID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "packs", "bad", "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Uppercase letters violate the ^[a-z0-9-]+/[a-z0-9-]+$ id shape.
	doc := "apiVersion: mizan.dev/v1alpha1\n" +
		"kind: MetricTemplate\n" +
		"metadata:\n" +
		"  id: Bad/ID\n" +
		"spec:\n" +
		"  kind: pointwise\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store := newFakeStore()
	svc := NewService(store)
	if _, err := svc.Import(ctx(), root, ImportOptions{}); err == nil {
		t.Fatal("expected import to reject a malformed metadata.id")
	}
	if store.putCalls != 0 {
		t.Errorf("malformed id reached the store (%d puts)", store.putCalls)
	}
}

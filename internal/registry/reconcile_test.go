package registry

// reconcile_test.go exercises the full §3.8 import conflict matrix (design/
// collaboration-design.md §3.8) as a table: every (local state × incoming ×
// strategy) row is asserted against Service.Import, plus dirty protection, the
// no-op hash short-circuit, and --dry-run.

import (
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

package registry

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Service is the single façade that the CLI, GUI, and eval engine depend on.
// In P1 it exposes CRUD over the local Store only; import/export and the
// contribution channel (Codec/SyncBackend) are added in P2 without changing
// this type's consumers (design/collaboration-design.md §3.1).
type Service struct {
	store   Store
	codec   Codec
	syncCfg SyncConfig
}

// Option configures a Service. Options keep NewService backward-compatible: P1
// callers that pass no options get the sane defaults (YAML codec, zero-value
// SyncConfig), so existing CRUD behavior is unchanged (design §3.1).
type Option func(*Service)

// WithCodec sets the pack codec used by Import/Export. Defaults to YAMLCodec.
func WithCodec(c Codec) Option {
	return func(s *Service) {
		if c != nil {
			s.codec = c
		}
	}
}

// WithSyncConfig injects the ambient sync settings (pack cache dir, default
// templates repo) the composition root derives from config.
func WithSyncConfig(cfg SyncConfig) Option {
	return func(s *Service) { s.syncCfg = cfg }
}

// NewService returns a Service backed by the given Store. With no options it
// uses the default YAMLCodec and a zero-value SyncConfig, so P1 callers are
// unaffected; wire.OpenService injects the codec + config-derived SyncConfig.
func NewService(store Store, opts ...Option) *Service {
	s := &Service{store: store, codec: NewYAMLCodec()}
	for _, opt := range opts {
		opt(s)
	}
	if s.codec == nil {
		s.codec = NewYAMLCodec()
	}
	return s
}

// Create inserts a new template. It fails if a template with the same ID
// already exists (use Update to modify an existing one).
func (s *Service) Create(ctx context.Context, t MetricTemplate) error {
	if t.ID == "" {
		return fmt.Errorf("registry: template ID is required")
	}
	if _, err := s.store.Get(ctx, t.ID); err == nil {
		return fmt.Errorf("registry: template %q already exists", t.ID)
	} else if err != ErrNotFound {
		return err
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	return s.store.Put(ctx, &t)
}

// Update modifies an existing template (must already exist). A local edit marks
// the template Dirty so a subsequent Import respects dirty protection and does
// not silently clobber the user's changes (design §3.9/§3.8 dirty row). Provenance
// (Source/ContentHash/ImportedAt) is preserved from the stored copy — those are
// set by Import, never by a local edit.
func (s *Service) Update(ctx context.Context, t MetricTemplate) error {
	if t.ID == "" {
		return fmt.Errorf("registry: template ID is required")
	}
	existing, err := s.store.Get(ctx, t.ID)
	if err != nil {
		return err
	}
	t.CreatedAt = existing.CreatedAt
	t.UpdatedAt = time.Now().UTC()
	t.Dirty = true
	return s.store.Put(ctx, &t)
}

// Get returns the template with the given ID, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (*MetricTemplate, error) {
	return s.store.Get(ctx, id)
}

// List returns templates matching the filter.
func (s *Service) List(ctx context.Context, f ListFilter) ([]MetricTemplate, error) {
	return s.store.List(ctx, f)
}

// Delete removes a template by ID.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// ImportStrategy selects how Import reconciles an incoming template against a
// local one with the same id (design/collaboration-design.md §3.8). The owner
// decision (D2) fixes the default to StrategyNewer.
type ImportStrategy string

const (
	// StrategyNewer (default) takes the higher-version side: an incoming upgrade
	// updates, an older incoming is skipped, and an equal-version/hash-differs
	// clash is reported as a conflict and skipped (never silently clobbered).
	StrategyNewer ImportStrategy = "newer"
	// StrategySkip never overwrites an existing template; only absent ids insert.
	StrategySkip ImportStrategy = "skip"
	// StrategyOverwrite unconditionally replaces the local copy with the incoming
	// one (including dirty local edits) — the escape hatch for a resolved conflict.
	StrategyOverwrite ImportStrategy = "overwrite"
	// StrategyFork keeps local edits while still pulling upstream: on a genuine
	// conflict (equal-version/hash-differs) or over a dirty local, the incoming is
	// imported under a distinct "<ns>-fork/<slug>" id, leaving the local untouched.
	StrategyFork ImportStrategy = "fork"
)

// normalize returns the effective strategy (empty -> newer) and whether it is a
// recognized value.
func (st ImportStrategy) normalize() (ImportStrategy, bool) {
	switch st {
	case "":
		return StrategyNewer, true
	case StrategyNewer, StrategySkip, StrategyOverwrite, StrategyFork:
		return st, true
	default:
		return st, false
	}
}

// ImportOptions configures an Import.
type ImportOptions struct {
	// Strategy selects reconciliation behavior for id collisions. Empty means the
	// default, StrategyNewer (D2).
	Strategy ImportStrategy
	// DryRun computes and returns the full ImportReport WITHOUT writing anything to
	// the store — a preview of exactly what a real import would do.
	DryRun bool
	// Namespace, when set, imports only templates whose id namespace (the
	// "<namespace>/" prefix) equals it; every other loaded template is ignored.
	// Empty means import all discovered templates (P2.5 --namespace filter).
	Namespace string
}

// ImportAction is the outcome recorded per template in an ImportReport.
type ImportAction string

const (
	// ActionInserted means the template was absent and was inserted.
	ActionInserted ImportAction = "inserted"
	// ActionUpdated means an existing template was replaced by the incoming one.
	ActionUpdated ImportAction = "updated"
	// ActionSkipped means the incoming template was intentionally not applied
	// (older version, dirty local under a non-overwrite strategy, or skip strategy).
	ActionSkipped ImportAction = "skipped"
	// ActionConflicted means an equal-version/hash-differs clash was detected and
	// left unresolved: skipped and reported so the user re-runs with an explicit
	// --strategy (D2).
	ActionConflicted ImportAction = "conflicted"
	// ActionUnchanged means the incoming template is byte-identical (same
	// contentHash) to the local one — a no-op, not counted as an update.
	ActionUnchanged ImportAction = "unchanged"
	// ActionForked means the incoming template was imported under a derived
	// "<ns>-fork/<slug>" id, preserving the local copy.
	ActionForked ImportAction = "forked"
)

// ImportEntry is the per-template record in an ImportReport.
type ImportEntry struct {
	ID     string
	Action ImportAction
	Reason string // human-readable note (e.g. why it was skipped, the fork id)
}

// ImportReport summarizes an Import run: per-action counts plus a per-template
// entry with a reason. The counts partition the processed templates (each
// template increments exactly one).
type ImportReport struct {
	Source     SourceInfo
	Strategy   ImportStrategy
	DryRun     bool
	Inserted   int
	Updated    int
	Skipped    int
	Conflicted int
	Unchanged  int
	Forked     int
	Entries    []ImportEntry
}

// Import reads templates from a source and reconciles them into the local store
// per the §3.8 conflict matrix, keyed by metadata.id and driven by
// opt.Strategy (default StrategyNewer, D2).
//
// Behavior summary (local state -> action under the chosen strategy):
//   - absent                          -> insert (all strategies)
//   - present, same content (hash)    -> unchanged/no-op (all strategies)
//   - present, dirty local edit       -> skip+warn (newer/skip) | update (overwrite) | fork
//   - present, incoming version >      -> update (newer/overwrite/fork) | skip
//   - present, incoming version <      -> skip (newer/skip/fork) | update (overwrite)
//   - present, equal version, differs -> conflict (newer) | skip | update (overwrite) | fork
//
// A no-op short-circuit compares recomputed content hashes (not the stored
// stamp), so an unchanged re-import is never counted as an update and a dirty
// local whose content still matches upstream is correctly a no-op.
//
// When opt.DryRun is set nothing is written; the returned report is exactly what
// a real import would produce.
//
// WIRING (design §3.1, the seam-preserving shape): Import takes a source STRING
// plus options — not a SyncBackend value — and constructs the GitPackBackend
// internally from the injected SyncConfig. This keeps cmd/* free of the
// sync/codec packages.
func (s *Service) Import(ctx context.Context, src string, opt ImportOptions) (ImportReport, error) {
	if strings.TrimSpace(src) == "" {
		// Bare import: default to the configured templates repo (P2.5). This is the
		// ONLY default-source wiring — the source is still a per-operation string,
		// so switching source needs no cmd/wire change (the seam-pivot property).
		src = s.syncCfg.DefaultTemplatesRepo
		if strings.TrimSpace(src) == "" {
			return ImportReport{}, fmt.Errorf("registry: no import source given and no default templates repo configured (set templates-repo)")
		}
	}
	strategy, ok := opt.Strategy.normalize()
	if !ok {
		return ImportReport{}, fmt.Errorf("registry: unknown import strategy %q (want one of: newer|skip|overwrite|fork)", opt.Strategy)
	}

	backend := NewGitPackBackend(src, s.codec, s.syncCfg)
	info := backend.Describe()

	templates, err := backend.Load(ctx)
	if err != nil {
		return ImportReport{}, err
	}

	report := ImportReport{Source: info, Strategy: strategy, DryRun: opt.DryRun}
	now := time.Now().UTC()
	for i := range templates {
		// --namespace filter: import only templates in the requested namespace,
		// applied before reconciliation so the existing P2.3 reconcile path is
		// reused unchanged for exactly the selected templates.
		if opt.Namespace != "" && namespaceOf(templates[i].ID) != opt.Namespace {
			continue
		}
		if err := s.reconcileOne(ctx, templates[i], strategy, info.Origin, now, opt.DryRun, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// reconcileOne resolves a single incoming template against the store per §3.8 and
// records the outcome in report, writing through the store unless dryRun is set.
func (s *Service) reconcileOne(ctx context.Context, incoming MetricTemplate, strategy ImportStrategy, origin string, now time.Time, dryRun bool, report *ImportReport) error {
	local, err := s.store.Get(ctx, incoming.ID)
	switch {
	case err == ErrNotFound:
		return s.applyInsert(ctx, incoming, origin, now, dryRun, report, ActionInserted, "")
	case err != nil:
		return err
	}

	// Compare RECOMPUTED content hashes so the no-op check reflects the actual
	// current content on both sides, independent of any stale stored stamp.
	if contentHash(&incoming) == contentHash(local) {
		report.Unchanged++
		report.addEntry(incoming.ID, ActionUnchanged, "unchanged (same content)")
		return nil
	}

	if local.Dirty {
		return s.resolveDirty(ctx, incoming, local.CreatedAt, strategy, origin, now, dryRun, report)
	}

	cmp, comparable := compareVersions(incoming.Version, local.Version)
	switch {
	case comparable && cmp > 0: // incoming is an upgrade
		return s.resolveUpgrade(ctx, incoming, local.CreatedAt, strategy, origin, now, dryRun, report)
	case comparable && cmp < 0: // incoming is older
		return s.resolveOlder(ctx, incoming, local.CreatedAt, strategy, origin, now, dryRun, report)
	default: // equal version, or incomparable, with differing content -> conflict
		return s.resolveConflict(ctx, incoming, local.CreatedAt, strategy, origin, now, dryRun, report)
	}
}

func (s *Service) resolveDirty(ctx context.Context, incoming MetricTemplate, createdAt time.Time, strategy ImportStrategy, origin string, now time.Time, dryRun bool, report *ImportReport) error {
	switch strategy {
	case StrategyOverwrite:
		return s.applyUpdate(ctx, incoming, createdAt, origin, now, dryRun, report, "overwrote dirty local edit")
	case StrategyFork:
		return s.applyFork(ctx, incoming, origin, now, dryRun, report, "local is dirty")
	default: // newer, skip -> protect the local edit
		report.Skipped++
		report.addEntry(incoming.ID, ActionSkipped, "skipped: local edit (dirty) — re-run with --strategy overwrite or fork to pull upstream")
		return nil
	}
}

func (s *Service) resolveUpgrade(ctx context.Context, incoming MetricTemplate, createdAt time.Time, strategy ImportStrategy, origin string, now time.Time, dryRun bool, report *ImportReport) error {
	switch strategy {
	case StrategySkip:
		report.Skipped++
		report.addEntry(incoming.ID, ActionSkipped, "skipped: newer upstream available (strategy=skip)")
		return nil
	default: // newer, overwrite, fork all take the upgrade
		return s.applyUpdate(ctx, incoming, createdAt, origin, now, dryRun, report, "updated to newer upstream version")
	}
}

func (s *Service) resolveOlder(ctx context.Context, incoming MetricTemplate, createdAt time.Time, strategy ImportStrategy, origin string, now time.Time, dryRun bool, report *ImportReport) error {
	switch strategy {
	case StrategyOverwrite:
		return s.applyUpdate(ctx, incoming, createdAt, origin, now, dryRun, report, "overwrote local with older upstream (strategy=overwrite)")
	default: // newer, skip, fork keep the newer local
		report.Skipped++
		report.addEntry(incoming.ID, ActionSkipped, "skipped: local version is newer than upstream")
		return nil
	}
}

func (s *Service) resolveConflict(ctx context.Context, incoming MetricTemplate, createdAt time.Time, strategy ImportStrategy, origin string, now time.Time, dryRun bool, report *ImportReport) error {
	switch strategy {
	case StrategySkip:
		report.Skipped++
		report.addEntry(incoming.ID, ActionSkipped, "skipped: same version, different content (strategy=skip)")
		return nil
	case StrategyOverwrite:
		return s.applyUpdate(ctx, incoming, createdAt, origin, now, dryRun, report, "overwrote local on same-version/content conflict")
	case StrategyFork:
		return s.applyFork(ctx, incoming, origin, now, dryRun, report, "same version, different content")
	default: // newer -> report the conflict, do not clobber (D2)
		report.Conflicted++
		report.addEntry(incoming.ID, ActionConflicted, "conflict: same version, different content — re-run with --strategy overwrite or fork")
		return nil
	}
}

// applyInsert stamps provenance on an absent template and inserts it.
func (s *Service) applyInsert(ctx context.Context, t MetricTemplate, origin string, now time.Time, dryRun bool, report *ImportReport, action ImportAction, reason string) error {
	stampImported(&t, origin, now, true)
	if !dryRun {
		if err := s.store.Put(ctx, &t); err != nil {
			return err
		}
	}
	report.Inserted++
	report.addEntry(t.ID, action, reason)
	return nil
}

// applyUpdate stamps provenance and replaces an existing template, preserving its
// original CreatedAt. The createdAt is the value reconcileOne already loaded from
// the store, so no redundant Get is needed here.
func (s *Service) applyUpdate(ctx context.Context, t MetricTemplate, createdAt time.Time, origin string, now time.Time, dryRun bool, report *ImportReport, reason string) error {
	stampImported(&t, origin, now, false)
	t.CreatedAt = createdAt
	if !dryRun {
		if err := s.store.Put(ctx, &t); err != nil {
			return err
		}
	}
	report.Updated++
	report.addEntry(t.ID, ActionUpdated, reason)
	return nil
}

// applyFork imports the incoming template under a derived "<ns>-fork/<slug>" id,
// leaving the local copy untouched. The fork TARGET is itself protected from a
// silent clobber (D2 LOCKED "never silently clobber a dirty local edit"): if a
// template already exists at the fork id, an identical one is a no-op and a
// differing one (including a dirty or higher-version edited fork) is reported as
// a conflict and NOT overwritten. The fork id is derived from the ORIGINAL
// incoming id, so t.ID is set to forkID before the content comparison so both
// sides hash under the same id (contentHash includes the id).
//
// DRY-RUN REPORT FIDELITY: under dryRun the fork-target existence check still
// reads the store, but earlier entries in the SAME import are not persisted, so a
// pack that both inserts "<ns>-fork/x" and forks "<ns>/x" onto it will report the
// fork as "forked" rather than the "conflicted" a real run yields (the un-written
// insert is invisible to this Get). This is a preview-only reporting artifact:
// dry-run writes nothing, and a real (non-dry-run) import reconciles it correctly.
func (s *Service) applyFork(ctx context.Context, t MetricTemplate, origin string, now time.Time, dryRun bool, report *ImportReport, why string) error {
	forkID := forkedID(t.ID)
	t.ID = forkID
	if existing, err := s.store.Get(ctx, forkID); err == nil {
		// Fork target already exists: never silently clobber it.
		if contentHash(&t) == contentHash(existing) {
			report.Unchanged++
			report.addEntry(forkID, ActionUnchanged, fmt.Sprintf("fork target %s already up to date", forkID))
			return nil
		}
		report.Conflicted++
		report.addEntry(forkID, ActionConflicted, fmt.Sprintf("fork target %s already exists and differs — resolve manually", forkID))
		return nil
	} else if err != ErrNotFound {
		return err
	}
	stampImported(&t, origin, now, true)
	if !dryRun {
		if err := s.store.Put(ctx, &t); err != nil {
			return err
		}
	}
	report.Forked++
	report.addEntry(forkID, ActionForked, fmt.Sprintf("forked (%s) — imported as %s, local kept", why, forkID))
	return nil
}

// stampImported sets the provenance/lifecycle fields on a freshly imported or
// updated template. A fresh import (isInsert) sets CreatedAt; an update preserves
// the caller-supplied CreatedAt (set by applyUpdate).
func stampImported(t *MetricTemplate, origin string, now time.Time, isInsert bool) {
	t.Source = fmt.Sprintf("pack:%s@%s", namespaceOf(t.ID), origin)
	t.ContentHash = contentHash(t)
	t.ImportedAt = now
	t.UpdatedAt = now
	if isInsert {
		t.CreatedAt = now
	}
	t.Dirty = false
}

func (r *ImportReport) addEntry(id string, action ImportAction, reason string) {
	r.Entries = append(r.Entries, ImportEntry{ID: id, Action: action, Reason: reason})
}

// forkedID derives the fork target id "<ns>-fork/<slug>" from "<ns>/<slug>".
// An id with no namespace slash is suffixed as "<id>-fork".
func forkedID(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i] + "-fork" + id[i:]
	}
	return id + "-fork"
}

// namespaceOf returns the namespace prefix of a "<namespace>/<slug>" template
// id, or the whole id if it carries no slash.
func namespaceOf(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return id
}

// --- P2.4: export + pack authoring ------------------------------------------

// Selector chooses which stored templates an Export writes. Exactly one of the
// three modes must be set: a single ID, a Namespace (all templates whose id
// begins "<namespace>/"), or All. It mirrors the `registry export`
// `--id | --namespace | --all` flag group (design §3.7).
type Selector struct {
	ID        string // export the one template with this exact id
	Namespace string // export every template in this namespace
	All       bool   // export every template in the store
}

// ExportAction is the outcome recorded per template in an ExportReport.
type ExportAction string

const (
	// ExportWritten means the template was written to the pack dir.
	ExportWritten ExportAction = "written"
	// ExportSkipped means the template was skipped because its id failed the
	// "<namespace>/<slug>" shape guard, so no path-safe filename could be derived
	// (audit rec#3); it is reported, never written.
	ExportSkipped ExportAction = "skipped"
)

// ExportEntry is the per-template record in an ExportReport.
type ExportEntry struct {
	ID     string
	Path   string // path written, relative to the pack dir (e.g. "templates/foo.yaml")
	Action ExportAction
	Reason string // human-readable note (e.g. why it was skipped)
}

// ExportReport summarizes an Export run.
type ExportReport struct {
	Dest    string
	Written int
	Skipped int
	Entries []ExportEntry
}

// Export writes selected templates from the local store into a pack directory at
// dst (one file per template under dst/templates/, design §3.3). It is the write
// side of the round-trip: export → (human commits + PRs) → import.
//
// Namespaced-id enforcement on write (audit rec#3): the output filename is
// derived from a VALIDATED slug via slugForID, never from a raw id, so a
// malformed or hostile stored id (a second '/', a "..", a control character)
// cannot escape the templates/ dir. Such an id is SKIPPED and reported rather
// than written. custom_schema templates have their responseSchema canonicalized
// by the codec on write, so a CLI-authored template's key order does not drift a
// later re-export or its contentHash (P2.1 review #2).
func (s *Service) Export(ctx context.Context, dst string, sel Selector) (ExportReport, error) {
	if strings.TrimSpace(dst) == "" {
		return ExportReport{}, fmt.Errorf("registry: export destination is required (a pack dir, e.g. packs/<name>)")
	}
	templates, err := s.selectForExport(ctx, sel)
	if err != nil {
		return ExportReport{}, err
	}

	report := ExportReport{Dest: dst}
	ext := s.codec.Ext()
	valid := make([]MetricTemplate, 0, len(templates))
	// Written entries are staged, not yet committed to the report: a template's
	// "written" status is only true once Save has landed all files. Skips, by
	// contrast, are decided here (they never reach Save) so they go into the
	// report immediately.
	writtenEntries := make([]ExportEntry, 0, len(templates))
	for _, t := range templates {
		slug, err := slugForID(t.ID)
		if err != nil {
			report.Skipped++
			report.Entries = append(report.Entries, ExportEntry{
				ID:     t.ID,
				Action: ExportSkipped,
				Reason: err.Error(),
			})
			continue
		}
		valid = append(valid, t)
		writtenEntries = append(writtenEntries, ExportEntry{
			ID:     t.ID,
			Path:   filepath.Join("templates", slug+"."+ext),
			Action: ExportWritten,
		})
	}

	backend := NewGitPackBackend(dst, s.codec, s.syncCfg)
	if err := backend.Save(ctx, valid); err != nil {
		// Save is all-or-nothing from the report's perspective: on failure the
		// report claims no writes, so a caller that inspects it on the error path
		// never sees files that did not land (review Opt#2). Skips are already
		// recorded (they never reached Save).
		return report, err
	}
	// Save succeeded: promote the staged writes into the report.
	report.Written = len(writtenEntries)
	report.Entries = append(report.Entries, writtenEntries...)
	return report, nil
}

// selectForExport resolves a Selector to the templates to export, sorted by id
// for deterministic output (so an export dir and its report are byte-stable).
// Exactly one selector mode must be set.
func (s *Service) selectForExport(ctx context.Context, sel Selector) ([]MetricTemplate, error) {
	modes := 0
	if sel.ID != "" {
		modes++
	}
	if sel.Namespace != "" {
		modes++
	}
	if sel.All {
		modes++
	}
	if modes != 1 {
		return nil, fmt.Errorf("registry: export requires exactly one of --id, --namespace, or --all")
	}

	switch {
	case sel.ID != "":
		t, err := s.store.Get(ctx, sel.ID)
		if err != nil {
			return nil, err
		}
		return []MetricTemplate{*t}, nil
	case sel.Namespace != "":
		ts, err := s.store.List(ctx, ListFilter{Namespace: sel.Namespace})
		if err != nil {
			return nil, err
		}
		// Defensive prefix filter: not every Store honors ListFilter.Namespace,
		// so re-check here rather than trust the query to have narrowed it.
		out := make([]MetricTemplate, 0, len(ts))
		for _, t := range ts {
			if namespaceOf(t.ID) == sel.Namespace {
				out = append(out, t)
			}
		}
		sortByID(out)
		return out, nil
	default:
		ts, err := s.store.List(ctx, ListFilter{})
		if err != nil {
			return nil, err
		}
		sortByID(ts)
		return ts, nil
	}
}

// sortByID orders templates by id in place for deterministic export.
func sortByID(ts []MetricTemplate) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
}

// InitPack scaffolds an empty, valid pack directory at dir with metadata.name =
// namespace: a mizan-pack.yaml manifest, an empty templates/ dir, and an empty
// evalsets/ dir (the §3.4a EvalSet carriage hook). It emits NO CI workflow
// (design §6/P2.4). It is exposed on the Service so cmd/* scaffolds a pack
// through the façade without importing the codec/YAML packages (the seam).
func (s *Service) InitPack(_ context.Context, dir, namespace string) error {
	return scaffoldPack(dir, namespace)
}

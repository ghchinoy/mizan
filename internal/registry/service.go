package registry

import (
	"context"
	"fmt"
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

// Update modifies an existing template (must already exist).
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

// ImportOptions configures an Import. It is a skeleton in P2.1 (insert-only);
// reconciliation strategies, namespace filters/remaps, and --dry-run land in
// P2.3/P2.5. Its presence now keeps the Import signature stable across phases.
type ImportOptions struct{}

// ImportAction is the outcome recorded per template in an ImportReport.
type ImportAction string

const (
	// ActionInserted means the template was absent and was inserted.
	ActionInserted ImportAction = "inserted"
	// ActionSkipped means a template with the same ID already existed. In P2.1
	// (insert-only) any conflict is skipped; the full strategy matrix is P2.3.
	ActionSkipped ImportAction = "skipped"
)

// ImportEntry is the per-template record in an ImportReport.
type ImportEntry struct {
	ID     string
	Action ImportAction
	Reason string // human-readable note (e.g. why it was skipped)
}

// ImportReport summarizes an Import run. In P2.1 only Inserted/Skipped are
// possible; Updated/Conflicted counts arrive with the P2.3 reconciliation.
type ImportReport struct {
	Source   SourceInfo
	Inserted int
	Skipped  int
	Entries  []ImportEntry
}

// Import reads templates from a source and lands new ones in the local store.
//
// P2.1 reconciliation is INSERT-ONLY: a template whose ID is absent is inserted
// (via Store.Put) with Source="pack:<ns>@<origin>", a computed ContentHash, and
// ImportedAt set; a template whose ID already exists is SKIPPED and recorded in
// the report. The full conflict matrix + strategies (newer/skip/overwrite/fork)
// and dirty protection are P2.3 (design §3.8) and are intentionally not built
// here.
//
// WIRING (design §3.1, the seam-preserving shape): Import takes a source STRING
// plus options — not a SyncBackend value — and constructs the GitPackBackend
// internally from the injected SyncConfig. This keeps cmd/* free of the
// sync/codec packages.
func (s *Service) Import(ctx context.Context, src string, _ ImportOptions) (ImportReport, error) {
	if strings.TrimSpace(src) == "" {
		// Bare import (default source) is P2.5; P2.1 requires an explicit path.
		return ImportReport{}, fmt.Errorf("registry: import source is required (a local checkout path); default-source import is P2.5")
	}

	backend := NewGitPackBackend(src, s.codec, s.syncCfg)
	info := backend.Describe()

	templates, err := backend.Load(ctx)
	if err != nil {
		return ImportReport{}, err
	}

	report := ImportReport{Source: info}
	now := time.Now().UTC()
	for i := range templates {
		t := templates[i]
		if _, err := s.store.Get(ctx, t.ID); err == nil {
			report.Skipped++
			report.Entries = append(report.Entries, ImportEntry{
				ID:     t.ID,
				Action: ActionSkipped,
				Reason: "already exists (P2.1 is insert-only; use --strategy in P2.3)",
			})
			continue
		} else if err != ErrNotFound {
			return report, err
		}

		t.Source = fmt.Sprintf("pack:%s@%s", namespaceOf(t.ID), info.Origin)
		t.ContentHash = contentHash(&t)
		t.ImportedAt = now
		t.CreatedAt = now
		t.UpdatedAt = now
		t.Dirty = false
		if err := s.store.Put(ctx, &t); err != nil {
			return report, err
		}
		report.Inserted++
		report.Entries = append(report.Entries, ImportEntry{ID: t.ID, Action: ActionInserted})
	}
	return report, nil
}

// namespaceOf returns the namespace prefix of a "<namespace>/<slug>" template
// id, or the whole id if it carries no slash.
func namespaceOf(id string) string {
	if i := strings.IndexByte(id, '/'); i >= 0 {
		return id[:i]
	}
	return id
}

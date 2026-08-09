package registry

import (
	"context"
	"fmt"
	"time"
)

// Service is the single façade that the CLI, GUI, and eval engine depend on.
// In P1 it exposes CRUD over the local Store only; import/export and the
// contribution channel (Codec/SyncBackend) are added in P2 without changing
// this type's consumers (design/collaboration-design.md §3.1).
type Service struct {
	store Store
}

// NewService returns a Service backed by the given Store.
func NewService(store Store) *Service {
	return &Service{store: store}
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

package registry

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a template does not exist.
var ErrNotFound = errors.New("registry: template not found")

// Store is the CRUD interface over stored metric templates. The default
// implementation is SQLite-backed (internal/registry/sqlite); the interface
// exists so a Firestore/GCS-backed implementation can be added later without
// touching CLI or GUI code.
type Store interface {
	Create(ctx context.Context, t MetricTemplate) error
	Get(ctx context.Context, id string) (MetricTemplate, error)
	List(ctx context.Context) ([]MetricTemplate, error)
	Update(ctx context.Context, t MetricTemplate) error
	Delete(ctx context.Context, id string) error
}

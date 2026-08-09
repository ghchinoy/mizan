package registry

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a template does not exist.
var ErrNotFound = errors.New("registry: template not found")

// ListFilter narrows the results of Store.List. A zero-value filter matches
// everything. Only the fields needed by the P1 slice are honored by the SQLite
// store today; the rest are part of the sync-aware contract (collab §3.9) and
// are accepted now so P2 needs no interface churn.
type ListFilter struct {
	Modalities []Modality
	Kinds      []MetricKind
	Namespace  string // e.g. "google-brand" (id prefix)
	Source     string // provenance filter (which pack/url it came from)
	DirtyOnly  bool   // locally-modified-since-import only
}

// Store is the local persistence interface over stored metric templates. The
// default implementation is SQLite-backed (internal/registry/sqlite); the
// interface exists so a Firestore/GCS-backed implementation can be added later
// without touching CLI, GUI, or eval code.
type Store interface {
	Get(ctx context.Context, id string) (*MetricTemplate, error)
	List(ctx context.Context, f ListFilter) ([]MetricTemplate, error)
	Put(ctx context.Context, t *MetricTemplate) error // upsert by ID
	Delete(ctx context.Context, id string) error
	// ListChangedSince is a sync-friendly primitive that both SQLite and a
	// future Firestore backend can serve cheaply.
	ListChangedSince(ctx context.Context, since time.Time) ([]MetricTemplate, error)
}

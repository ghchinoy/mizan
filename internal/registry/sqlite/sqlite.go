// Package sqlite provides the default SQLite-backed registry.Store.
//
// This is a scaffold stub: the concrete schema and CRUD implementation are
// delivered in the registry implementation phase (see docs/spikes.md Spike 5).
package sqlite

import (
	"context"
	"errors"

	"github.com/ghchinoy/mizan/internal/registry"
)

// errNotImplemented marks scaffold stubs that are wired in a later phase.
var errNotImplemented = errors.New("sqlite: not implemented")

// Store is the SQLite-backed implementation of registry.Store.
type Store struct {
	path string
}

// Open returns a Store backed by the SQLite database at path.
func Open(path string) (*Store, error) {
	return &Store{path: path}, nil
}

var _ registry.Store = (*Store)(nil)

func (s *Store) Create(context.Context, registry.MetricTemplate) error { return errNotImplemented }

func (s *Store) Get(context.Context, string) (registry.MetricTemplate, error) {
	return registry.MetricTemplate{}, errNotImplemented
}

func (s *Store) List(context.Context) ([]registry.MetricTemplate, error) {
	return nil, errNotImplemented
}

func (s *Store) Update(context.Context, registry.MetricTemplate) error { return errNotImplemented }

func (s *Store) Delete(context.Context, string) error { return errNotImplemented }

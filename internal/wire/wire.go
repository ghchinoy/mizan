// Package wire is the single composition root that assembles Mizan's concrete
// backends behind the interfaces the frontends depend on. It exists so that
// cmd/* (and the future cmd/mizan-desktop) import ONLY registry.Service,
// eval.Engine, and config — never registry/sqlite, aiplatformpb, or the
// sync/codec packages (architecture-final §3, dependency-direction acceptance
// check). Swapping SQLite for a future Firestore Store, or the eval transport,
// is a one-line change here and nowhere else.
package wire

import (
	"context"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/registry/sqlite"
)

// OpenService returns a registry.Service backed by the default SQLite store at
// cfg.RegistryDBPath, plus a close function the caller must invoke.
func OpenService(cfg *config.Config) (*registry.Service, func() error, error) {
	store, err := sqlite.Open(cfg.RegistryDBPath)
	if err != nil {
		return nil, nil, err
	}
	return registry.NewService(store), store.Close, nil
}

// NewEngine returns an eval.Engine wired to a live EvaluationClient targeting
// the configured regional endpoint, plus a close function the caller must
// invoke. It also builds the separate genai client (location=global) used by the
// custom_schema path and supplies it via eval.WithGenaiClient — keeping the two
// clients/locations distinct (spike-core). The genai client is built here, in
// the composition root, and never in cmd/*.
func NewEngine(ctx context.Context, cfg *config.Config) (*eval.Engine, func() error, error) {
	client, err := eval.NewClient(ctx, cfg.Location, cfg.APIEndpoint)
	if err != nil {
		return nil, nil, err
	}

	// The genai custom_schema path uses location=global, distinct from the
	// native regional EvaluationClient above.
	genaiClient, err := eval.NewGenaiClient(ctx, cfg.ProjectID, "global")
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	engine := eval.NewEngine(client, cfg.ProjectID, cfg.Location, eval.WithGenaiClient(genaiClient))
	return engine, client.Close, nil
}

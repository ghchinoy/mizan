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
	"errors"
	"os"

	"github.com/ghchinoy/mizan/internal/asset"
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

	// Endpoint allow-list parity for the genai path: the genai SDK honors the
	// GOOGLE_VERTEX_BASE_URL / GOOGLE_GEMINI_BASE_URL env overrides (verified in
	// v1.67.0, base_url.go getBaseURL priority 3), which — like the native
	// APIEndpoint — could redirect the ADC bearer token to a non-Google host.
	// Reject an override that is not under *.googleapis.com before building the
	// authenticated client (config.ValidateGenaiBaseURL respects the same
	// MIZAN_ALLOW_CUSTOM_ENDPOINT escape hatch). This never disables ADC/TLS.
	for _, k := range []string{"GOOGLE_VERTEX_BASE_URL", "GOOGLE_GEMINI_BASE_URL"} {
		if err := config.ValidateGenaiBaseURL(os.Getenv(k)); err != nil {
			_ = client.Close()
			return nil, nil, err
		}
	}

	// The genai custom_schema path uses location=global, distinct from the
	// native regional EvaluationClient above.
	genaiClient, err := eval.NewGenaiClient(ctx, cfg.ProjectID, eval.GenaiLocation)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	// R-GLOBAL: a DISTINCT native client targeting the GLOBAL eval host
	// (aiplatform.googleapis.com / locations/global). The engine auto-routes the
	// whole EvaluateInstances call to it when the resolved autorater is a
	// global-only judge (e.g. the gemini-3.5 family): the spike proved the eval
	// HOST — not the autorater's location path — is decisive, so the only way to
	// use such a judge is to move the whole call to the global host. Built here in
	// the composition root (cmd/* must not build clients — dependency-direction
	// rule). It is built ALWAYS (not lazily): NewEvaluationClient does not dial
	// until the first RPC, so an unused global client for a purely-regional run
	// costs only a cheap handle, keeping this wiring simple and the seam uniform.
	// When cfg.Location is already "global" the regional client above IS the
	// global host and the engine skips this one; we still build it for uniformity.
	globalClient, err := eval.NewClient(ctx, eval.GenaiLocation, cfg.APIEndpoint)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	// The config default-model (WI-F3) is the lowest-precedence input to the
	// engine's model resolution chain (below the flag and the template's own
	// model, above the built-in). An empty value falls through to the built-in.
	opts := []eval.Option{
		eval.WithGenaiClient(genaiClient),
		eval.WithGlobalClient(globalClient),
		eval.WithDefaultModel(cfg.DefaultModel),
	}
	// closeClients closes both native clients (regional + global); the regional
	// client's error takes precedence for the caller. A stager, when built below,
	// is layered on top of this.
	closeClients := func() error {
		gErr := globalClient.Close()
		cErr := client.Close()
		if cErr != nil {
			return cErr
		}
		return gErr
	}
	closeFn := closeClients

	// Build the GCS asset stager ONLY when a staging bucket is configured. When
	// none is set, construction still succeeds (text-only and custom_schema-inline
	// evals need no bucket); a multimodal native eval that needs staging then
	// fails at Run time with a clear asset.ErrNoBucket message.
	if cfg.StagingBucket != "" {
		stager, err := asset.NewGCSStager(ctx, cfg.StagingBucket)
		if err != nil && !errors.Is(err, asset.ErrNoBucket) {
			_ = closeClients()
			return nil, nil, err
		}
		if stager != nil {
			opts = append(opts, eval.WithStager(stager))
			closeFn = func() error {
				sErr := stager.Close()
				cErr := closeClients()
				if cErr != nil {
					return cErr
				}
				return sErr
			}
		}
	}

	engine := eval.NewEngine(client, cfg.ProjectID, cfg.Location, opts...)
	return engine, closeFn, nil
}

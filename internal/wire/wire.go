// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	"fmt"
	"os"
	"time"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/eval/diffusion"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/registry/sqlite"
	"github.com/ghchinoy/mizan/internal/results"
	resultsqlite "github.com/ghchinoy/mizan/internal/results/sqlite"
	"github.com/ghchinoy/mizan/internal/rubricgen"
	"github.com/ghchinoy/mizan/internal/version"
)

// closableEvalClient is the native EvaluationClient the composition root builds
// and later closes. The concrete *aiplatform.EvaluationClient satisfies it
// (EvaluateInstances + Close); a fake satisfies it in wire's unit tests.
type closableEvalClient interface {
	eval.EvaluationClient
	Close() error
}

// newNativeClient and newGenaiClient are the seams NewEngine builds its clients
// through. They are package-level vars ONLY so wire's unit tests can substitute
// fakes and assert the config->client wiring — specifically the native-vs-global
// endpoint selection (which location each native client is constructed for) and
// that the genai client targets eval.GenaiLocation — without opening real gRPC
// clients (which need ADC). Production always uses the eval constructors below;
// the wiring and its behavior are unchanged.
var (
	newNativeClient = func(ctx context.Context, location, apiEndpoint string) (closableEvalClient, error) {
		return eval.NewClient(ctx, location, apiEndpoint)
	}
	newGenaiClient = func(ctx context.Context, projectID, location string) (eval.GenaiClient, error) {
		return eval.NewGenaiClient(ctx, projectID, location)
	}
)

// OpenService returns a registry.Service backed by the default SQLite store at
// cfg.RegistryDBPath, plus a close function the caller must invoke. The
// composition root injects the pack Codec (YAML) and the config-derived
// SyncConfig here — the ONLY place that knows the concrete codec/sync wiring —
// so cmd/* depends on registry.Service alone and never imports the codec/sync
// packages (design §3.1, the seam).
func OpenService(cfg *config.Config) (*registry.Service, func() error, error) {
	store, err := sqlite.Open(cfg.RegistryDBPath)
	if err != nil {
		return nil, nil, err
	}
	svc := registry.NewService(store,
		registry.WithCodec(registry.NewYAMLCodec()),
		registry.WithSyncConfig(registry.SyncConfig{
			PackCacheDir:         cfg.PackCacheDir,
			DefaultTemplatesRepo: cfg.DefaultTemplatesRepo,
		}),
	)
	return svc, store.Close, nil
}

// OpenResultService returns a results.Service backed by the eval results store
// selected by cfg.ResultsBackend, plus a close function the caller must invoke.
// It follows OpenService's shape exactly: the composition root is the only place
// that knows the concrete backend + the retention policy / version wiring, so
// cmd/* depends on results.Service alone and never imports results/sqlite.
//
// Phase 1 implements only the "sqlite" backend (design §9). "firestore" (and any
// unknown value) returns a clear error — the Firestore leg is a visible deferred
// dependency, not a stub, so no firestore backend is imported or built here.
func OpenResultService(cfg *config.Config) (*results.Service, func() error, error) {
	switch cfg.ResultsBackend {
	case "", "sqlite":
		store, err := resultsqlite.Open(cfg.ResultsDBPath)
		if err != nil {
			return nil, nil, err
		}
		svc := results.NewService(store,
			results.WithRetentionPolicy(results.PolicyFor(cfg.ResultsRetention)),
			results.WithVersion(version.Get()),
		)
		return svc, store.Close, nil
	case "firestore":
		return nil, nil, fmt.Errorf("wire: results backend %q is not implemented in Phase 1 (sqlite only)", cfg.ResultsBackend)
	default:
		return nil, nil, fmt.Errorf("wire: unknown results backend %q (supported: sqlite)", cfg.ResultsBackend)
	}
}

// NewRubricGenerator builds the ADC-authenticated adaptive-rubric generation
// client (Stage 1) from config. It is the composition-root seam for the sync
// `:generateInstanceRubrics` REST call used by `rubric generate` (CUJ 7) and
// `eval adaptive` (CUJ 8), so cmd/* depends on the rubricgen.Client interface
// only and never builds the authed transport itself.
//
// It requires a project id (generation is a live Vertex call). The endpoint
// override is validated against the *.googleapis.com allow-list inside
// rubricgen.NewRESTClient BEFORE the ADC bearer-token client is constructed
// (token-exfil defense, mirroring NewEngine's genai base-URL guard).
func NewRubricGenerator(ctx context.Context, cfg *config.Config) (rubricgen.Client, error) {
	if cfg.ProjectID == "" {
		return nil, config.ErrMissingProjectID
	}
	return rubricgen.NewRESTClient(ctx, cfg.ProjectID, cfg.Location, cfg.APIEndpoint)
}

// NewEngine returns an eval.Engine wired to a live EvaluationClient targeting
// the configured regional endpoint, plus a close function the caller must
// invoke. It also builds the separate genai client (location=global) used by the
// custom_schema path and supplies it via eval.WithGenaiClient — keeping the two
// clients/locations distinct (spike-core). The genai client is built here, in
// the composition root, and never in cmd/*.
func NewEngine(ctx context.Context, cfg *config.Config) (*eval.Engine, func() error, error) {
	client, err := newNativeClient(ctx, cfg.Location, cfg.APIEndpoint)
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
	genaiClient, err := newGenaiClient(ctx, cfg.ProjectID, eval.GenaiLocation)
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
	globalClient, err := newNativeClient(ctx, eval.GenaiLocation, cfg.APIEndpoint)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	diffClient := diffusion.NewClient(cfg.DiffusionEndpoint, cfg.DiffusionModel, 60*time.Second)

	// The config default-model (WI-F3) is the lowest-precedence input to the
	// engine's model resolution chain (below the flag and the template's own
	// model, above the built-in). An empty value falls through to the built-in.
	opts := []eval.Option{
		eval.WithGenaiClient(genaiClient),
		eval.WithGlobalClient(globalClient),
		eval.WithDiffusionClient(diffClient),
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

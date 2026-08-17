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

// Package app is the thin, GUI-oriented binding layer over Mizan's core
// (internal/registry + internal/eval + internal/config). It is kept free of
// Wails imports so the core stays UI-framework-agnostic; the Wails entrypoint
// (cmd/mizan-desktop) wires the runtime around this struct.
//
// Scaffold stub: method bodies are implemented in the desktop phase
// (docs/spikes.md Spike 6).
package app

import (
	"context"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
)

// App binds the core packages for a desktop frontend.
type App struct {
	store  registry.Store
	engine *eval.Engine
}

// New returns an App backed by the given store and engine.
func New(store registry.Store, engine *eval.Engine) *App {
	return &App{store: store, engine: engine}
}

// ListMetricTemplates returns all stored templates.
func (a *App) ListMetricTemplates(ctx context.Context) ([]registry.MetricTemplate, error) {
	return a.store.List(ctx, registry.ListFilter{})
}

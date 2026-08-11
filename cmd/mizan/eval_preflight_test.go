package main

import (
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
)

// TestPreflightSources proves the pre-flight source-attribution logic
// (preflightSources) maps each resolved project/location value to the correct
// src= token. The golden and TestPrintPreflightLine cases only feed printPreflight
// pre-computed strings, so without this the branch logic that decides env vs
// env-file vs default vs model vs global-path is untested — a regression there
// (e.g. always reporting "model") would ship silently. Table-driven over every
// branch of the function (config-precedence POLA #1).
func TestPreflightSources(t *testing.T) {
	// A config whose project-id/location each resolved from a known source, so the
	// "target matches config" branches can assert the config source is reused.
	cfg := &config.Config{
		ProjectID: "cfg-project",
		Location:  "us-central1",
		Sources: map[string]config.Source{
			"project-id": config.SourceEnv,
			"location":   config.SourceEnvFile,
		},
	}

	cases := []struct {
		name        string
		target      eval.ResolvedTarget
		wantProjSrc string
		wantLocSrc  string
	}{
		{
			name:        "project and location match config: reuse each config source",
			target:      eval.ResolvedTarget{Project: "cfg-project", Location: "us-central1", Path: "native"},
			wantProjSrc: "env",      // project-id resolved from an exported var
			wantLocSrc:  "env-file", // location resolved from the loaded .env
		},
		{
			name:        "fully-qualified model overrides project: src=model",
			target:      eval.ResolvedTarget{Project: "other-project", Location: "us-central1", Path: "native"},
			wantProjSrc: "model",
			wantLocSrc:  "env-file",
		},
		{
			name:        "genai path forces global location: src=global-path",
			target:      eval.ResolvedTarget{Project: "cfg-project", Location: "global", Path: "genai"},
			wantProjSrc: "env",
			wantLocSrc:  "global-path",
		},
		{
			name:        "fully-qualified regional model overrides location on native path: src=model",
			target:      eval.ResolvedTarget{Project: "cfg-project", Location: "europe-west4", Path: "native", LocationFromModelResource: true},
			wantProjSrc: "env",
			wantLocSrc:  "model",
		},
		{
			// FIX-SRC: a known global-only judge auto-routes the NATIVE path to the
			// global host (location=global, path stays native). That override comes
			// from ROUTING, not a fully-qualified model resource, so it must read
			// src=global-route — NOT src=model.
			name:        "global-only model forces global on native path: src=global-route",
			target:      eval.ResolvedTarget{Project: "cfg-project", Location: "global", Path: "native"},
			wantProjSrc: "env",
			wantLocSrc:  "global-route",
		},
		{
			// Review OPTIONAL (provenance): a fully-qualified model RESOURCE
			// explicitly pinned to locations/global surfaces Location="global" on the
			// NATIVE path — the SAME string a routing-forced global would — but its
			// provenance is the resource, not routing. ResolvedTarget.LocationFromModelResource
			// carries that provenance so it reads src=model, NOT src=global-route.
			// The two global cases are deliberately kept distinct.
			name:        "global-pinned model resource on native path: src=model (not global-route)",
			target:      eval.ResolvedTarget{Project: "cfg-project", Location: "global", Path: "native", LocationFromModelResource: true},
			wantProjSrc: "env",
			wantLocSrc:  "model",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projSrc, locSrc := preflightSources(cfg, tc.target)
			if projSrc != tc.wantProjSrc {
				t.Errorf("projSrc = %q, want %q", projSrc, tc.wantProjSrc)
			}
			if locSrc != tc.wantLocSrc {
				t.Errorf("locSrc = %q, want %q", locSrc, tc.wantLocSrc)
			}
		})
	}
}

// TestPreflightSourcesDefault proves an unset project/location (built-in default,
// SourceDefault) is attributed to "default" — the row the config-precedence
// incident owner most needed to see (a value in effect with no env behind it).
func TestPreflightSourcesDefault(t *testing.T) {
	cfg := &config.Config{
		ProjectID: "",
		Location:  "us-central1", // the built-in DefaultLocation
		Sources: map[string]config.Source{
			"project-id": config.SourceDefault,
			"location":   config.SourceDefault,
		},
	}
	projSrc, locSrc := preflightSources(cfg, eval.ResolvedTarget{Project: "", Location: "us-central1", Path: "native"})
	if projSrc != "default" {
		t.Errorf("projSrc = %q, want %q", projSrc, "default")
	}
	if locSrc != "default" {
		t.Errorf("locSrc = %q, want %q", locSrc, "default")
	}
}

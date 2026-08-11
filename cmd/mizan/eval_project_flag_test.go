package main

import (
	"testing"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
)

// TestApplyProjectOverride proves the per-invocation --project flag (FEAT-PROJECT)
// sits at the TOP of the project precedence chain:
//
//	flag > exported env (MIZAN_PROJECT_ID/PROJECT_ID) > .env > default
//
// LoadConfig has already collapsed env/.env/default into cfg.ProjectID with a
// recorded Source; these cases assert the flag overrides that resolved value
// regardless of which source it came from, and re-attributes it to `flag` so the
// pre-flight echo can show src=flag. An empty flag must leave cfg untouched so the
// env/.env precedence is preserved intact.
func TestApplyProjectOverride(t *testing.T) {
	t.Run("flag beats exported env", func(t *testing.T) {
		// cfg as LoadConfig would leave it when MIZAN_PROJECT_ID was exported.
		cfg := &config.Config{
			ProjectID: "env-project",
			Sources:   map[string]config.Source{"project-id": config.SourceEnv},
		}
		applyProjectOverride(cfg, "flag-project")
		if cfg.ProjectID != "flag-project" {
			t.Errorf("ProjectID = %q, want %q (flag must beat exported env)", cfg.ProjectID, "flag-project")
		}
		if got := cfg.SourceOf("project-id"); got != config.SourceFlag {
			t.Errorf("SourceOf(project-id) = %v, want SourceFlag", got)
		}
	})

	t.Run("flag beats .env", func(t *testing.T) {
		// cfg as LoadConfig would leave it when project-id came from the .env file.
		cfg := &config.Config{
			ProjectID: "dotenv-project",
			Sources:   map[string]config.Source{"project-id": config.SourceEnvFile},
		}
		applyProjectOverride(cfg, "flag-project")
		if cfg.ProjectID != "flag-project" {
			t.Errorf("ProjectID = %q, want %q (flag must beat .env)", cfg.ProjectID, "flag-project")
		}
		if got := cfg.SourceOf("project-id"); got != config.SourceFlag {
			t.Errorf("SourceOf(project-id) = %v, want SourceFlag", got)
		}
	})

	t.Run("flag supplies project when none configured", func(t *testing.T) {
		// No env/.env project (ErrMissingProjectID path); the flag alone provides it.
		cfg := &config.Config{
			ProjectID: "",
			Sources:   map[string]config.Source{"project-id": config.SourceDefault},
		}
		applyProjectOverride(cfg, "flag-project")
		if cfg.ProjectID != "flag-project" {
			t.Errorf("ProjectID = %q, want %q", cfg.ProjectID, "flag-project")
		}
		if got := cfg.SourceOf("project-id"); got != config.SourceFlag {
			t.Errorf("SourceOf(project-id) = %v, want SourceFlag", got)
		}
	})

	t.Run("absent flag leaves env/.env precedence intact", func(t *testing.T) {
		cfg := &config.Config{
			ProjectID: "env-project",
			Sources:   map[string]config.Source{"project-id": config.SourceEnv},
		}
		applyProjectOverride(cfg, "")
		if cfg.ProjectID != "env-project" {
			t.Errorf("ProjectID = %q, want unchanged %q", cfg.ProjectID, "env-project")
		}
		if got := cfg.SourceOf("project-id"); got != config.SourceEnv {
			t.Errorf("SourceOf(project-id) = %v, want unchanged SourceEnv", got)
		}
	})

	t.Run("nil Sources map is tolerated", func(t *testing.T) {
		cfg := &config.Config{ProjectID: "env-project"} // Sources nil
		applyProjectOverride(cfg, "flag-project")
		if got := cfg.SourceOf("project-id"); got != config.SourceFlag {
			t.Errorf("SourceOf(project-id) = %v, want SourceFlag", got)
		}
	})
}

// TestPreflightSourceFlag proves that once the --project flag override is applied
// the pre-flight echo attributes the project to src=flag: the resolved target's
// project matches cfg.ProjectID, so preflightSources reuses the config source —
// which applyProjectOverride set to SourceFlag. This is the end-to-end link that
// makes deliverable (3) — "src=flag shown in preflight" — hold.
func TestPreflightSourceFlag(t *testing.T) {
	cfg := &config.Config{
		ProjectID: "env-project",
		Location:  "us-central1",
		Sources: map[string]config.Source{
			"project-id": config.SourceEnv,
			"location":   config.SourceDefault,
		},
	}
	applyProjectOverride(cfg, "flag-project")

	// The engine resolves the target from cfg.ProjectID, so the pre-flight target
	// project equals the flag value.
	target := eval.ResolvedTarget{Project: "flag-project", Location: "us-central1", Path: "native"}
	projSrc, locSrc := preflightSources(cfg, target)
	if projSrc != "flag" {
		t.Errorf("projSrc = %q, want %q", projSrc, "flag")
	}
	if locSrc != "default" {
		t.Errorf("locSrc = %q, want %q (location is unaffected by --project)", locSrc, "default")
	}
}

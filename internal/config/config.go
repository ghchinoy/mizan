// Package config loads Mizan runtime configuration from environment
// variables (with optional .env support), mirroring the mcp-common pattern
// but returning an error instead of calling log.Fatal so that both the CLI
// and the (future) Wails GUI can handle missing configuration gracefully.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

const (
	// DefaultLocation is the native eval-service region used when no location
	// is configured. The "us" multi-region 404s and must never be used
	// (spike-core); us-central1 is the verified default.
	DefaultLocation = "us-central1"

	// DefaultTemplatesRepo is the default source for `mizan registry import`
	// with no <src> (design rev 3). It is a config value so a fork or internal
	// mirror can be selected without a code change.
	DefaultTemplatesRepo = "github.com/ghchinoy/mizan-templates"
)

// Config holds the resolved runtime configuration for a Mizan process.
type Config struct {
	ProjectID            string // required for eval (env: MIZAN_PROJECT_ID or PROJECT_ID)
	Location             string // native eval-service region; default us-central1
	StagingBucket        string // gs:// prefix stripped; required only for multimodal (not this slice)
	APIEndpoint          string // optional override
	RegistryDBPath       string // default: <UserConfigDir>/mizan/registry.db
	PackCacheDir         string // default: <UserCacheDir>/mizan/packs (for import <git-url>)
	DefaultTemplatesRepo string // default: github.com/ghchinoy/mizan-templates
}

// ErrMissingProjectID is returned by LoadConfig when no project ID is set.
// It is non-fatal to callers that do not need eval (e.g. registry CRUD): the
// returned *Config is still populated so those paths can proceed.
var ErrMissingProjectID = errors.New("config: project ID not set (set MIZAN_PROJECT_ID or PROJECT_ID)")

// LoadConfig resolves configuration from the environment, loading a .env file
// from the current directory first if one is present (existing environment
// variables take precedence). It returns an error rather than exiting so
// callers can decide how to react (CLI: fatal for eval; GUI: setup screen).
func LoadConfig() (*Config, error) {
	// godotenv.Load does not override already-set variables, which is the
	// behavior we want (real env wins over .env).
	if _, err := os.Stat(".env"); err == nil {
		_ = godotenv.Load()
	}

	c := &Config{
		ProjectID:            firstNonEmpty(os.Getenv("MIZAN_PROJECT_ID"), os.Getenv("PROJECT_ID")),
		Location:             firstNonEmpty(os.Getenv("MIZAN_LOCATION"), os.Getenv("LOCATION"), DefaultLocation),
		StagingBucket:        strings.TrimPrefix(firstNonEmpty(os.Getenv("MIZAN_STAGING_BUCKET"), os.Getenv("GENMEDIA_BUCKET")), "gs://"),
		APIEndpoint:          firstNonEmpty(os.Getenv("MIZAN_API_ENDPOINT"), os.Getenv("VERTEX_API_ENDPOINT")),
		DefaultTemplatesRepo: firstNonEmpty(os.Getenv("MIZAN_TEMPLATES_REPO"), DefaultTemplatesRepo),
	}

	c.RegistryDBPath = firstNonEmpty(os.Getenv("MIZAN_REGISTRY_DB"), defaultDBPath())
	c.PackCacheDir = firstNonEmpty(os.Getenv("MIZAN_PACK_CACHE"), defaultPackCacheDir())

	if c.ProjectID == "" {
		return c, ErrMissingProjectID
	}
	return c, nil
}

func defaultDBPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "registry.db"
	}
	return filepath.Join(dir, "mizan", "registry.db")
}

func defaultPackCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join("mizan", "packs")
	}
	return filepath.Join(dir, "mizan", "packs")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

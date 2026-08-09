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
)

// DefaultLocation is used when neither LOCATION nor MIZAN_LOCATION is set.
const DefaultLocation = "us-central1"

// Config holds the resolved runtime configuration for a Mizan process.
type Config struct {
	ProjectID      string // required (env: MIZAN_PROJECT_ID or PROJECT_ID)
	Location       string // default: us-central1
	StagingBucket  string // optional, gs:// prefix stripped
	APIEndpoint    string // optional override
	RegistryDBPath string // default: <UserConfigDir>/mizan/registry.db
}

// ErrMissingProjectID is returned by LoadConfig when no project ID is set.
var ErrMissingProjectID = errors.New("config: project ID not set (set MIZAN_PROJECT_ID or PROJECT_ID)")

// LoadConfig resolves configuration from the environment. It returns an error
// rather than exiting so callers can decide how to react (CLI: fatal;
// GUI: show a setup screen).
func LoadConfig() (*Config, error) {
	c := &Config{
		ProjectID:     firstNonEmpty(os.Getenv("MIZAN_PROJECT_ID"), os.Getenv("PROJECT_ID")),
		Location:      firstNonEmpty(os.Getenv("MIZAN_LOCATION"), os.Getenv("LOCATION"), DefaultLocation),
		StagingBucket: strings.TrimPrefix(firstNonEmpty(os.Getenv("MIZAN_STAGING_BUCKET"), os.Getenv("GENMEDIA_BUCKET")), "gs://"),
		APIEndpoint:   firstNonEmpty(os.Getenv("MIZAN_API_ENDPOINT"), os.Getenv("VERTEX_API_ENDPOINT")),
	}

	c.RegistryDBPath = firstNonEmpty(os.Getenv("MIZAN_REGISTRY_DB"), defaultDBPath())

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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

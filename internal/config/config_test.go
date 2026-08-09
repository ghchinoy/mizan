package config

import (
	"errors"
	"testing"
)

// clearEnv blanks every environment variable LoadConfig consults so each test
// starts from a known-empty baseline. t.Setenv restores the prior value when the
// test ends. It also chdirs into an empty temp dir so a stray .env in the
// package directory can never influence the result (LoadConfig loads .env from
// the current working directory).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"MIZAN_PROJECT_ID", "PROJECT_ID",
		"MIZAN_LOCATION", "LOCATION",
		"MIZAN_STAGING_BUCKET", "GENMEDIA_BUCKET",
		"MIZAN_API_ENDPOINT", "VERTEX_API_ENDPOINT",
		"MIZAN_TEMPLATES_REPO",
		"MIZAN_REGISTRY_DB", "MIZAN_PACK_CACHE",
	} {
		t.Setenv(k, "")
	}
	t.Chdir(t.TempDir())
}

func TestLoadConfigMissingProjectID(t *testing.T) {
	clearEnv(t)

	c, err := LoadConfig()
	if !errors.Is(err, ErrMissingProjectID) {
		t.Fatalf("err = %v, want ErrMissingProjectID", err)
	}
	// The config is still returned (populated) so non-eval paths can proceed.
	if c == nil {
		t.Fatal("LoadConfig returned nil config alongside ErrMissingProjectID")
	}
	if c.Location != DefaultLocation {
		t.Errorf("Location = %q, want default %q even when ProjectID missing", c.Location, DefaultLocation)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.ProjectID != "proj-123" {
		t.Errorf("ProjectID = %q, want proj-123", c.ProjectID)
	}
	if c.Location != DefaultLocation {
		t.Errorf("Location = %q, want default %q", c.Location, DefaultLocation)
	}
	if c.Location != "us-central1" {
		t.Errorf("DefaultLocation drifted: %q, want us-central1 (the us multi-region 404s)", c.Location)
	}
	if c.DefaultTemplatesRepo != DefaultTemplatesRepo {
		t.Errorf("DefaultTemplatesRepo = %q, want %q", c.DefaultTemplatesRepo, DefaultTemplatesRepo)
	}
	if c.StagingBucket != "" {
		t.Errorf("StagingBucket = %q, want empty when unset", c.StagingBucket)
	}
}

func TestLoadConfigProjectIDPrecedence(t *testing.T) {
	clearEnv(t)
	// MIZAN_PROJECT_ID must win over PROJECT_ID.
	t.Setenv("MIZAN_PROJECT_ID", "mizan-proj")
	t.Setenv("PROJECT_ID", "fallback-proj")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.ProjectID != "mizan-proj" {
		t.Errorf("ProjectID = %q, want mizan-proj (MIZAN_PROJECT_ID takes precedence)", c.ProjectID)
	}
}

func TestLoadConfigStagingBucketStripsGSPrefix(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"with gs:// prefix", "gs://my-bucket", "my-bucket"},
		{"with gs:// prefix and path", "gs://my-bucket/staging", "my-bucket/staging"},
		{"bare bucket name unchanged", "my-bucket", "my-bucket"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("PROJECT_ID", "proj-123")
			t.Setenv("MIZAN_STAGING_BUCKET", tc.env)

			c, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if c.StagingBucket != tc.want {
				t.Errorf("StagingBucket = %q, want %q", c.StagingBucket, tc.want)
			}
		})
	}
}

func TestLoadConfigStagingBucketFallbackEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	// GENMEDIA_BUCKET is the fallback source for the staging bucket.
	t.Setenv("GENMEDIA_BUCKET", "gs://fallback-bucket")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.StagingBucket != "fallback-bucket" {
		t.Errorf("StagingBucket = %q, want fallback-bucket (from GENMEDIA_BUCKET, gs:// stripped)", c.StagingBucket)
	}
}

func TestLoadConfigLocationOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_LOCATION", "europe-west1")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.Location != "europe-west1" {
		t.Errorf("Location = %q, want europe-west1 (explicit override)", c.Location)
	}
}

func TestLoadConfigRegistryDBOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_REGISTRY_DB", "/tmp/custom/registry.db")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.RegistryDBPath != "/tmp/custom/registry.db" {
		t.Errorf("RegistryDBPath = %q, want the explicit override", c.RegistryDBPath)
	}
}

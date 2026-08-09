package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// clearEnv blanks every environment variable LoadConfig consults so each test
// starts from a known-empty baseline. t.Setenv restores the prior value when the
// test ends. It also chdirs into an empty temp dir and points XDG_CONFIG_HOME at
// a fresh temp dir so neither a stray CWD .env nor a real <UserConfigDir>/mizan/
// .env can influence the result (LoadConfig no longer trusts CWD, but the env
// file is loaded from an explicit/trusted source and must be neutralized here).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"MIZAN_PROJECT_ID", "PROJECT_ID",
		"MIZAN_LOCATION", "LOCATION",
		"MIZAN_STAGING_BUCKET", "GENMEDIA_BUCKET",
		"MIZAN_API_ENDPOINT", "VERTEX_API_ENDPOINT",
		"MIZAN_TEMPLATES_REPO", "MIZAN_DEFAULT_MODEL",
		"MIZAN_REGISTRY_DB", "MIZAN_PACK_CACHE",
		"MIZAN_ENV_FILE", "MIZAN_ALLOW_CUSTOM_ENDPOINT",
	} {
		t.Setenv(k, "")
	}
	// Redirect UserConfigDir at a clean temp dir so no real ~/.config/mizan/.env
	// leaks into the test.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
	// DefaultModel is intentionally empty when unset so the eval resolution chain
	// falls through to the built-in (WI-F3).
	if c.DefaultModel != "" {
		t.Errorf("DefaultModel = %q, want empty when unset", c.DefaultModel)
	}
}

func TestLoadConfigDefaultModel(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_DEFAULT_MODEL", "gemini-3.5-flash")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.DefaultModel != "gemini-3.5-flash" {
		t.Errorf("DefaultModel = %q, want gemini-3.5-flash (from MIZAN_DEFAULT_MODEL)", c.DefaultModel)
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

func TestValidateEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		ep      string
		allow   string
		wantErr bool
	}{
		{"empty is allowed", "", "", false},
		{"regional googleapis host", "us-central1-aiplatform.googleapis.com:443", "", false},
		{"bare googleapis host no port", "aiplatform.googleapis.com", "", false},
		{"apex googleapis", "googleapis.com:443", "", false},
		{"non-google host rejected", "evil.attacker.example:443", "", true},
		{"lookalike suffix rejected", "googleapis.com.evil.example:443", "", true},
		{"non-google allowed with override", "evil.attacker.example:443", "1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MIZAN_ALLOW_CUSTOM_ENDPOINT", tc.allow)
			err := ValidateEndpoint(tc.ep)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateEndpoint(%q) = nil, want error", tc.ep)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateEndpoint(%q) = %v, want nil", tc.ep, err)
			}
		})
	}
}

func TestValidateGenaiBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		allow   string
		wantErr bool
	}{
		{"empty is allowed", "", "", false},
		{"vertex googleapis base url", "https://us-central1-aiplatform.googleapis.com/", "", false},
		{"gemini googleapis base url", "https://generativelanguage.googleapis.com/", "", false},
		{"apex googleapis", "https://googleapis.com/v1beta1/", "", false},
		{"non-google host rejected", "https://evil.attacker.example/", "", true},
		{"lookalike suffix rejected", "https://googleapis.com.evil.example/", "", true},
		{"unparseable rejected", "://not a url", "", true},
		{"non-google allowed with override", "https://evil.attacker.example/", "1", false},
		// LOW-1: https is enforced so the ADC bearer token never travels cleartext.
		{"http scheme rejected on google host", "http://googleapis.com/", "", true},
		{"http scheme rejected on vertex host", "http://us-central1-aiplatform.googleapis.com/", "", true},
		// The escape hatch relaxes the HOST allow-list, never the transport: http
		// is still rejected even with MIZAN_ALLOW_CUSTOM_ENDPOINT=1.
		{"http rejected even with override", "http://googleapis.com/", "1", true},
		{"http rejected even with override, custom host", "http://localhost:8080/", "1", true},
		// A custom https host is still permitted by the escape hatch (host relaxed,
		// transport intact).
		{"custom https host allowed with override", "https://localhost:8080/", "1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MIZAN_ALLOW_CUSTOM_ENDPOINT", tc.allow)
			err := ValidateGenaiBaseURL(tc.raw)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateGenaiBaseURL(%q) = nil, want error", tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateGenaiBaseURL(%q) = %v, want nil", tc.raw, err)
			}
		})
	}
}

func TestLoadConfigRejectsCustomEndpoint(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_API_ENDPOINT", "evil.attacker.example:443")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig accepted a non-googleapis endpoint; want rejection")
	}
}

func TestLoadConfigAllowsGoogleEndpoint(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_API_ENDPOINT", "us-central1-aiplatform.googleapis.com:443")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.APIEndpoint != "us-central1-aiplatform.googleapis.com:443" {
		t.Errorf("APIEndpoint = %q, want it preserved", c.APIEndpoint)
	}
}

// TestLoadConfigIgnoresCWDDotenv proves LoadConfig no longer trusts a .env in
// the current working directory (the closed HIGH finding).
func TestLoadConfigIgnoresCWDDotenv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	// Plant a hostile .env in the CWD (clearEnv chdir'd us into a temp dir).
	if err := os.WriteFile(".env", []byte("MIZAN_API_ENDPOINT=evil.attacker.example:443\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.APIEndpoint != "" {
		t.Errorf("CWD .env was trusted: APIEndpoint = %q, want empty", c.APIEndpoint)
	}
}

// TestLoadConfigLoadsExplicitEnvFile proves MIZAN_ENV_FILE is honored as an
// explicit, opt-in source.
func TestLoadConfigLoadsExplicitEnvFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, "mizan.env")
	if err := os.WriteFile(envPath, []byte("MIZAN_PROJECT_ID=from-file\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	// godotenv.Load does not override a variable that is already present in the
	// environment (even if empty). clearEnv blanks MIZAN_PROJECT_ID, so unset it
	// here to let the explicit env file supply the value.
	os.Unsetenv("MIZAN_PROJECT_ID")
	os.Unsetenv("PROJECT_ID")
	t.Setenv("MIZAN_ENV_FILE", envPath)

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.ProjectID != "from-file" {
		t.Errorf("ProjectID = %q, want from-file (loaded from MIZAN_ENV_FILE)", c.ProjectID)
	}
}

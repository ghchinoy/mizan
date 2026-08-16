package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		"MIZAN_RESULTS_BACKEND", "MIZAN_RESULTS_DB", "MIZAN_RESULTS_RETENTION",
		"MIZAN_ENV_FILE", "MIZAN_ALLOW_CUSTOM_ENDPOINT",
		"MIZAN_AUTHOR_NAME", "MIZAN_DEFAULT_LICENSE",
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

// TestLoadConfigDefaultModelFromEnvFile proves the default-model key is also
// loaded from a trusted env file (WI-F3 asks for both env AND config file), not
// only from a live environment variable.
func TestLoadConfigDefaultModelFromEnvFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, "mizan.env")
	if err := os.WriteFile(envPath, []byte("MIZAN_PROJECT_ID=proj-123\nMIZAN_DEFAULT_MODEL=gemini-3.5-flash\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	// godotenv.Load does not override an already-set variable (even empty), so
	// unset the two keys the env file supplies.
	os.Unsetenv("MIZAN_PROJECT_ID")
	os.Unsetenv("PROJECT_ID")
	os.Unsetenv("MIZAN_DEFAULT_MODEL")
	t.Setenv("MIZAN_ENV_FILE", envPath)

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.DefaultModel != "gemini-3.5-flash" {
		t.Errorf("DefaultModel = %q, want gemini-3.5-flash (loaded from env file)", c.DefaultModel)
	}
}

// TestAuthorLicenseFromEnvFile proves the two authoring metadata keys
// (author-name, default-license) are also loaded from a trusted env file (not
// only from live environment variables), that a real exported env var shadows
// the env-file value, and that an unset key reports SourceDefault. It mirrors
// TestLoadConfigDefaultModelFromEnvFile for the metadata keys specifically.
func TestAuthorLicenseFromEnvFile(t *testing.T) {
	t.Run("loaded from env file", func(t *testing.T) {
		clearEnv(t)
		dir := t.TempDir()
		envPath := filepath.Join(dir, "mizan.env")
		if err := os.WriteFile(envPath, []byte(
			"MIZAN_PROJECT_ID=proj-123\nMIZAN_AUTHOR_NAME=File Author\nMIZAN_DEFAULT_LICENSE=MIT\n"), 0o600); err != nil {
			t.Fatalf("write env file: %v", err)
		}
		// godotenv.Load does not override already-set variables (even empty), so
		// unset the keys the env file supplies.
		os.Unsetenv("MIZAN_PROJECT_ID")
		os.Unsetenv("PROJECT_ID")
		os.Unsetenv("MIZAN_AUTHOR_NAME")
		os.Unsetenv("MIZAN_DEFAULT_LICENSE")
		t.Setenv("MIZAN_ENV_FILE", envPath)

		c, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if c.AuthorName != "File Author" {
			t.Errorf("AuthorName = %q, want File Author (from env file)", c.AuthorName)
		}
		if c.DefaultLicense != "MIT" {
			t.Errorf("DefaultLicense = %q, want MIT (from env file)", c.DefaultLicense)
		}
		if got := c.SourceOf("author-name"); got != SourceEnvFile {
			t.Errorf("SourceOf(author-name) = %v, want env-file", got)
		}
		if got := c.SourceOf("default-license"); got != SourceEnvFile {
			t.Errorf("SourceOf(default-license) = %v, want env-file", got)
		}
	})

	t.Run("real env shadows env file", func(t *testing.T) {
		clearEnv(t)
		dir := t.TempDir()
		envPath := filepath.Join(dir, "mizan.env")
		if err := os.WriteFile(envPath, []byte(
			"MIZAN_AUTHOR_NAME=File Author\nMIZAN_DEFAULT_LICENSE=MIT\n"), 0o600); err != nil {
			t.Fatalf("write env file: %v", err)
		}
		t.Setenv("MIZAN_PROJECT_ID", "proj-123")
		t.Setenv("MIZAN_ENV_FILE", envPath)
		// Exported env vars must win over the env file (godotenv does not override).
		t.Setenv("MIZAN_AUTHOR_NAME", "Env Author")
		t.Setenv("MIZAN_DEFAULT_LICENSE", "Apache-2.0")

		c, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if c.AuthorName != "Env Author" {
			t.Errorf("AuthorName = %q, want Env Author (real env shadows env file)", c.AuthorName)
		}
		if c.DefaultLicense != "Apache-2.0" {
			t.Errorf("DefaultLicense = %q, want Apache-2.0 (real env shadows env file)", c.DefaultLicense)
		}
		if got := c.SourceOf("author-name"); got != SourceEnv {
			t.Errorf("SourceOf(author-name) = %v, want env", got)
		}
		if got := c.SourceOf("default-license"); got != SourceEnv {
			t.Errorf("SourceOf(default-license) = %v, want env", got)
		}
	})

	t.Run("unset reports SourceDefault", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("MIZAN_PROJECT_ID", "proj-123") // avoid ErrMissingProjectID

		c, err := LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		if c.AuthorName != "" {
			t.Errorf("AuthorName = %q, want empty when unset", c.AuthorName)
		}
		if c.DefaultLicense != "" {
			t.Errorf("DefaultLicense = %q, want empty when unset", c.DefaultLicense)
		}
		if got := c.SourceOf("author-name"); got != SourceDefault {
			t.Errorf("SourceOf(author-name) = %v, want default", got)
		}
		if got := c.SourceOf("default-license"); got != SourceDefault {
			t.Errorf("SourceOf(default-license) = %v, want default", got)
		}
	})
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

func TestLoadConfigResultsDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.ResultsBackend != "sqlite" {
		t.Errorf("ResultsBackend = %q, want sqlite", c.ResultsBackend)
	}
	if c.ResultsRetention != "hybrid" {
		t.Errorf("ResultsRetention = %q, want hybrid", c.ResultsRetention)
	}
	if !strings.HasSuffix(filepath.ToSlash(c.ResultsDBPath), "mizan/results.db") {
		t.Errorf("ResultsDBPath = %q, want a path ending in mizan/results.db", c.ResultsDBPath)
	}
	// The results DB must be a SEPARATE file from the registry DB.
	if c.ResultsDBPath == c.RegistryDBPath {
		t.Errorf("ResultsDBPath == RegistryDBPath (%q); they must be separate files", c.ResultsDBPath)
	}
}

func TestLoadConfigResultsOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_RESULTS_BACKEND", "firestore")
	t.Setenv("MIZAN_RESULTS_DB", "/tmp/custom/results.db")
	t.Setenv("MIZAN_RESULTS_RETENTION", "reference")

	c, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if c.ResultsBackend != "firestore" {
		t.Errorf("ResultsBackend = %q, want firestore", c.ResultsBackend)
	}
	if c.ResultsDBPath != "/tmp/custom/results.db" {
		t.Errorf("ResultsDBPath = %q, want the explicit override", c.ResultsDBPath)
	}
	if c.ResultsRetention != "reference" {
		t.Errorf("ResultsRetention = %q, want reference", c.ResultsRetention)
	}
}

func TestResultsFieldsPresent(t *testing.T) {
	want := map[string]string{
		"results-backend":   "MIZAN_RESULTS_BACKEND",
		"results-db":        "MIZAN_RESULTS_DB",
		"results-retention": "MIZAN_RESULTS_RETENTION",
	}
	got := make(map[string]string)
	for _, f := range Fields() {
		if _, ok := want[f.Key]; ok {
			got[f.Key] = f.EnvVars[0]
		}
	}
	for k, env := range want {
		if got[k] != env {
			t.Errorf("Fields() key %q env = %q, want %q", k, got[k], env)
		}
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
		// CRIT-2 parser-differential vectors: the real authority is evil.com but a
		// naive HasSuffix(".googleapis.com") on the whole string would have PASSED
		// these. The bare-host check rejects any path/userinfo/query/fragment so
		// validation (here) and use (rubricgen.restBaseURL / eval.endpointFor) agree.
		{"path smuggles google suffix", "evil.com/foo.googleapis.com", "", true},
		{"path with dot-google suffix", "evil.com/.googleapis.com", "", true},
		{"fragment smuggles google suffix", "evil.com#.googleapis.com", "", true},
		{"query smuggles google suffix", "evil.com?.googleapis.com", "", true},
		{"userinfo before google host", "@evil.com/foo.googleapis.com", "", true},
		{"scheme prefix rejected", "https://evil.com/.googleapis.com", "", true},
		{"backslash path rejected", "evil.com\\foo.googleapis.com", "", true},
		// The escape hatch still relaxes the host allow-list for these crafted values
		// (operator explicitly opted in), matching endpoint policy elsewhere.
		{"differential allowed with override", "evil.com/foo.googleapis.com", "1", false},
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

func TestValidateLocation(t *testing.T) {
	cases := []struct {
		name    string
		loc     string
		wantErr bool
	}{
		// Positive: the region label and the sentinels callers normalize to.
		{"empty is allowed (normalized to global)", "", false},
		{"global sentinel", "global", false},
		{"typical region us-central1", "us-central1", false},
		{"region europe-west4", "europe-west4", false},
		{"region asia-northeast1", "asia-northeast1", false},
		// CRIT-1 host-injection vectors: a location that carries authority-
		// structural bytes must be rejected BEFORE it is concatenated into
		// {loc}-aiplatform.googleapis.com and the ADC bearer token is attached.
		{"slash moves authority", "evil.com/", true},
		{"fragment moves authority", "evil.com#", true},
		{"userinfo moves authority", "@evil.com/", true},
		{"query moves authority", "evil.com?", true},
		{"colon port injection", "evil.com:443", true},
		{"embedded dot host", "evil.com", true},
		{"uppercase rejected", "US-CENTRAL1", true},
		{"leading hyphen rejected", "-central1", true},
		{"trailing hyphen rejected", "us-central1-", true},
		{"leading digit rejected", "1region", true},
		{"whitespace rejected", "us central1", true},
		{"newline rejected", "us-central1\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLocation(tc.loc)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateLocation(%q) = nil, want error", tc.loc)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateLocation(%q) = %v, want nil", tc.loc, err)
			}
		})
	}
}

func TestValidateProjectID(t *testing.T) {
	cases := []struct {
		name    string
		project string
		wantErr bool
	}{
		{"empty is allowed (no override)", "", false},
		{"typical project id", "my-project-123", false},
		{"minimum length six", "abcde1", false},
		{"thirty-one chars rejected", "a234567890123456789012345678901", true}, // 31 chars
		{"exactly thirty allowed", "a12345678901234567890123456789", false},    // 30 chars
		{"digits and hyphens", "proj-2026-eval", false},
		{"too short five chars", "abcde", true},
		{"leading digit rejected", "1project", true},
		{"leading hyphen rejected", "-project", true},
		{"trailing hyphen rejected", "project-", true},
		{"uppercase rejected", "MyProject", true},
		{"underscore rejected", "my_project", true},
		{"space rejected", "my project", true},
		{"newline rejected", "my-project\n", true},
		{"slash rejected", "my/project", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProjectID(tc.project)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateProjectID(%q) = nil, want error", tc.project)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateProjectID(%q) = %v, want nil", tc.project, err)
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

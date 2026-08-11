// Package config loads Mizan runtime configuration from environment
// variables (with optional .env support), mirroring the mcp-common pattern
// but returning an error instead of calling log.Fatal so that both the CLI
// and the (future) Wails GUI can handle missing configuration gracefully.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	DefaultModel         string // default autorater model (env: MIZAN_DEFAULT_MODEL); "" -> built-in (WI-F3)

	// Sources records where each field's resolved value came from (real env /
	// env file / built-in default), keyed by `config set` key. It powers the
	// per-value source hint in `config show` and the eval pre-flight echo
	// (config-precedence POLA #1). Populated by LoadConfig; omitted from JSON
	// when empty.
	Sources map[string]Source `json:",omitempty"`
}

// SourceOf reports where the value for a `config set` key resolved from. An
// unknown or unpopulated key reports SourceDefault.
func (c *Config) SourceOf(key string) Source { return c.Sources[key] }

// ErrMissingProjectID is returned by LoadConfig when no project ID is set.
// It is non-fatal to callers that do not need eval (e.g. registry CRUD): the
// returned *Config is still populated so those paths can proceed.
var ErrMissingProjectID = errors.New("config: project ID not set (set MIZAN_PROJECT_ID or PROJECT_ID)")

// LoadConfig resolves configuration from the environment. It first loads an
// env file only from an EXPLICIT, trusted source (never silently from the
// current working directory) — see loadEnvFile — then resolves each value from
// the environment (real env always wins over the env file). It returns an error
// rather than exiting so callers can decide how to react (CLI: fatal for eval;
// GUI: setup screen).
func LoadConfig() (*Config, error) {
	// Snapshot the REAL process environment BEFORE the env file is loaded so we
	// can attribute each resolved value's source: a name present here supplied a
	// real (exported) value; a name only in the env file came from the file;
	// neither means the built-in default. godotenv.Load never overrides an
	// already-present variable, so this snapshot is the ground truth for "real
	// env" (config-precedence POLA #1).
	realEnv := realEnvSnapshot()
	fileVars := loadEnvFile()

	// Warn (stderr) on any unrecognized MIZAN_* variable so a typo like
	// MIZAN_PROJECT (instead of MIZAN_PROJECT_ID) is no longer silently ignored
	// (config-precedence POLA #2). Recognized names derive from the same single
	// source of truth as `config set`.
	emitUnknownEnvWarnings(os.Stderr)

	c := &Config{
		ProjectID:            firstNonEmpty(os.Getenv("MIZAN_PROJECT_ID"), os.Getenv("PROJECT_ID")),
		Location:             firstNonEmpty(os.Getenv("MIZAN_LOCATION"), os.Getenv("LOCATION"), DefaultLocation),
		StagingBucket:        strings.TrimPrefix(firstNonEmpty(os.Getenv("MIZAN_STAGING_BUCKET"), os.Getenv("GENMEDIA_BUCKET")), "gs://"),
		APIEndpoint:          firstNonEmpty(os.Getenv("MIZAN_API_ENDPOINT"), os.Getenv("VERTEX_API_ENDPOINT")),
		DefaultTemplatesRepo: firstNonEmpty(os.Getenv("MIZAN_TEMPLATES_REPO"), DefaultTemplatesRepo),
		// DefaultModel is intentionally left empty when unset: the eval engine's
		// precedence chain (flag > template > config default > built-in) treats an
		// empty value as "fall through to the built-in", so no default is baked in
		// here (WI-F3).
		DefaultModel: os.Getenv("MIZAN_DEFAULT_MODEL"),
	}

	c.RegistryDBPath = firstNonEmpty(os.Getenv("MIZAN_REGISTRY_DB"), defaultDBPath())
	c.PackCacheDir = firstNonEmpty(os.Getenv("MIZAN_PACK_CACHE"), defaultPackCacheDir())

	c.Sources = resolveSources(realEnv, fileVars)

	// Reject a custom API endpoint that could redirect ADC bearer tokens to a
	// non-Google host (token-exfil defense). Never weakens auth/TLS.
	if err := ValidateEndpoint(c.APIEndpoint); err != nil {
		return c, err
	}

	if c.ProjectID == "" {
		return c, ErrMissingProjectID
	}
	return c, nil
}

// loadEnvFile loads a dotenv file only from an explicit, trusted source, never
// silently from the current working directory (which would let a stray/hostile
// .env in a cloned repo or shared work dir redirect the Vertex endpoint and
// exfiltrate ADC OAuth tokens). Resolution order:
//
//  1. MIZAN_ENV_FILE, if set (explicit path; may be a CWD ".env" as an opt-in).
//  2. <os.UserConfigDir>/mizan/.env, if it exists.
//
// godotenv.Load does not override already-set variables, so real environment
// values always win. The env file actually loaded is announced on stderr so any
// redirect of configuration is visible to the operator. It returns the variables
// the loaded file defined (name -> value) so the caller can attribute each
// resolved value's source (env-file vs real env vs default); the map is nil when
// no file is loaded.
func loadEnvFile() map[string]string {
	if p := os.Getenv("MIZAN_ENV_FILE"); p != "" {
		// The path is an explicit, operator-supplied opt-in (see doc comment
		// above); statting it is intended, not attacker-controlled traversal.
		if _, err := os.Stat(p); err == nil { //nolint:gosec // G304: MIZAN_ENV_FILE is a trusted, explicit operator path
			fileVars, _ := godotenv.Read(p) // best-effort: source attribution only
			if err := godotenv.Load(p); err == nil {
				fmt.Fprintf(os.Stderr, "mizan: loaded env file %s (MIZAN_ENV_FILE)\n", p)
				return fileVars
			}
		}
		return nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	p := filepath.Join(dir, "mizan", ".env")
	if _, err := os.Stat(p); err == nil {
		fileVars, _ := godotenv.Read(p) // best-effort: source attribution only
		if err := godotenv.Load(p); err == nil {
			fmt.Fprintf(os.Stderr, "mizan: loaded env file %s\n", p)
			return fileVars
		}
	}
	return nil
}

// realEnvSnapshot captures the process environment as a name -> value map. Taken
// BEFORE loadEnvFile runs, it is the ground truth for which values came from a
// real (exported) variable rather than the env file.
func realEnvSnapshot() map[string]string {
	env := os.Environ()
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// resolveSources attributes every field's resolved value to its origin (real env
// / env file / built-in default), reusing the SAME Field precedence order used to
// read the value so the source can never disagree with what was loaded. realEnv
// is the process environment captured before the env file loaded; fileVars is
// what the env file defined. It is the single source-resolution helper shared by
// `config show` and the eval pre-flight echo.
func resolveSources(realEnv, fileVars map[string]string) map[string]Source {
	src := make(map[string]Source, len(Fields()))
	for _, f := range Fields() {
		src[f.Key] = resolveSource(f.EnvVars, realEnv, fileVars)
	}
	return src
}

// resolveSource walks a field's env vars in precedence order and returns the
// origin of the winning value, mirroring godotenv.Load's no-override behavior
// exactly so the reported source can never disagree with the value that actually
// won:
//
//   - A real (exported) variable that is PRESENT wins over the env file for the
//     same name — even when its value is empty. godotenv.Load treats any name
//     present in os.Environ() (including an exported-but-empty NAME=) as
//     already-set and does NOT load the file's value for it. So an exported-empty
//     var that shadows a non-empty .env entry is attributed to `env` (the source
//     that actually wins), NOT `env-file`: the file value never took effect. This
//     closes the narrow source-attribution corner where the label wrongly claimed
//     `env-file` for a value the exported (empty) variable had shadowed.
//   - An exported-empty var with NO env-file entry of the same name supplies no
//     value and shadows nothing, so scanning continues down the precedence list
//     (and ultimately falls through to SourceDefault); this keeps the built-in
//     default correctly attributed to `default`.
//   - A name absent from the real env but supplied non-empty by the env file is
//     attributed to `env-file`.
//
// Returns SourceDefault when nothing supplies a value.
func resolveSource(envVars []string, realEnv, fileVars map[string]string) Source {
	for _, name := range envVars {
		if v, ok := realEnv[name]; ok {
			if v != "" {
				return SourceEnv
			}
			// Exported-but-empty: godotenv.Load leaves it untouched. If it shadows
			// a non-empty env-file entry of the same name, the exported var is what
			// wins (suppressing the file value), so label it `env`.
			if fileVars[name] != "" {
				return SourceEnv
			}
			// No file value to shadow: this name contributes nothing; keep scanning.
			continue
		}
		if fileVars[name] != "" {
			return SourceEnvFile
		}
	}
	return SourceDefault
}

// ValidateEndpoint rejects a non-empty APIEndpoint whose host is not under
// *.googleapis.com unless MIZAN_ALLOW_CUSTOM_ENDPOINT=1 is set. This prevents a
// stray/hostile config from redirecting the authenticated Vertex client (which
// attaches an ADC OAuth bearer token to every RPC) to an attacker-controlled
// host. It never disables ADC or TLS.
func ValidateEndpoint(ep string) error {
	if ep == "" {
		return nil
	}
	if os.Getenv("MIZAN_ALLOW_CUSTOM_ENDPOINT") == "1" {
		return nil
	}
	host := ep
	if h, _, err := net.SplitHostPort(ep); err == nil {
		host = h
	}
	if endpointHostAllowed(host) {
		return nil
	}
	return fmt.Errorf("config: refusing custom API endpoint %q: host is not *.googleapis.com (set MIZAN_ALLOW_CUSTOM_ENDPOINT=1 to override)", ep)
}

// projectIDPattern is the canonical GCP project-ID format: 6–30 characters, a
// lowercase letter first, then lowercase letters / digits / hyphens, and no
// trailing hyphen. It is deliberately the documented GCP rule so a malformed
// --project value is caught LOCALLY with a crisp error instead of only failing
// server-side with an opaque InvalidArgument (defense-in-depth + better UX,
// mirroring the local ValidateModel guard on --model).
var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// ValidateProjectID rejects a non-empty project id that does not match the
// canonical GCP format (projectIDPattern). An empty id is accepted: it means "no
// --project override, keep the env/.env/default the loader resolved". This is the
// local counterpart to ValidateModel — it turns a typo or hostile value into a
// crisp local error before it is echoed in the pre-flight line or sent to Vertex.
func ValidateProjectID(project string) error {
	if project == "" {
		return nil
	}
	if !projectIDPattern.MatchString(project) {
		return fmt.Errorf("config: invalid project id %q: expected 6-30 chars, a lowercase letter first, then lowercase letters, digits or '-', no trailing '-' (e.g. \"my-project-123\")", project)
	}
	return nil
}

// ValidateGenaiBaseURL applies the same *.googleapis.com allow-list to a genai
// SDK base-URL override (GOOGLE_VERTEX_BASE_URL / GOOGLE_GEMINI_BASE_URL, which
// the SDK honors — verified in genai v1.67.0). Unlike ValidateEndpoint the value
// is a full URL (e.g. "https://host/"), so the host is parsed out. This gives
// the genai (custom_schema) path endpoint parity with the native path, closing
// the ADC-token-redirection gap flagged in the WI-5 audit.
//
// The scheme MUST be https: the genai client attaches an ADC OAuth bearer token
// to every request, and a cleartext (http) transport would expose it to an
// on-path/downgrade attacker even when the host is on-Google (WI-4 audit LOW-1).
// The https requirement is enforced UNCONDITIONALLY — the
// MIZAN_ALLOW_CUSTOM_ENDPOINT=1 escape hatch relaxes only the host allow-list
// (e.g. a local emulator on a *.googleapis.com-lookalike host), never the
// transport. ADC/TLS are never weakened.
func ValidateGenaiBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("config: refusing genai base URL %q: not a parseable URL (set MIZAN_ALLOW_CUSTOM_ENDPOINT=1 to override)", raw)
	}
	// Enforce https before (and regardless of) the escape hatch so the ADC
	// bearer token is never sent over cleartext, even under a custom endpoint.
	if u.Scheme != "https" {
		return fmt.Errorf("config: refusing genai base URL %q: scheme must be https, not %q (transport is never downgraded, even with MIZAN_ALLOW_CUSTOM_ENDPOINT=1)", raw, u.Scheme)
	}
	if os.Getenv("MIZAN_ALLOW_CUSTOM_ENDPOINT") == "1" {
		return nil
	}
	if endpointHostAllowed(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("config: refusing genai base URL %q: host %q is not *.googleapis.com (set MIZAN_ALLOW_CUSTOM_ENDPOINT=1 to override)", raw, u.Hostname())
}

// endpointHostAllowed reports whether host is permitted under the Google-only
// endpoint policy (the canonical apex or any *.googleapis.com subdomain).
func endpointHostAllowed(host string) bool {
	return host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com")
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

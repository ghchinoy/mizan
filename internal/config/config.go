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
}

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
	loadEnvFile()

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
// redirect of configuration is visible to the operator.
func loadEnvFile() {
	if p := os.Getenv("MIZAN_ENV_FILE"); p != "" {
		// The path is an explicit, operator-supplied opt-in (see doc comment
		// above); statting it is intended, not attacker-controlled traversal.
		if _, err := os.Stat(p); err == nil { //nolint:gosec // G304: MIZAN_ENV_FILE is a trusted, explicit operator path
			if err := godotenv.Load(p); err == nil {
				fmt.Fprintf(os.Stderr, "mizan: loaded env file %s (MIZAN_ENV_FILE)\n", p)
			}
		}
		return
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	p := filepath.Join(dir, "mizan", ".env")
	if _, err := os.Stat(p); err == nil {
		if err := godotenv.Load(p); err == nil {
			fmt.Fprintf(os.Stderr, "mizan: loaded env file %s\n", p)
		}
	}
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

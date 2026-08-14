package config

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Source identifies where a resolved configuration value came from. It backs the
// per-value source hint shown by `config show` and the eval pre-flight echo so an
// operator can see, at a glance, whether a value is a built-in default, a
// persisted `.env` entry, or an exported shell variable — the visibility gap the
// config-precedence findings flagged (POLA #1). Real (exported) environment
// variables win over the loaded `.env`, which wins over the built-in default.
type Source int

const (
	// SourceDefault means no environment variable supplied a value; the built-in
	// default (or "unset") is in effect.
	SourceDefault Source = iota
	// SourceEnvFile means the value came from the loaded env file
	// (<UserConfigDir>/mizan/.env or MIZAN_ENV_FILE).
	SourceEnvFile
	// SourceEnv means the value came from a real (exported) process environment
	// variable, which always wins over the env file.
	SourceEnv
	// SourceFlag means the value was supplied by a per-invocation command-line
	// flag (e.g. eval's --project), the HIGHEST-precedence source: it overrides an
	// exported env var, the env file, and the built-in default. LoadConfig never
	// sets this — it is applied by the CLI when a run-scoped flag overrides a
	// resolved value, so the pre-flight echo can attribute that value to the flag.
	SourceFlag
)

// String renders the source as the stable token used in CLI output and JSON.
func (s Source) String() string {
	switch s {
	case SourceFlag:
		return "flag"
	case SourceEnv:
		return "env"
	case SourceEnvFile:
		return "env-file"
	default:
		return "default"
	}
}

// MarshalText makes `config show --output json` serialize a source as its stable
// string token ("flag"/"env"/"env-file"/"default") rather than an opaque integer.
func (s Source) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// Field describes one user-configurable value. It is the SINGLE SOURCE OF TRUTH
// shared by `config set` (the accepted key and the env var it writes),
// `config show` (the label, value, and source of each row), the eval pre-flight
// source attribution, and the unknown-env-var warning. Because keys, env vars,
// and display all derive from this one list, the `config show` labels can never
// drift from the keys `config set` accepts again — the exact defect FIX-CONFIG
// closes.
type Field struct {
	// Key is the exact `config set <key>` token, e.g. "project-id". `config show`
	// labels each row with it so its output round-trips into `config set`.
	Key string
	// EnvVars are the environment variables read for this field, in precedence
	// order (highest first). EnvVars[0] is the canonical name `config set`
	// writes; any later entries are read-only fallbacks (e.g. PROJECT_ID).
	EnvVars []string
	// Value extracts the resolved value from a loaded *Config.
	Value func(*Config) string
}

// Fields returns the ordered set of user-configurable fields. The slice order is
// the display order for `config show`. This is the one place keys, env vars, and
// value accessors are declared; every other config-UX behavior derives from it.
func Fields() []Field {
	return []Field{
		{Key: "project-id", EnvVars: []string{"MIZAN_PROJECT_ID", "PROJECT_ID"}, Value: func(c *Config) string { return c.ProjectID }},
		{Key: "location", EnvVars: []string{"MIZAN_LOCATION", "LOCATION"}, Value: func(c *Config) string { return c.Location }},
		{Key: "staging-bucket", EnvVars: []string{"MIZAN_STAGING_BUCKET", "GENMEDIA_BUCKET"}, Value: func(c *Config) string { return c.StagingBucket }},
		{Key: "api-endpoint", EnvVars: []string{"MIZAN_API_ENDPOINT", "VERTEX_API_ENDPOINT"}, Value: func(c *Config) string { return c.APIEndpoint }},
		{Key: "registry-db", EnvVars: []string{"MIZAN_REGISTRY_DB"}, Value: func(c *Config) string { return c.RegistryDBPath }},
		{Key: "pack-cache", EnvVars: []string{"MIZAN_PACK_CACHE"}, Value: func(c *Config) string { return c.PackCacheDir }},
		{Key: "templates-repo", EnvVars: []string{"MIZAN_TEMPLATES_REPO"}, Value: func(c *Config) string { return c.DefaultTemplatesRepo }},
		{Key: "default-model", EnvVars: []string{"MIZAN_DEFAULT_MODEL"}, Value: func(c *Config) string { return c.DefaultModel }},
		{Key: "author-name", EnvVars: []string{"MIZAN_AUTHOR_NAME"}, Value: func(c *Config) string { return c.AuthorName }},
		{Key: "default-license", EnvVars: []string{"MIZAN_DEFAULT_LICENSE"}, Value: func(c *Config) string { return c.DefaultLicense }},
	}
}

// operationalEnvVars are recognized MIZAN_*-prefixed variables that steer the
// loader but are not `config set` keys, so the unknown-var warning must not flag
// them.
var operationalEnvVars = []string{"MIZAN_ENV_FILE", "MIZAN_ALLOW_CUSTOM_ENDPOINT"}

// recognizedEnvVars returns the set of every environment variable the loader
// understands (all Field env vars plus the operational vars), derived from the
// same single source of truth as `config set`.
func recognizedEnvVars() map[string]bool {
	set := make(map[string]bool)
	for _, f := range Fields() {
		for _, v := range f.EnvVars {
			set[v] = true
		}
	}
	for _, v := range operationalEnvVars {
		set[v] = true
	}
	return set
}

// mizanEnvCandidates returns the recognized MIZAN_*-prefixed variable names,
// sorted — the candidate pool for did-you-mean suggestions.
func mizanEnvCandidates() []string {
	var out []string
	for v := range recognizedEnvVars() {
		if strings.HasPrefix(v, "MIZAN_") {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

// UnknownEnvVarWarnings scans the process environment for MIZAN_*-prefixed
// variables the loader does not recognize and returns one warning line per
// offender, with a did-you-mean suggestion when a recognized variable is a near
// match. This catches the exact typo class from the config-precedence incident
// (an exported MIZAN_PROJECT was silently ignored because the read key is
// MIZAN_PROJECT_ID — POLA #2). It never flags a recognized variable. Results are
// sorted for deterministic output.
func UnknownEnvVarWarnings() []string {
	recognized := recognizedEnvVars()
	candidates := mizanEnvCandidates()

	var unknown []string
	for _, kv := range os.Environ() {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if !strings.HasPrefix(name, "MIZAN_") || recognized[name] {
			continue
		}
		unknown = append(unknown, name)
	}
	sort.Strings(unknown)

	warns := make([]string, 0, len(unknown))
	for _, name := range unknown {
		if s := nearestEnvVar(name, candidates); s != "" {
			warns = append(warns, fmt.Sprintf("mizan: warning: ignoring unknown env var %s (did you mean %s?)", name, s))
		} else {
			warns = append(warns, fmt.Sprintf("mizan: warning: ignoring unknown env var %s", name))
		}
	}
	return warns
}

// emitUnknownEnvWarnings writes UnknownEnvVarWarnings to w (stderr in
// production), one per line.
func emitUnknownEnvWarnings(w io.Writer) {
	for _, line := range UnknownEnvVarWarnings() {
		fmt.Fprintln(w, line)
	}
}

// nearestEnvVar returns the candidate closest (by Levenshtein distance) to name
// when within a small edit budget, else "" (no plausible suggestion). The budget
// scales with the name length so a short typo suggests a fix but an unrelated
// name does not (e.g. MIZAN_PROJECT -> MIZAN_PROJECT_ID, distance 3, is offered;
// a wholly unrelated MIZAN_XYZZY is not).
func nearestEnvVar(name string, candidates []string) string {
	best := ""
	bestDist := 1 << 30
	for _, c := range candidates {
		if d := levenshtein(name, c); d < bestDist {
			bestDist, best = d, c
		}
	}
	budget := len(name) / 3
	if budget < 3 {
		budget = 3
	}
	if best != "" && bestDist <= budget {
		return best
	}
	return ""
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

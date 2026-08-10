package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSourceAttribution proves LoadConfig attributes each resolved value to the
// correct origin — built-in default, the loaded env file, or a real (exported)
// environment variable — reusing the single source-resolution helper that also
// backs `config show` and the eval pre-flight echo (POLA #1). The three cases
// exercise all three sources by set/unset of the environment in the test.
func TestSourceAttribution(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want Source
		// setup mutates the environment (after clearEnv) for the case.
		setup func(t *testing.T)
	}{
		{
			name: "default: a field with a built-in default and no env",
			key:  "templates-repo",
			want: SourceDefault,
			setup: func(t *testing.T) {
				t.Setenv("PROJECT_ID", "proj-123") // avoid ErrMissingProjectID
			},
		},
		{
			name: "env: an exported variable",
			key:  "project-id",
			want: SourceEnv,
			setup: func(t *testing.T) {
				t.Setenv("MIZAN_PROJECT_ID", "from-shell")
			},
		},
		{
			name: "env-file: a value only in the loaded .env",
			key:  "project-id",
			want: SourceEnvFile,
			setup: func(t *testing.T) {
				dir := t.TempDir()
				envPath := filepath.Join(dir, "mizan.env")
				if err := os.WriteFile(envPath, []byte("MIZAN_PROJECT_ID=from-file\n"), 0o600); err != nil {
					t.Fatalf("write env file: %v", err)
				}
				// godotenv.Load does not override an already-present variable (even
				// empty), so unset the keys the file supplies to let it win.
				os.Unsetenv("MIZAN_PROJECT_ID")
				os.Unsetenv("PROJECT_ID")
				t.Setenv("MIZAN_ENV_FILE", envPath)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			tc.setup(t)

			c, err := LoadConfig()
			if err != nil && err != ErrMissingProjectID {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got := c.SourceOf(tc.key); got != tc.want {
				t.Errorf("SourceOf(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// TestUnknownEnvVarWarningsFires proves an unrecognized MIZAN_* variable produces
// a warning with a did-you-mean suggestion for the near-miss recognized key —
// the exact typo class from the config-precedence incident (POLA #2).
func TestUnknownEnvVarWarningsFires(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIZAN_PROJECT", "oops-wrong-key")

	warns := UnknownEnvVarWarnings()
	var line string
	for _, w := range warns {
		if strings.Contains(w, "MIZAN_PROJECT ") || strings.HasSuffix(w, "MIZAN_PROJECT") {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("no warning for MIZAN_PROJECT; got %v", warns)
	}
	if !strings.Contains(line, "did you mean MIZAN_PROJECT_ID?") {
		t.Errorf("warning missing did-you-mean suggestion: %q", line)
	}
}

// TestUnknownEnvVarWarningsSilentOnRecognized proves recognized variables — both
// `config set` keys and operational vars — never produce a warning.
func TestUnknownEnvVarWarningsSilentOnRecognized(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIZAN_PROJECT_ID", "proj-123")
	t.Setenv("MIZAN_DEFAULT_MODEL", "gemini-3.5-flash")
	t.Setenv("MIZAN_ALLOW_CUSTOM_ENDPOINT", "1")
	t.Setenv("MIZAN_ENV_FILE", "/nonexistent/mizan.env")

	if warns := UnknownEnvVarWarnings(); len(warns) != 0 {
		t.Errorf("recognized MIZAN_* vars produced warnings: %v", warns)
	}
}

// TestUnknownEnvVarWarningsNoSuggestionForUnrelated proves a MIZAN_* var with no
// near-miss recognized key still warns, but without a spurious suggestion.
func TestUnknownEnvVarWarningsNoSuggestionForUnrelated(t *testing.T) {
	clearEnv(t)
	t.Setenv("MIZAN_XYZZY_PLUGH", "1")

	warns := UnknownEnvVarWarnings()
	var line string
	for _, w := range warns {
		if strings.Contains(w, "MIZAN_XYZZY_PLUGH") {
			line = w
		}
	}
	if line == "" {
		t.Fatalf("no warning for MIZAN_XYZZY_PLUGH; got %v", warns)
	}
	if strings.Contains(line, "did you mean") {
		t.Errorf("unexpected suggestion for an unrelated var: %q", line)
	}
}

// TestRecognizedEnvVarsCoverFields is a drift guard: every Field env var must be
// in the recognized set, so the unknown-var warning can never flag a variable the
// loader actually reads.
func TestRecognizedEnvVarsCoverFields(t *testing.T) {
	recognized := recognizedEnvVars()
	for _, f := range Fields() {
		for _, v := range f.EnvVars {
			if !recognized[v] {
				t.Errorf("Field %q env var %q not in recognized set", f.Key, v)
			}
		}
	}
}

// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
)

// configKeys maps friendly `config set` keys to their environment-variable
// names. It is DERIVED from config.Fields — the single source of truth shared
// with `config show` — so the keys `config set` accepts and their target env
// vars can never drift from what `config show` displays. `config set` persists
// to <UserConfigDir>/mizan/.env, the same trusted location LoadConfig reads
// (never the current working directory).
var configKeys = buildConfigKeys()

// buildConfigKeys derives the friendly-key -> env-var map from config.Fields
// (the canonical env var is EnvVars[0], the name `config set` writes).
func buildConfigKeys() map[string]string {
	m := make(map[string]string, len(config.Fields()))
	for _, f := range config.Fields() {
		m[f.Key] = f.EnvVars[0]
	}
	return m
}

// dotenvPath returns the trusted env-file path <UserConfigDir>/mizan/.env that
// `config set` writes and LoadConfig reads. It never uses the current working
// directory (avoids the CWD-.env trust bug closed in internal/config).
func dotenvPath() (string, error) {
	if p := os.Getenv("MIZAN_ENV_FILE"); p != "" {
		return p, nil
	}
	dir, err := config.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "mizan", ".env"), nil
}

// newConfigCmd wires the `mizan config` command family.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "View and set Mizan configuration",
		GroupID: groupConfig,
	}
	cmd.AddCommand(newConfigShowCmd(), newConfigSetCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use: "show",
		// `config list` is an alias for `config show`, for parity with
		// `registry list`.
		Aliases: []string{"list"},
		Short:   "Show resolved configuration (alias: list)",
		Long: "Show the resolved configuration. Each row is labelled by the exact " +
			"`config set` KEY (so the output round-trips into `config set`) and carries " +
			"its SOURCE: env (an exported variable), env-file (the loaded .env), or " +
			"default (built-in).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if outputFormat == outputJSON {
				return printJSON(w, cfg)
			}
			tw := newTabWriter(w)
			fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
			// Rows derive from the SAME source of truth as `config set`, so labels
			// (the keys) can never drift from the accepted keys.
			for _, f := range config.Fields() {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", f.Key, showValue(f, cfg), cfg.SourceOf(f.Key))
			}
			return tw.Flush()
		},
	}
}

// showValue renders a field's value for `config show`. An empty value becomes
// "(unset)"; the default-model field additionally annotates the built-in
// fallback so the user sees exactly what an eval will use when neither a flag nor
// a template pins a model (WI-F3).
func showValue(f config.Field, cfg *config.Config) string {
	v := f.Value(cfg)
	if f.Key == "default-model" {
		return orBuiltinModel(v)
	}
	if v == "" {
		return "(unset)"
	}
	return v
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value (persisted to <UserConfigDir>/mizan/.env)",
		Long:  "Set a configuration value in <UserConfigDir>/mizan/.env. Valid keys: " + strings.Join(sortedKeys(), ", "),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			env, ok := configKeys[key]
			if !ok {
				return fmt.Errorf("unknown config key %q (valid: %s)", key, strings.Join(sortedKeys(), ", "))
			}

			path, err := dotenvPath()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}

			existing, _ := godotenv.Read(path) // empty map if absent
			if existing == nil {
				existing = map[string]string{}
			}
			existing[env] = value
			if err := godotenv.Write(existing, path); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			// Restrict perms: the env file is the natural home for future secrets.
			if err := os.Chmod(path, 0o600); err != nil {
				return fmt.Errorf("chmod %s: %w", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set %s (%s) in %s\n", key, env, path)
			return nil
		},
	}
}

// orBuiltinModel renders the resolved default model for `config show`: the
// configured default-model when set, otherwise the built-in fallback annotated
// as such so the user sees exactly what an eval will use when neither a flag nor
// a template pins a model (WI-F3).
func orBuiltinModel(s string) string {
	if s == "" {
		return eval.BuiltinDefaultModel + " (built-in)"
	}
	return s
}

func sortedKeys() []string {
	keys := make([]string, 0, len(configKeys))
	for k := range configKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

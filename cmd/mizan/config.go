package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/eval"
)

// configKeys maps friendly `config set` keys to their environment-variable
// names. `config set` persists to <UserConfigDir>/mizan/.env, the same trusted
// location LoadConfig reads (never the current working directory).
var configKeys = map[string]string{
	"project-id":     "MIZAN_PROJECT_ID",
	"location":       "MIZAN_LOCATION",
	"staging-bucket": "MIZAN_STAGING_BUCKET",
	"api-endpoint":   "MIZAN_API_ENDPOINT",
	"registry-db":    "MIZAN_REGISTRY_DB",
	"pack-cache":     "MIZAN_PACK_CACHE",
	"templates-repo": "MIZAN_TEMPLATES_REPO",
	"default-model":  "MIZAN_DEFAULT_MODEL",
}

// dotenvPath returns the trusted env-file path <UserConfigDir>/mizan/.env that
// `config set` writes and LoadConfig reads. It never uses the current working
// directory (avoids the CWD-.env trust bug closed in internal/config).
func dotenvPath() (string, error) {
	dir, err := os.UserConfigDir()
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
		Use:   "show",
		Short: "Show resolved configuration",
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
			fmt.Fprintf(tw, "ProjectID:\t%s\n", orUnset(cfg.ProjectID))
			fmt.Fprintf(tw, "Location:\t%s\n", cfg.Location)
			fmt.Fprintf(tw, "StagingBucket:\t%s\n", orUnset(cfg.StagingBucket))
			fmt.Fprintf(tw, "APIEndpoint:\t%s\n", orUnset(cfg.APIEndpoint))
			fmt.Fprintf(tw, "RegistryDBPath:\t%s\n", cfg.RegistryDBPath)
			fmt.Fprintf(tw, "PackCacheDir:\t%s\n", cfg.PackCacheDir)
			fmt.Fprintf(tw, "DefaultTemplatesRepo:\t%s\n", cfg.DefaultTemplatesRepo)
			fmt.Fprintf(tw, "DefaultModel:\t%s\n", orBuiltinModel(cfg.DefaultModel))
			return tw.Flush()
		},
	}
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

func orUnset(s string) string {
	if s == "" {
		return "(unset)"
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

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

// configKeys maps friendly `config set` keys to their environment-variable
// names. LoadConfig reads a .env from the working directory, so `config set`
// persists there.
var configKeys = map[string]string{
	"project-id":     "MIZAN_PROJECT_ID",
	"location":       "MIZAN_LOCATION",
	"staging-bucket": "MIZAN_STAGING_BUCKET",
	"api-endpoint":   "MIZAN_API_ENDPOINT",
	"registry-db":    "MIZAN_REGISTRY_DB",
	"pack-cache":     "MIZAN_PACK_CACHE",
	"templates-repo": "MIZAN_TEMPLATES_REPO",
}

const dotenvPath = ".env"

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
			return tw.Flush()
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value (persisted to ./.env)",
		Long:  "Set a configuration value in ./.env. Valid keys: " + strings.Join(sortedKeys(), ", "),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			env, ok := configKeys[key]
			if !ok {
				return fmt.Errorf("unknown config key %q (valid: %s)", key, strings.Join(sortedKeys(), ", "))
			}

			existing, _ := godotenv.Read(dotenvPath) // empty map if absent
			if existing == nil {
				existing = map[string]string{}
			}
			existing[env] = value
			if err := godotenv.Write(existing, dotenvPath); err != nil {
				return fmt.Errorf("write %s: %w", dotenvPath, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set %s (%s) in %s\n", key, env, dotenvPath)
			return nil
		},
	}
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

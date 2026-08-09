package main

import "github.com/spf13/cobra"

// newRegistryCmd wires the `mizan registry` command family. Subcommand bodies
// are implemented in the CLI phase against internal/registry.
func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "registry",
		Short:   "Create, list, get, update, and delete metric templates",
		GroupID: groupRegistry,
	}
	cmd.AddCommand(
		&cobra.Command{Use: "list", Short: "List metric templates", RunE: notImplemented},
		&cobra.Command{Use: "get <name>", Short: "Show a metric template", RunE: notImplemented},
		&cobra.Command{Use: "create", Short: "Create a metric template", RunE: notImplemented},
		&cobra.Command{Use: "delete <name>", Short: "Delete a metric template", RunE: notImplemented},
	)
	return cmd
}

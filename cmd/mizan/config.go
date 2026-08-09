package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// errNotImplemented is returned by scaffold command stubs.
var errNotImplemented = errors.New("not implemented")

func notImplemented(cmd *cobra.Command, _ []string) error {
	return errNotImplemented
}

// newConfigCmd wires the `mizan config` command family.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		Short:   "View and set Mizan configuration",
		GroupID: groupConfig,
	}
	cmd.AddCommand(
		&cobra.Command{Use: "show", Short: "Show resolved configuration", RunE: notImplemented},
		&cobra.Command{Use: "set", Short: "Set a configuration value", RunE: notImplemented},
	)
	return cmd
}

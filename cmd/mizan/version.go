package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/version"
)

// newVersionCmd wires the `mizan version` command. It prints the build version,
// git commit, and build date injected via -ldflags at build time (see the
// Makefile and .github/workflows/release.yml). It honors the persistent
// --output flag: `--output json` emits the structured version.Info.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print mizan version, git commit, and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := version.Get()
			w := cmd.OutOrStdout()
			if outputFormat == outputJSON {
				return printJSON(w, info)
			}
			fmt.Fprintln(w, info.String())
			return nil
		},
	}
}

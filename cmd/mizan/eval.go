package main

import "github.com/spf13/cobra"

// newEvalCmd wires the `mizan eval` command family.
func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "eval",
		Short:   "Run metric templates against assets",
		GroupID: groupEval,
	}
	cmd.AddCommand(
		&cobra.Command{Use: "run", Short: "Run a pointwise/rubric metric", RunE: notImplemented},
		&cobra.Command{Use: "pairwise", Short: "Run a pairwise comparison", RunE: notImplemented},
	)
	return cmd
}

package main

import "github.com/spf13/cobra"

const (
	groupRegistry = "registry"
	groupEval     = "eval"
	groupConfig   = "config"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "mizan",
		Short: "Create, manage, share, and run Gemini LLM-as-a-Judge metric templates",
		Long: "Mizan manages a registry of Gemini-based autorater (\"LLM-as-a-Judge\") " +
			"metric templates and runs them against assets of any modality using the " +
			"Vertex AI Gen AI Evaluation Service.",
		SilenceUsage: true,
		// SilenceErrors stops cobra from printing "Error: <err>" itself so that
		// main.go remains the single error printer (lowercase "error:" prefix +
		// os.Exit(1)). Without this, every error path prints twice. (WI-QF6)
		SilenceErrors: true,
	}

	root.AddGroup(
		&cobra.Group{ID: groupRegistry, Title: "Registry commands:"},
		&cobra.Group{ID: groupEval, Title: "Evaluation commands:"},
		&cobra.Group{ID: groupConfig, Title: "Config commands:"},
	)

	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", outputTable,
		"output format: table|json")
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		return validOutput(outputFormat)
	}

	root.AddCommand(newRegistryCmd(), newEvalCmd(), newConfigCmd())
	return root
}

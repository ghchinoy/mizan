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

import "github.com/spf13/cobra"

const (
	groupRegistry = "registry"
	groupEval     = "eval"
	groupConfig   = "config"
	groupRubric   = "rubric"
	groupResults  = "results"
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
		&cobra.Group{ID: groupResults, Title: "Results commands:"},
		&cobra.Group{ID: groupRubric, Title: "Rubric authoring commands:"},
		&cobra.Group{ID: groupConfig, Title: "Config commands:"},
	)

	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", outputTable,
		"output format: table|json")
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		return validOutput(outputFormat)
	}

	root.AddCommand(newRegistryCmd(), newPackCmd(), newEvalCmd(), newResultsCmd(), newRubricCmd(), newConfigCmd(), newVersionCmd())
	return root
}

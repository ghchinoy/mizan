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

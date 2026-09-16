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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/staxexport"
	"github.com/ghchinoy/mizan/internal/wire"
)

// newExportCmd wires the `mizan export` command family: one-directional export of
// a local Mizan metric template into an external evaluator format. Today it has a
// single target, `stax` (the Stax LLMEvaluator interchange format, L1 —
// design/mizan-stax-export-spec.md).
func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "export",
		Short:   "Export a metric template to an external evaluator format",
		GroupID: groupExport,
	}
	cmd.AddCommand(newExportStaxCmd())
	return cmd
}

// newExportStaxCmd wires `export stax --metric <id> [--flatten] [--out <file>]`.
// It reads ONE local template from the registry and writes Stax LLMEvaluator
// JSON to a file or stdout. It is credential-free: it opens only the local
// registry (wire.OpenService — SQLite, no Vertex/genai client, no ADC) and never
// touches the network or any key (design invariants N3, ADC-only).
func newExportStaxCmd() *cobra.Command {
	var (
		metric  string
		flatten bool
		out     string
		mapArgs []string
	)
	cmd := &cobra.Command{
		Use:   "stax --metric <id> [--flatten] [--out <file>]",
		Short: "Export a metric template as Stax LLMEvaluator JSON",
		Long: "Export a local Mizan metric template into the Stax LLMEvaluator interchange\n" +
			"format (design/mizan-stax-export-spec.md).\n\n" +
			"Supported kinds (text modality): pointwise and rubric.\n\n" +
			"  rubric     defaults to Option B (fan-out): ONE Stax evaluator per\n" +
			"             (group, criterion) pair, named {id}::{group}::{criterion}, so\n" +
			"             per-criterion scores and rationales are preserved. Pass\n" +
			"             --flatten for Option A (one aggregate evaluator) — LOSSY:\n" +
			"             per-criterion granularity is dropped and a warning is printed.\n" +
			"  pointwise  direct map: RatingRubric bands become output_categories in ONE\n" +
			"             evaluator.\n\n" +
			"pairwise, custom_schema, and non-text modalities are unsupported in v1 and\n" +
			"fail closed (no lossy guess). A prompt placeholder that maps to no Stax\n" +
			"reserved var — or two fields that map to the SAME one — is a hard error;\n" +
			"use --placeholder-map to override a non-conventional field name.\n\n" +
			"Output is a single JSON object when one evaluator is produced, or a JSON\n" +
			"array when several are (rubric fan-out). No credentials are read or emitted:\n" +
			"the exporter never calls Vertex/genai and never migrates keys.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if metric == "" {
				return fmt.Errorf("--metric is required (the template id to export)")
			}
			overrides, err := parsePlaceholderMap(mapArgs)
			if err != nil {
				return err
			}

			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			tmpl, err := svc.Get(cmd.Context(), metric)
			if err != nil {
				return err
			}

			// Convert BEFORE opening the output file so a fail-closed error writes
			// nothing (design decision 5).
			res, err := staxexport.Export(*tmpl, staxexport.Options{
				Flatten:             flatten,
				PlaceholderOverride: overrides,
			})
			if err != nil {
				return err
			}

			// Warnings (e.g. the flatten granularity loss) go to stderr, sanitized —
			// they can echo user-authored criterion names (security O1).
			for _, w := range res.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", sanitizeCell(w))
			}

			data, err := marshalEvaluators(res.Evaluators)
			if err != nil {
				return err
			}
			return writeExportOutput(cmd.OutOrStdout(), out, data)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "template id to export (required)")
	cmd.Flags().BoolVar(&flatten, "flatten", false, "rubric Option A: emit ONE aggregate evaluator instead of fan-out (LOSSY — drops per-criterion granularity)")
	cmd.Flags().StringVar(&out, "out", "", "write to this file instead of stdout")
	cmd.Flags().StringArrayVar(&mapArgs, "placeholder-map", nil, `override the placeholder rename map, "mizan_field=stax_var" (repeatable; stax_var one of output|prompt|expected_output|history)`)
	return cmd
}

// parsePlaceholderMap parses the repeatable --placeholder-map "field=stax_var"
// flags into an override table for staxexport.
func parsePlaceholderMap(args []string) (map[string]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, a := range args {
		rawField, rawTarget, ok := strings.Cut(a, "=")
		field, target := strings.TrimSpace(rawField), strings.TrimSpace(rawTarget)
		if !ok || field == "" || target == "" {
			return nil, fmt.Errorf(`--placeholder-map %q: want "mizan_field=stax_var"`, a)
		}
		out[field] = target
	}
	return out, nil
}

// marshalEvaluators renders the evaluators as indented JSON: a single object when
// exactly one evaluator is produced (pointwise / flatten), or an array when
// several are (rubric fan-out) — matching the spec worked examples (§6). HTML
// escaping is disabled so template braces and text stay literal in the output.
func marshalEvaluators(evs []staxexport.Evaluator) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	var v any = evs
	if len(evs) == 1 {
		v = evs[0]
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeExportOutput writes the rendered JSON to stdout (out == "") or to the
// named file (0o644). The file is created only after a successful conversion, so
// a fail-closed export never leaves a partial or stale file.
func writeExportOutput(stdout io.Writer, out string, data []byte) error {
	if out == "" {
		_, err := stdout.Write(data)
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", out, err)
	}
	return nil
}

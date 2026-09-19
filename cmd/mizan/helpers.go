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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/wire"
)

// outputFormat is the value of the persistent --output flag.
var outputFormat string

const (
	outputTable = "table"
	outputJSON  = "json"
)

// mustConfig loads config, tolerating a missing project ID for commands that do
// not need eval (registry CRUD, config show). Eval commands call requireProject
// afterwards.
func mustConfig() (*config.Config, error) {
	cfg, err := config.LoadConfig()
	if err != nil && err != config.ErrMissingProjectID {
		return nil, err
	}
	return cfg, nil
}

// requireProject returns an error if the project ID is unset (needed for eval).
func requireProject(cfg *config.Config) error {
	if cfg.ProjectID == "" {
		return config.ErrMissingProjectID
	}
	return nil
}

// printJSON writes v as indented JSON.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// newTabWriter returns a tabwriter suitable for aligned table output.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

func validOutput(format string) error {
	switch format {
	case outputTable, outputJSON:
		return nil
	default:
		return fmt.Errorf("invalid --output %q (want table|json)", format)
	}
}

// openEngine constructs a live eval.Engine via the composition root. It
// requires a project ID.
//
// It is a package-level var ONLY so command tests can substitute a fake engine
// (built over a fake eval.EvaluationClient) and exercise the full
// `eval adaptive --save-as` provenance round-trip against a real temp-file
// sqlite store without live ADC/Vertex — mirroring the newRubricGenerator seam
// in rubric.go. Production always builds the live engine via wire.NewEngine.
var openEngine = func(ctx context.Context, cfg *config.Config) (*eval.Engine, func() error, error) {
	if err := requireProject(cfg); err != nil {
		return nil, nil, err
	}
	return wire.NewEngine(ctx, cfg)
}

// renderTemplate prints a single template as JSON or a key/value table.
func renderTemplate(w io.Writer, t *registry.MetricTemplate) error {
	if outputFormat == outputJSON {
		return printJSON(w, t)
	}
	tw := newTabWriter(w)
	fmt.Fprintf(tw, "ID:\t%s\n", t.ID)
	fmt.Fprintf(tw, "Name:\t%s\n", t.Name)
	if t.Version != "" {
		fmt.Fprintf(tw, "Version:\t%s\n", t.Version)
	}
	fmt.Fprintf(tw, "Kind:\t%s\n", t.Kind)
	fmt.Fprintf(tw, "Modalities:\t%s\n", joinModalities(t.Modalities))
	if t.License != "" {
		fmt.Fprintf(tw, "License:\t%s\n", t.License)
	}
	if len(t.Authors) > 0 {
		fmt.Fprintf(tw, "Authors:\t%s\n", joinAuthors(t.Authors))
	}
	for _, in := range t.Inputs {
		fmt.Fprintf(tw, "Input[%s]:\t%s (required=%t)\n", in.Name, in.Modality, in.Required)
	}
	fmt.Fprintf(tw, "Model:\t%s\n", t.AutoraterModel)
	fmt.Fprintf(tw, "SamplingCount:\t%d\n", t.SamplingCount)
	if t.Source != "" {
		fmt.Fprintf(tw, "Source:\t%s\n", t.Source)
	}
	if t.Description != "" {
		fmt.Fprintf(tw, "Description:\t%s\n", t.Description)
	}
	if t.MetricPromptTemplate != "" {
		fmt.Fprintf(tw, "Prompt:\t%s\n", firstLine(t.MetricPromptTemplate))
	}
	if len(t.Choices) > 0 {
		fmt.Fprintf(tw, "Choices:\t%s\n", strings.Join(t.Choices, ", "))
	}
	if len(t.RubricGroups) > 0 {
		names := make([]string, 0, len(t.RubricGroups))
		for name := range t.RubricGroups {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(tw, "RubricGroup[%s]:\t%s\n", name, strings.Join(t.RubricGroups[name], "; "))
		}
	}
	if t.ResponseSchema != nil && t.ResponseSchema.JSON != "" {
		fmt.Fprintf(tw, "ResponseSchema:\t%s\n", firstLine(t.ResponseSchema.JSON))
	}
	return tw.Flush()
}

// renderTemplateList prints a table (or JSON array) of templates.
func renderTemplateList(w io.Writer, ts []registry.MetricTemplate) error {
	if outputFormat == outputJSON {
		return printJSON(w, ts)
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tNAME\tKIND\tMODEL")
	for _, t := range ts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Kind, t.AutoraterModel)
	}
	return tw.Flush()
}

// renderImportReport prints the outcome of `registry import`. Text mode prints a
// one-line summary of the per-action counts followed by the per-template actions
// (with reasons, so conflicts and skips are visible); JSON mode prints the whole
// report. A dry run is flagged so the counts are clearly a preview.
func renderImportReport(w io.Writer, r registry.ImportReport) error {
	if outputFormat == outputJSON {
		return printJSON(w, r)
	}
	if r.DryRun {
		fmt.Fprint(w, "dry run (no changes written): ")
	}
	fmt.Fprintf(w,
		"%d inserted, %d updated, %d skipped, %d conflicted, %d unchanged, %d forked (source: %s)\n",
		r.Inserted, r.Updated, r.Skipped, r.Conflicted, r.Unchanged, r.Forked, r.Source.Origin)
	for _, e := range r.Entries {
		if e.Reason != "" {
			fmt.Fprintf(w, "  %s: %s (%s)\n", e.Action, e.ID, e.Reason)
		} else {
			fmt.Fprintf(w, "  %s: %s\n", e.Action, e.ID)
		}
	}
	return nil
}

// renderExportReport prints the outcome of `registry export` / `pack add`. Text
// mode prints a one-line summary followed by the per-template actions; JSON mode
// prints the whole report.
func renderExportReport(w io.Writer, r registry.ExportReport) error {
	if outputFormat == outputJSON {
		return printJSON(w, r)
	}
	fmt.Fprintf(w, "%d written, %d skipped (dest: %s)\n", r.Written, r.Skipped, r.Dest)
	for _, e := range r.Entries {
		switch {
		case e.Reason != "":
			fmt.Fprintf(w, "  %s: %s (%s)\n", e.Action, e.ID, e.Reason)
		case e.Path != "":
			fmt.Fprintf(w, "  %s: %s -> %s\n", e.Action, e.ID, e.Path)
		default:
			fmt.Fprintf(w, "  %s: %s\n", e.Action, e.ID)
		}
	}
	return nil
}

func joinAuthors(as []registry.Author) string {
	var s []string
	for _, a := range as {
		switch {
		case a.Name != "" && a.Email != "":
			s = append(s, fmt.Sprintf("%s <%s>", a.Name, a.Email))
		case a.Name != "":
			s = append(s, a.Name)
		case a.Email != "":
			s = append(s, a.Email)
		}
	}
	return strings.Join(s, ", ")
}

func joinModalities(ms []registry.Modality) string {
	var s []string
	for _, m := range ms {
		s = append(s, string(m))
	}
	return strings.Join(s, ",")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
func openEngine(ctx context.Context, cfg *config.Config) (*eval.Engine, func() error, error) {
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
	fmt.Fprintf(tw, "Kind:\t%s\n", t.Kind)
	fmt.Fprintf(tw, "Modalities:\t%s\n", joinModalities(t.Modalities))
	fmt.Fprintf(tw, "Model:\t%s\n", t.AutoraterModel)
	fmt.Fprintf(tw, "SamplingCount:\t%d\n", t.SamplingCount)
	if t.Description != "" {
		fmt.Fprintf(tw, "Description:\t%s\n", t.Description)
	}
	if t.MetricPromptTemplate != "" {
		fmt.Fprintf(tw, "Prompt:\t%s\n", firstLine(t.MetricPromptTemplate))
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

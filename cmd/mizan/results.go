package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/results"
	"github.com/ghchinoy/mizan/internal/wire"
)

// newResultsCmd wires the `mizan results` command family — the read side of the
// eval results store (design §4.8). It mirrors the `registry` noun: a parent
// command grouping the query verbs, each depending only on the results.Service
// façade via wire (never internal/results/sqlite — the seam that keeps a
// Firestore backend a wire constructor swap). Phase 1 ships only `list` and
// `show`; compare/trends/delete are later phases.
func newResultsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "results",
		Short:   "List and inspect stored eval results",
		GroupID: groupResults,
	}
	cmd.AddCommand(
		newResultsListCmd(),
		newResultsShowCmd(),
	)
	return cmd
}

// newResultsListCmd wires `mizan results list [--metric <id>] [--namespace <ns>]
// [--since <t>] [--limit N]`. It maps the flags onto a results.ResultFilter,
// calls Service.List (newest first), and renders a table or, with -o json, the
// whole []Result. Untrusted (judge/model-derived) cells go through sanitizeCell.
func newResultsListCmd() *cobra.Command {
	var (
		metric    string
		namespace string
		since     string
		limit     int
	)
	cmd := &cobra.Command{
		Use:   "list [--metric <id>] [--namespace <ns>] [--since <RFC3339-or-date>] [--limit N]",
		Short: "List stored eval results (newest first)",
		Long: "List eval results persisted by `eval run` / `eval pairwise`.\n\n" +
			"Filter by template id (--metric), id namespace (--namespace), and run\n" +
			"time (--since, an RFC3339 timestamp or a YYYY-MM-DD date). --limit caps\n" +
			"the number of rows (0 = backend default).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}

			filter := results.ResultFilter{
				TemplateID: metric,
				Namespace:  namespace,
				Limit:      limit,
			}
			if since != "" {
				t, err := parseSince(since)
				if err != nil {
					return err
				}
				filter.Since = t
			}

			svc, closeSvc, err := wire.OpenResultService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			rs, err := svc.List(cmd.Context(), filter)
			if err != nil {
				return err
			}
			return renderResultList(cmd.OutOrStdout(), cmd.ErrOrStderr(), rs)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "filter by exact template id (<namespace>/<slug>)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "filter by template id namespace")
	cmd.Flags().StringVar(&since, "since", "", "only results at or after this time (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of results to return (0 = backend default)")
	return cmd
}

// newResultsShowCmd wires `mizan results show <run-id>`. It fetches one result by
// RunID (ErrNotFound → a crisp non-zero-exit error) and renders full provenance:
// the template id+version+contentHash, the applied autorater, the rubric ref (for
// rubric templates), the stored inputs, and the outcome. -o json emits the whole
// Result. Untrusted cells (explanation, input values) go through sanitizeCell.
func newResultsShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <run-id>",
		Short: "Show full provenance and outcome for one stored result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}

			svc, closeSvc, err := wire.OpenResultService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			r, err := svc.Get(cmd.Context(), args[0])
			if err != nil {
				if errors.Is(err, results.ErrNotFound) {
					return fmt.Errorf("no result with run id %q", args[0])
				}
				return err
			}
			return renderResultDetail(cmd.OutOrStdout(), r)
		},
	}
	return cmd
}

// parseSince accepts either a full RFC3339 timestamp or a bare YYYY-MM-DD date
// (interpreted as midnight UTC), so `--since 2026-08-17` and
// `--since 2026-08-17T12:00:00Z` both work.
func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q (want RFC3339 or YYYY-MM-DD)", s)
}

// metricRef renders the "<id>@<version>" template reference for a table cell.
func metricRef(r results.Result) string {
	if r.Template.Version == "" {
		return r.Template.ID
	}
	return r.Template.ID + "@" + r.Template.Version
}

// outcomeSummary renders the one-cell score/choice summary for the list table.
func outcomeSummary(o results.Outcome) string {
	switch {
	case o.Score != nil:
		return fmt.Sprintf("%g", *o.Score)
	case o.PairwiseChoice != "":
		return o.PairwiseChoice
	default:
		return "-"
	}
}

// renderResultList prints results as an aligned table (or JSON with -o json). An
// empty result set prints a friendly note on stderr plus an empty table / `[]`.
func renderResultList(w, errw io.Writer, rs []results.Result) error {
	if outputFormat == outputJSON {
		if rs == nil {
			rs = []results.Result{}
		}
		return printJSON(w, rs)
	}
	if len(rs) == 0 {
		fmt.Fprintln(errw, "no results found")
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "RUN ID\tRUN AT\tMETRIC\tOUTCOME\tMODEL")
	for _, r := range rs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.RunID,
			r.RunAt.Format(time.RFC3339),
			sanitizeCell(metricRef(r)),
			sanitizeCell(outcomeSummary(r.Outcome)),
			sanitizeCell(r.Autorater.Model),
		)
	}
	return tw.Flush()
}

// renderResultDetail prints the full provenance + outcome for one result (or the
// whole Result as JSON with -o json). Judge/model-derived and input-derived text
// is passed through sanitizeCell before it reaches a terminal cell (security O1,
// same discipline as eval.go).
func renderResultDetail(w io.Writer, r *results.Result) error {
	if outputFormat == outputJSON {
		return printJSON(w, r)
	}
	tw := newTabWriter(w)
	fmt.Fprintf(tw, "Run ID:\t%s\n", r.RunID)
	fmt.Fprintf(tw, "Run At:\t%s\n", r.RunAt.Format(time.RFC3339))
	fmt.Fprintf(tw, "Command:\t%s\n", sanitizeCell(r.Invocation.Command))
	if r.Invocation.ProjectID != "" {
		fmt.Fprintf(tw, "Project:\t%s\n", sanitizeCell(r.Invocation.ProjectID))
	}
	if r.Invocation.HostLabel != "" {
		fmt.Fprintf(tw, "Host:\t%s\n", sanitizeCell(r.Invocation.HostLabel))
	}
	if r.Mizan.Version != "" {
		fmt.Fprintf(tw, "Mizan:\t%s (%s)\n", sanitizeCell(r.Mizan.Version), sanitizeCell(r.Mizan.Commit))
	}

	// Template ref — the exact-version anchor (id + version + contentHash).
	fmt.Fprintf(tw, "Template ID:\t%s\n", sanitizeCell(r.Template.ID))
	fmt.Fprintf(tw, "Template Version:\t%s\n", sanitizeCell(r.Template.Version))
	fmt.Fprintf(tw, "Template ContentHash:\t%s\n", sanitizeCell(r.Template.ContentHash))
	fmt.Fprintf(tw, "Template Kind:\t%s\n", sanitizeCell(string(r.Template.Kind)))
	if r.Template.Source != "" {
		fmt.Fprintf(tw, "Template Source:\t%s\n", sanitizeCell(r.Template.Source))
	}

	// Applied autorater — RESOLVED model + effective host + location + source.
	fmt.Fprintf(tw, "Autorater Model:\t%s\n", sanitizeCell(r.Autorater.Model))
	fmt.Fprintf(tw, "Autorater ModelSource:\t%s\n", sanitizeCell(r.Autorater.ModelSource))
	fmt.Fprintf(tw, "Autorater EffectiveHost:\t%s\n", sanitizeCell(r.Autorater.EffectiveHost))
	fmt.Fprintf(tw, "Autorater Location:\t%s\n", sanitizeCell(r.Autorater.Location))
	fmt.Fprintf(tw, "Autorater SamplingCount:\t%d\n", r.Autorater.SamplingCount)
	fmt.Fprintf(tw, "Autorater FlipEnabled:\t%t\n", r.Autorater.FlipEnabled)

	// Rubric ref (rubric templates only). Method is "authored" and the scale may
	// be empty for registry-loaded templates — a documented provenance limitation,
	// not a defect (the store records the template faithfully, never synthesizes).
	if r.Rubric != nil {
		fmt.Fprintf(tw, "Rubric Method:\t%s\n", sanitizeCell(r.Rubric.Method))
		if r.Rubric.GeneratorModel != "" {
			fmt.Fprintf(tw, "Rubric GeneratorModel:\t%s\n", sanitizeCell(r.Rubric.GeneratorModel))
		}
		if r.Rubric.Recipe != "" {
			fmt.Fprintf(tw, "Rubric Recipe:\t%s\n", sanitizeCell(r.Rubric.Recipe))
		}
		if len(r.Rubric.Origins) > 0 {
			fmt.Fprintf(tw, "Rubric Origins:\t%s\n", sanitizeCell(strings.Join(r.Rubric.Origins, ", ")))
		}
		if r.Rubric.ScaleMin != nil && r.Rubric.ScaleMax != nil {
			fmt.Fprintf(tw, "Rubric Scale:\t%d-%d\n", *r.Rubric.ScaleMin, *r.Rubric.ScaleMax)
		} else {
			fmt.Fprintf(tw, "Rubric Scale:\t(not recorded)\n")
		}
		fmt.Fprintf(tw, "Rubric DetailMode:\t%t\n", r.Rubric.DetailMode)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// Inputs — one row per stored field (field / modality / mode / hash /
	// inline-or-URI). The inline value is judge/user text → sanitize it.
	if len(r.Inputs) > 0 {
		fmt.Fprintln(w, "Inputs:")
		itw := newTabWriter(w)
		fmt.Fprintln(itw, "FIELD\tMODALITY\tMODE\tHASH\tVALUE")
		for _, in := range r.Inputs {
			value := in.URI
			if in.Mode == results.ModeInline {
				value = in.Inline
			}
			fmt.Fprintf(itw, "%s\t%s\t%s\t%s\t%s\n",
				sanitizeCell(in.Field),
				sanitizeCell(string(in.Modality)),
				sanitizeCell(string(in.Mode)),
				sanitizeCell(in.ContentHash),
				sanitizeCell(value),
			)
		}
		if err := itw.Flush(); err != nil {
			return err
		}
	}

	// Outcome — score/choice/explanation/warnings/duration/tokens.
	otw := newTabWriter(w)
	if r.Outcome.Score != nil {
		fmt.Fprintf(otw, "Score:\t%g\n", *r.Outcome.Score)
	}
	if r.Outcome.PairwiseChoice != "" {
		fmt.Fprintf(otw, "Choice:\t%s\n", sanitizeCell(r.Outcome.PairwiseChoice))
	}
	if r.Outcome.Explanation != "" {
		fmt.Fprintf(otw, "Explanation:\t%s\n", sanitizeCell(r.Outcome.Explanation))
	}
	for _, wn := range r.Outcome.Warnings {
		fmt.Fprintf(otw, "Warning:\t%s\n", sanitizeCell(wn))
	}
	fmt.Fprintf(otw, "Duration:\t%s\n", time.Duration(r.Outcome.DurationNS).Round(time.Millisecond))
	if tu := r.Outcome.TokenUsage; tu != nil {
		fmt.Fprintf(otw, "Tokens:\tprompt=%d candidates=%d total=%d\n",
			tu.PromptTokens, tu.CandidatesTokens, tu.TotalTokens)
	}
	return otw.Flush()
}

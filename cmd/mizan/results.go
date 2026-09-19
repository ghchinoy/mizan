package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/registry"
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
		newResultsSummaryCmd(),
		newResultsTrendCmd(),
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
		tags      []string
	)
	cmd := &cobra.Command{
		Use:   "list [--metric <id>] [--namespace <ns>] [--tag <T>]... [--since <RFC3339-or-date>] [--limit N]",
		Short: "List stored eval results (newest first)",
		Long: "List eval results persisted by `eval run` / `eval pairwise`.\n\n" +
			"Filter by template id (--metric), id namespace (--namespace), and run\n" +
			"time (--since, an RFC3339 timestamp or a YYYY-MM-DD date). --limit caps\n" +
			"the number of rows (0 = backend default).\n\n" +
			"--tag filters to results whose template currently carries ALL given tags\n" +
			"(repeatable, AND-narrowing, case-sensitive exact match). Because the\n" +
			"results store does not persist tags, --tag is a registry->results join:\n" +
			"tags are resolved to template ids against the registry's CURRENT tags,\n" +
			"then results for those templates are merged newest-first. --since/--limit\n" +
			"are applied AFTER the merge.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}

			// --metric and --tag select templates by two different mechanisms (exact
			// id vs a current-tags registry join); combining them is ambiguous in v1,
			// so reject it explicitly rather than silently ignoring one.
			if metric != "" && len(tags) > 0 {
				return fmt.Errorf("--metric and --tag cannot be combined")
			}

			var sinceT time.Time
			if since != "" {
				sinceT, err = parseSince(since)
				if err != nil {
					return err
				}
			}

			resultSvc, closeResults, err := wire.OpenResultService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeResults() }()

			var rs []results.Result
			if len(tags) > 0 {
				// Registry->results tag join (design §4.A step 4). The results store
				// does not persist tags, so --tag cannot be a store filter: resolve
				// the tag set to template ids via the registry's CURRENT tags, query
				// results per id, then merge. This reflects each template's current
				// tags by design (immutable results carry point-in-time provenance,
				// not a stale copy of a tag that has since moved).
				regSvc, closeReg, err := wire.OpenService(cfg)
				if err != nil {
					return err
				}
				defer func() { _ = closeReg() }()

				perTemplate, err := resolveTaggedResults(cmd.Context(), regSvc, resultSvc,
					TaggedResultsQuery{Tags: tags, Namespace: namespace})
				if err != nil {
					return err
				}
				// `results list` has no --until flag (design §4.A), so pass a zero
				// upper bound; --since then --limit still window-before-cap.
				rs = mergeTaggedResults(perTemplate, sinceT, time.Time{}, limit)
			} else {
				rs, err = resultSvc.List(cmd.Context(), results.ResultFilter{
					TemplateID: metric,
					Namespace:  namespace,
					Since:      sinceT,
					Limit:      limit,
				})
				if err != nil {
					return err
				}
			}
			return renderResultList(cmd.OutOrStdout(), cmd.ErrOrStderr(), rs)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "filter by exact template id (<namespace>/<slug>)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "filter by template id namespace")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "filter to results whose template currently carries ALL given tags (repeatable)")
	cmd.Flags().StringVar(&since, "since", "", "only results at or after this time (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of results to return (0 = backend default)")
	return cmd
}

// TaggedResultsQuery selects which templates a tag join gathers results for. It
// mirrors the registry-side filter that resolves a tag set to template ids:
// AND-narrowing, case-sensitive tags (identical to `registry list --tag`) plus an
// optional id-namespace narrowing applied at the registry layer. It is the input
// to resolveTaggedResults.
type TaggedResultsQuery struct {
	Tags      []string
	Namespace string
}

// resolveTaggedResults performs the FULL registry->results tag-join resolution and
// fetch (design §4.A step 4), the reusable half of the join shared by
// `results list --tag` and B3's `results summary`/`trend --tag` (design §4.C).
// It resolves q.Tags to template ids against the registry's CURRENT tags, then
// fetches every stored result for each matching template, returning one slice per
// matching template (each ordered newest-first, as the store returns it). Callers
// combine the slices themselves — `results list` via mergeTaggedResults, B3 via
// its aggregation. An empty resolution (no template carries the tag set) returns
// an empty, non-nil slice and a nil error, which mergeTaggedResults then renders
// as the existing "no results found" note.
func resolveTaggedResults(ctx context.Context, regSvc *registry.Service, resSvc *results.Service, q TaggedResultsQuery) ([][]results.Result, error) {
	tmpls, err := regSvc.List(ctx, registry.ListFilter{Tags: q.Tags, Namespace: q.Namespace})
	if err != nil {
		return nil, err
	}
	perTemplate := make([][]results.Result, 0, len(tmpls))
	for _, t := range tmpls {
		sub, err := resSvc.List(ctx, results.ResultFilter{TemplateID: t.ID})
		if err != nil {
			return nil, err
		}
		perTemplate = append(perTemplate, sub)
	}
	return perTemplate, nil
}

// mergeTaggedResults is the pure core of the registry->results tag join (design
// §4.A step 4). It concatenates the per-template result slices, orders the merged
// set newest-first, then applies the [since, until] time window and finally the
// --limit cap — all AFTER the merge (a per-template limit would truncate each
// template independently and miss newer rows from other templates).
//
// ORDER MATTERS: the [since, until] window is applied BEFORE --limit so the
// newest-N selection is taken from the in-window rows, exactly as the direct
// --metric path does (there the store applies since/until/limit together). If
// --limit were applied first, `--until T --limit N` would truncate to the newest
// N and THEN drop rows newer than until, returning fewer in-window rows than
// exist — a window-inconsistent rollup. A zero since/until bound is a no-op, so
// callers with no --until (e.g. `results list`, which has none) pass time.Time{}.
//
// An empty input (no template resolved the tag set) yields no rows, which
// renderResultList surfaces as the existing "no results found" note.
func mergeTaggedResults(perTemplate [][]results.Result, since, until time.Time, limit int) []results.Result {
	var merged []results.Result
	for _, rs := range perTemplate {
		merged = append(merged, rs...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].RunAt.After(merged[j].RunAt)
	})
	if !since.IsZero() || !until.IsZero() {
		kept := merged[:0]
		for _, r := range merged {
			if !since.IsZero() && r.RunAt.Before(since) {
				continue
			}
			if !until.IsZero() && r.RunAt.After(until) {
				continue
			}
			kept = append(kept, r)
		}
		merged = kept
	}
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
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

// newResultsSummaryCmd wires `mizan results summary` — a READ-ONLY per-template
// rollup over stored results (design §4.C). It resolves the same filters as
// `results list` (--metric | --tag, --namespace, --since/--until, --limit),
// calls Service.List (or the registry->results tag join for --tag), then computes
// n/mean/min/max/stddev per template via results.Summarize. With --threshold it
// also reports pass/fail/passRate. It depends ONLY on the results.Service façade
// (+ registry service for --tag), never on internal/results/sqlite, preserving
// the Firestore-swap seam.
//
// Scope (design §4.C): B3 v1 aggregates `eval run`/`eval pairwise` results grouped
// by template and by tag. Per-eval-set (scorecard) aggregation is out of scope
// because eval-set runs are never persisted; cost/token trend is out of scope
// because cost is not persisted. No fake rows are ever synthesized.
func newResultsSummaryCmd() *cobra.Command {
	var (
		metric    string
		namespace string
		since     string
		until     string
		limit     int
		threshold float64
		tags      []string
	)
	cmd := &cobra.Command{
		Use:   "summary [--metric <id> | --tag <T>...] [--namespace <ns>] [--since t] [--until t] [--limit N] [--threshold X]",
		Short: "Summarize stored eval results per template (n/mean/min/max/stddev)",
		Long: "Summarize eval results persisted by `eval run` / `eval pairwise`, grouped\n" +
			"by template (id + version). Reports n, mean, min, max and stddev of the\n" +
			"score per template; unscored results (a genai error left no score) are\n" +
			"EXCLUDED from the statistics and counted separately as n_unscored.\n\n" +
			"--threshold X additionally reports pass/fail/passRate (a result passes when\n" +
			"its score >= X).\n\n" +
			"Filter by exact template id (--metric), id namespace (--namespace), run\n" +
			"time (--since/--until, RFC3339 or YYYY-MM-DD), and row cap (--limit).\n" +
			"--tag aggregates over ALL templates currently carrying the given tags\n" +
			"(repeatable, AND-narrowing, case-sensitive) via the registry->results join.\n\n" +
			"NOTE: eval-set (scorecard) runs are not persisted, so there is no per\n" +
			"eval-set aggregation; cost/token trend is not reported (cost is not\n" +
			"persisted). Use -o json for the full rollup incl. score-distribution buckets.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			if metric != "" && len(tags) > 0 {
				return fmt.Errorf("--metric and --tag cannot be combined")
			}
			sinceT, untilT, err := parseSinceUntil(since, until)
			if err != nil {
				return err
			}

			rs, err := collectResults(cmd.Context(), cfg, resultsQuery{
				metric:    metric,
				namespace: namespace,
				tags:      tags,
				since:     sinceT,
				until:     untilT,
				limit:     limit,
			})
			if err != nil {
				return err
			}

			var thr *float64
			if cmd.Flags().Changed("threshold") {
				thr = &threshold
			}
			summaries := results.Summarize(rs, thr)
			return renderResultSummary(cmd.OutOrStdout(), cmd.ErrOrStderr(), summaries, thr)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "filter by exact template id (<namespace>/<slug>)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "filter by template id namespace")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "aggregate over templates currently carrying ALL given tags (repeatable)")
	cmd.Flags().StringVar(&since, "since", "", "only results at or after this time (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "only results at or before this time (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of results to aggregate (0 = backend default)")
	cmd.Flags().Float64Var(&threshold, "threshold", 0, "report pass/fail/passRate against this score threshold")
	return cmd
}

// newResultsTrendCmd wires `mizan results trend --metric <id>` — a READ-ONLY,
// time-bucketed mean-score view over one template's stored results (design §4.C).
// It calls Service.List then results.Trend, emitting one row per day/week bucket.
// With --per-criterion it also emits the per-criterion means parsed from the
// persisted CustomOutput.per_criterion blob (rubric-detail results). It depends
// only on the results.Service façade, never on internal/results/sqlite.
//
// Scope (design §4.C): score trend only. Cost/token trend is out of scope (cost is
// not persisted; native EvaluateInstances returns no token usage). No fake data.
func newResultsTrendCmd() *cobra.Command {
	var (
		metric       string
		since        string
		until        string
		bucket       string
		perCriterion bool
	)
	cmd := &cobra.Command{
		Use:   "trend --metric <id> [--bucket day|week] [--per-criterion] [--since t] [--until t]",
		Short: "Show a time-bucketed mean-score trend for one template",
		Long: "Show the mean score of one template's eval results bucketed by day or\n" +
			"week (design §4.C). --metric is required. Unscored results are EXCLUDED\n" +
			"from each bucket's mean and counted as n_unscored.\n\n" +
			"--bucket selects day (default) or week granularity (weeks keyed by their\n" +
			"UTC Monday). --per-criterion additionally reports the per-criterion mean\n" +
			"per bucket, parsed from the persisted rubric-detail CustomOutput.\n\n" +
			"NOTE: score trend only. Cost/token trend is not reported (cost is not\n" +
			"persisted; the native Vertex path returns no token usage).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			if metric == "" {
				return fmt.Errorf("--metric is required for results trend")
			}
			tb, err := parseTrendBucket(bucket)
			if err != nil {
				return err
			}
			sinceT, untilT, err := parseSinceUntil(since, until)
			if err != nil {
				return err
			}

			svc, closeSvc, err := wire.OpenResultService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			rs, err := svc.List(cmd.Context(), results.ResultFilter{
				TemplateID: metric,
				Since:      sinceT,
				Until:      untilT,
			})
			if err != nil {
				return err
			}
			points := results.Trend(rs, tb)
			return renderResultTrend(cmd.OutOrStdout(), cmd.ErrOrStderr(), points, perCriterion)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "template id to trend (<namespace>/<slug>) — required")
	cmd.Flags().StringVar(&since, "since", "", "only results at or after this time (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "only results at or before this time (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&bucket, "bucket", "day", "time bucket granularity: day|week")
	cmd.Flags().BoolVar(&perCriterion, "per-criterion", false, "also report per-criterion means (rubric-detail results)")
	return cmd
}

// resultsQuery carries the resolved filters collectResults turns into stored
// results — either a direct Service.List (TemplateID/Namespace/Since/Until/Limit)
// or, when Tags is set, the registry->results tag join.
type resultsQuery struct {
	metric    string
	namespace string
	tags      []string
	since     time.Time
	until     time.Time
	limit     int
}

// collectResults fetches the stored results a summary/list aggregates over,
// honoring the two mutually-exclusive template-selection mechanisms: an exact
// --metric (plain Service.List) or a --tag set (the registry->results join,
// reusing resolveTaggedResults — design §4.C). It depends only on the results and
// registry Service façades, never on a concrete backend.
func collectResults(ctx context.Context, cfg *config.Config, q resultsQuery) ([]results.Result, error) {
	resultSvc, closeResults, err := wire.OpenResultService(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = closeResults() }()

	if len(q.tags) > 0 {
		regSvc, closeReg, err := wire.OpenService(cfg)
		if err != nil {
			return nil, err
		}
		defer func() { _ = closeReg() }()

		perTemplate, err := resolveTaggedResults(ctx, regSvc, resultSvc,
			TaggedResultsQuery{Tags: q.tags, Namespace: q.namespace})
		if err != nil {
			return nil, err
		}
		// Window by [since, until] BEFORE the --limit cap so the tag path matches
		// the --metric path's semantics (the store applies since/until/limit
		// together); mergeTaggedResults enforces that ordering.
		return mergeTaggedResults(perTemplate, q.since, q.until, q.limit), nil
	}

	return resultSvc.List(ctx, results.ResultFilter{
		TemplateID: q.metric,
		Namespace:  q.namespace,
		Since:      q.since,
		Until:      q.until,
		Limit:      q.limit,
	})
}

// parseSinceUntil parses the optional --since/--until flags (each RFC3339 or
// YYYY-MM-DD), returning zero times for empty flags.
func parseSinceUntil(since, until string) (time.Time, time.Time, error) {
	var sinceT, untilT time.Time
	var err error
	if since != "" {
		if sinceT, err = parseSince(since); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if until != "" {
		if untilT, err = parseSince(until); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid --until %q (want RFC3339 or YYYY-MM-DD)", until)
		}
	}
	return sinceT, untilT, nil
}

// parseTrendBucket validates the --bucket flag into a results.TrendBucket.
func parseTrendBucket(s string) (results.TrendBucket, error) {
	switch s {
	case "", "day":
		return results.TrendDay, nil
	case "week":
		return results.TrendWeek, nil
	default:
		return "", fmt.Errorf("invalid --bucket %q (want day|week)", s)
	}
}

// fmtFloatPtr renders an optional statistic: the value to 4 significant digits, or
// "-" when nil (no scored results fed it).
func fmtFloatPtr(f *float64) string {
	if f == nil {
		return "-"
	}
	return fmt.Sprintf("%.4g", *f)
}

// renderResultSummary prints the per-template rollup as an aligned table (or the
// full []TemplateSummary as JSON with -o json). The threshold columns appear only
// when a threshold was given. All template ids pass through sanitizeCell.
func renderResultSummary(w, errw io.Writer, summaries []results.TemplateSummary, threshold *float64) error {
	if outputFormat == outputJSON {
		if summaries == nil {
			summaries = []results.TemplateSummary{}
		}
		return printJSON(w, summaries)
	}
	if len(summaries) == 0 {
		fmt.Fprintln(errw, "no results found")
	}
	tw := newTabWriter(w)
	header := "METRIC\tN\tUNSCORED\tMEAN\tMIN\tMAX\tSTDDEV"
	if threshold != nil {
		header += "\tPASS\tFAIL\tPASS%"
	}
	fmt.Fprintln(tw, header)
	for _, s := range summaries {
		ref := s.TemplateID
		if s.TemplateVersion != "" {
			ref = s.TemplateID + "@" + s.TemplateVersion
		}
		row := fmt.Sprintf("%s\t%d\t%d\t%s\t%s\t%s\t%s",
			sanitizeCell(ref), s.N, s.NUnscored,
			fmtFloatPtr(s.Mean), fmtFloatPtr(s.Min), fmtFloatPtr(s.Max), fmtFloatPtr(s.Stddev))
		if threshold != nil {
			if s.Threshold != nil {
				row += fmt.Sprintf("\t%d\t%d\t%.1f%%", s.Threshold.Pass, s.Threshold.Fail, s.Threshold.PassRate*100)
			} else {
				row += "\t-\t-\t-"
			}
		}
		fmt.Fprintln(tw, row)
	}
	return tw.Flush()
}

// renderResultTrend prints the time-bucketed trend as an aligned table (or the
// full []TrendPoint as JSON with -o json). With perCriterion, per-criterion means
// are printed as indented rows under each bucket. Criterion labels pass through
// sanitizeCell (judge/author-derived text).
func renderResultTrend(w, errw io.Writer, points []results.TrendPoint, perCriterion bool) error {
	if outputFormat == outputJSON {
		if points == nil {
			points = []results.TrendPoint{}
		}
		return printJSON(w, points)
	}
	if len(points) == 0 {
		fmt.Fprintln(errw, "no results found")
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "BUCKET\tN\tUNSCORED\tMEAN")
	for _, p := range points {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", sanitizeCell(p.Bucket), p.N, p.NUnscored, fmtFloatPtr(p.Mean))
		if perCriterion {
			for _, c := range p.PerCriterion {
				label := c.Group + "/" + c.Criterion
				mean := c.Mean
				fmt.Fprintf(tw, "  %s\t%d\t\t%s\n", sanitizeCell(label), c.N, fmtFloatPtr(&mean))
			}
		}
	}
	return tw.Flush()
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
	if p, ok := o.CustomOutput["passed"].(bool); ok {
		if p {
			return "PASS"
		}
		return "FAIL"
	}
	if sel, ok := o.CustomOutput["selection"].(string); ok && sel != "" {
		return sel
	}
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
		// Origins adds information only for a mixed-origin (union-before-freeze)
		// rubric; suppress it when the lone recorded origin just restates Method
		// (e.g. a single-origin adaptive-generated draft).
		origins := r.Rubric.Origins
		redundant := len(origins) == 1 && origins[0] == r.Rubric.Method
		if len(origins) > 0 && !redundant {
			fmt.Fprintf(tw, "Rubric Origins:\t%s\n", sanitizeCell(strings.Join(origins, ", ")))
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
	if p, ok := r.Outcome.CustomOutput["passed"].(bool); ok {
		status := "FAIL"
		if p {
			status = "PASS"
		}
		if conf, ok := r.Outcome.CustomOutput["confidence"].(float64); ok {
			fmt.Fprintf(otw, "Passed:\t%s (confidence=%.2f)\n", status, conf)
		} else {
			fmt.Fprintf(otw, "Passed:\t%s\n", status)
		}
	}
	if sel, ok := r.Outcome.CustomOutput["selection"].(string); ok && sel != "" {
		fmt.Fprintf(otw, "Selection:\t%s\n", sanitizeCell(sel))
	}
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

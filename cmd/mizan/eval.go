package main

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/wire"
)

// newEvalCmd wires the `mizan eval` command family.
func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "eval",
		Short:   "Run metric templates against assets",
		GroupID: groupEval,
	}
	cmd.AddCommand(newEvalRunCmd())
	cmd.AddCommand(newEvalPairwiseCmd())
	// eval batch (P3) is intentionally not wired in this slice.
	return cmd
}

func newEvalRunCmd() *cobra.Command {
	var (
		metric       string
		model        string
		stats        bool
		rubricDetail bool
		rubricScale  string
		fields       []string
		files        []string
		gcs          []string
	)
	cmd := &cobra.Command{
		Use:   "run --metric <id> [--field key=value] [--file key=/path] [--gcs key=gs://…]",
		Short: "Run a pointwise (or rubric/custom_schema) metric against a live eval call",
		Long: "Run a metric template against an instance.\n\n" +
			"Supply instance fields with:\n" +
			"  --field key=text     a text value\n" +
			"  --file  key=/path    a local asset; the engine stages it to GCS\n" +
			"  --gcs   key=gs://…   a pre-staged asset\n\n" +
			"Multimodal (image/audio/video/music) fields go through the native\n" +
			"ContentMap path, which requires gs:// FileData; local --file assets are\n" +
			"staged automatically when a StagingBucket is configured.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if metric == "" {
				return fmt.Errorf("--metric is required")
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

			inst, err := buildInstance(fields, files, gcs)
			if err != nil {
				return err
			}

			// Validate the user-supplied --model BEFORE it is echoed to stderr or
			// composed into a Vertex resource name, so a malformed value fails
			// locally instead of injecting into the pre-flight line / remote call.
			if err := eval.ValidateModel(model); err != nil {
				return err
			}

			// --rubric-detail routes a rubric template through the genai
			// structured-output path for per-criterion transparency. Parse the
			// scale locally so a malformed --rubric-scale fails before any call.
			runOpts := []eval.RunOption{eval.WithModel(model)}
			if rubricDetail {
				min, max, err := eval.ParseRubricScale(rubricScale)
				if err != nil {
					return err
				}
				runOpts = append(runOpts, eval.WithRubricDetail(min, max))
			}

			eng, closeEng, err := openEngine(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeEng() }()

			// Pre-flight echo (WI-F7): the resolved project/location/model line is
			// on by default (cheap, high-value) and reflects the actual per-path
			// location (native=regional, genai=global; rubric-detail is genai/global).
			target := eng.Resolve(*tmpl, model, rubricDetail)
			projSrc, locSrc := preflightSources(cfg, target)
			printPreflight(cmd.ErrOrStderr(), target, projSrc, locSrc)

			res, err := eng.Run(cmd.Context(), *tmpl, inst, runOpts...)
			if err != nil {
				return err
			}
			// Surface non-fatal run warnings on stderr (never stdout) in text mode;
			// in --output json mode they also serialize under "warnings". See
			// emitWarnings for the shared emission behavior.
			emitWarnings(cmd.ErrOrStderr(), res.Warnings)
			return renderResult(cmd.OutOrStdout(), res, stats)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "template id to run (required)")
	cmd.Flags().StringVar(&model, "model", "", "override autorater model for this run (highest precedence)")
	cmd.Flags().BoolVar(&stats, "stats", false, "print per-run stats (timing always; token usage on the genai/custom_schema path only)")
	cmd.Flags().BoolVar(&rubricDetail, "rubric-detail", false, "for a rubric template, return per-criterion scores via the genai structured path (location=global; drops sampling)")
	cmd.Flags().StringVar(&rubricScale, "rubric-scale", "1-5", "Likert scale for --rubric-detail as \"<min>-<max>\" (two non-negative integers, min<max; negative bounds not supported)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "text instance field as key=value (repeatable)")
	cmd.Flags().StringArrayVar(&files, "file", nil, "local asset field as key=/path; engine stages to GCS (repeatable)")
	cmd.Flags().StringArrayVar(&gcs, "gcs", nil, "pre-staged asset field as key=gs://… (repeatable)")
	return cmd
}

func newEvalPairwiseCmd() *cobra.Command {
	var (
		metric    string
		model     string
		stats     bool
		baseline  string
		candidate string
		fields    []string
		files     []string
		gcs       []string
	)
	cmd := &cobra.Command{
		Use:   "pairwise --metric <id> --baseline key=… --candidate key=… [--field/--file/--gcs …]",
		Short: "Compare a baseline and candidate response with a pairwise metric",
		Long: "Run a pairwise metric template. Provide the baseline and candidate\n" +
			"responses with --baseline key=value and --candidate key=value, where\n" +
			"the keys match the template's baseline/candidate field names. Additional\n" +
			"placeholders use --field/--file/--gcs (media compares via gs:// FileData).\n" +
			"Prints the PairwiseChoice (BASELINE/CANDIDATE/TIE) and explanation.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if metric == "" {
				return fmt.Errorf("--metric is required")
			}
			if baseline == "" || candidate == "" {
				return fmt.Errorf("--baseline and --candidate are required (key=value)")
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

			// The baseline/candidate responses are ordinary fields keyed by the
			// template's field names; append them to any extra placeholders.
			inst, err := buildInstance(append([]string{baseline, candidate}, fields...), files, gcs)
			if err != nil {
				return err
			}

			// Validate the user-supplied --model BEFORE it is echoed to stderr or
			// composed into a Vertex resource name (see eval run).
			if err := eval.ValidateModel(model); err != nil {
				return err
			}

			eng, closeEng, err := openEngine(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeEng() }()

			// Pre-flight echo (WI-F7): default-on resolved project/location/model.
			// Pairwise never uses the rubric-detail lever.
			target := eng.Resolve(*tmpl, model, false)
			projSrc, locSrc := preflightSources(cfg, target)
			printPreflight(cmd.ErrOrStderr(), target, projSrc, locSrc)

			res, err := eng.Run(cmd.Context(), *tmpl, inst, eval.WithModel(model))
			if err != nil {
				return err
			}
			// Surface non-fatal run warnings (e.g. the pairwise flip caveat) on
			// stderr (never stdout) in text mode; in --output json mode they also
			// serialize under "warnings". See emitWarnings for the shared behavior.
			emitWarnings(cmd.ErrOrStderr(), res.Warnings)
			return renderResult(cmd.OutOrStdout(), res, stats)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "pairwise template id to run (required)")
	cmd.Flags().StringVar(&model, "model", "", "override autorater model for this run (highest precedence)")
	cmd.Flags().BoolVar(&stats, "stats", false, "print per-run stats (timing always; token usage on the genai/custom_schema path only)")
	cmd.Flags().StringVar(&baseline, "baseline", "", "baseline response as key=value (key = template BaselineFieldName)")
	cmd.Flags().StringVar(&candidate, "candidate", "", "candidate response as key=value (key = template CandidateFieldName)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "additional text field as key=value (repeatable)")
	cmd.Flags().StringArrayVar(&files, "file", nil, "additional local asset field as key=/path (repeatable)")
	cmd.Flags().StringArrayVar(&gcs, "gcs", nil, "additional pre-staged asset field as key=gs://… (repeatable)")
	return cmd
}

// buildInstance turns --field/--file/--gcs key=value pairs into an eval.Instance.
// Text fields carry an explicit text modality; --file yields an AssetRef.FilePath
// and --gcs an AssetRef.GCSUri, leaving MIME/modality for the engine to resolve
// (cmd/* must not import internal/asset, so no MIME detection happens here). Keys
// must be unique across all three flags.
func buildInstance(fields, files, gcs []string) (eval.Instance, error) {
	inst := eval.Instance{Fields: map[string]eval.AssetRef{}}
	add := func(raw, flag string, mk func(v string) eval.AssetRef) error {
		k, v, ok := strings.Cut(raw, "=")
		if !ok || k == "" {
			return fmt.Errorf("invalid --%s %q (want key=value)", flag, raw)
		}
		if _, dup := inst.Fields[k]; dup {
			return fmt.Errorf("duplicate field key %q", k)
		}
		inst.Fields[k] = mk(v)
		return nil
	}
	for _, f := range fields {
		if err := add(f, "field", func(v string) eval.AssetRef {
			return eval.AssetRef{Modality: registry.ModalityText, Text: v}
		}); err != nil {
			return eval.Instance{}, err
		}
	}
	for _, f := range files {
		if err := add(f, "file", func(v string) eval.AssetRef {
			return eval.AssetRef{FilePath: v}
		}); err != nil {
			return eval.Instance{}, err
		}
	}
	for _, f := range gcs {
		if err := add(f, "gcs", func(v string) eval.AssetRef {
			return eval.AssetRef{GCSUri: v}
		}); err != nil {
			return eval.Instance{}, err
		}
	}
	return inst, nil
}

// parseFields turns --field key=value pairs into a text Instance. It is retained
// as a thin wrapper over buildInstance for the text-only case.
func parseFields(fields []string) (eval.Instance, error) {
	return buildInstance(fields, nil, nil)
}

// printPreflight writes the default-on, one-line resolved-target echo to stderr
// BEFORE the eval call (WI-F7). Keeping it on stderr means it never pollutes the
// --output json result on stdout, yet is always visible so a wrong project or an
// unexpected global location surfaces immediately instead of only when the API
// rejects the call.
func printPreflight(w io.Writer, t eval.ResolvedTarget, projSrc, locSrc string) {
	// Sanitize every interpolated value at the output boundary so the echo is
	// ALWAYS exactly one line, regardless of input. A fully-qualified
	// "projects/.../models/<seg>" model is trusted verbatim by the resolution
	// path, so a self-supplied --model with a control character (e.g. a newline)
	// in the trailing segment could otherwise inject an extra line into the
	// caller's own stderr. Stripping control chars here closes that stderr-
	// injection class for both the bare and fully-qualified forms (security audit
	// INFO / review FYI2).
	//
	// The project/location source hints (src=env/env-file/default) reuse the
	// config source-resolution helper so an operator can see why a value is in
	// effect — the visibility gap the config-precedence findings flagged (POLA #1).
	fmt.Fprintf(w, "mizan: autorater → project=%s (src=%s) location=%s (src=%s) model=%s (path=%s)\n",
		sanitizeEchoValue(t.Project), sanitizeEchoValue(projSrc),
		sanitizeEchoValue(t.Location), sanitizeEchoValue(locSrc),
		sanitizeEchoValue(t.Model), sanitizeEchoValue(t.Path))
}

// preflightSources attributes the pre-flight project/location values to their
// origin, reusing the config source-resolution helper (POLA #1). When the
// resolved target matches the configured project/location, the value carries its
// config source (env/env-file/default); when a fully-qualified --model resource
// or the genai/global path overrides them, that override is named instead so the
// echo never claims a config source it did not actually use.
func preflightSources(cfg *config.Config, t eval.ResolvedTarget) (projSrc, locSrc string) {
	projSrc = "model" // a fully-qualified model resource carried its own project
	if t.Project == cfg.ProjectID {
		projSrc = cfg.SourceOf("project-id").String()
	}
	switch {
	case t.Location == cfg.Location:
		locSrc = cfg.SourceOf("location").String()
	case t.Path == "genai":
		locSrc = "global-path" // the genai path is always global (spike-custom)
	default:
		locSrc = "model" // a fully-qualified model resource carried its own location
	}
	return projSrc, locSrc
}

// emitWarnings surfaces non-fatal run warnings (e.g. the pairwise flip caveat or
// R-R2 rubric reconciliation extras) on stderr in text mode, mirroring the
// pre-flight echo (WI-F7) so they never break the human table on stdout. In
// --output json mode the warnings ALSO serialize into the result body under
// "warnings" (Result's `warnings,omitempty` tag), so machine consumers still see
// them; this helper only handles the text-mode stderr echo. Each warning is
// written verbatim on its own line, preserving the returned order. This is the
// single emission site shared by the eval-run and pairwise RunE paths.
func emitWarnings(w io.Writer, warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintln(w, warning)
	}
}

// sanitizeEchoValue drops control characters (newlines, carriage returns, and
// other C0/C1 control runes) from a value before it is written to the one-line
// pre-flight echo, guaranteeing the echo stays a single line.
func sanitizeEchoValue(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// ansiEscapePattern matches ANSI/VT escape sequences: CSI (ESC [ … final),
// OSC (ESC ] … BEL/ST), and single-character/other escapes (ESC <byte>). Judge
// output is untrusted, so these are stripped whole before any control-char pass
// (otherwise stripping the lone ESC byte would leave visible parameter text like
// "[31m").
var ansiEscapePattern = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]" + // CSI
	"|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC … BEL or ST
	"|\x1b[@-Z\\\\-_]") // two-char / other escapes

// sanitizeCell makes a judge-controlled string safe for a single terminal table
// cell: it strips ANSI escape sequences, then removes ALL control runes
// (including tabs and newlines, which would otherwise break the tabwriter column
// layout / spill a criterion across rows). Ordinary spaces are preserved, so the
// intended column content is kept intact (security O1).
func sanitizeCell(s string) string {
	s = ansiEscapePattern.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// renderResult prints an eval result as JSON or a small table. When showStats is
// true (the opt-in --stats flag), the table gains a stats footer: the wall-clock
// duration (always available) and the genai-path token usage, or a clear note
// that token usage is not available on the native path (WI-F4). JSON output
// always includes the stats object; --stats controls only the human footer.
func renderResult(w io.Writer, res eval.Result, showStats bool) error {
	if outputFormat == outputJSON {
		return printJSON(w, res)
	}
	if res.RubricDetail {
		return renderRubricDetailResult(w, res, showStats)
	}
	tw := newTabWriter(w)
	switch {
	case res.Score != nil:
		fmt.Fprintf(tw, "Score:\t%g\n", *res.Score)
	case res.PairwiseChoice != "":
		// Pairwise yields a Choice, not a Score; suppress the misleading
		// "Score: (none)" line and print only the Choice below (eval-triage #4).
	default:
		fmt.Fprintf(tw, "Score:\t(none)\n")
	}
	if res.PairwiseChoice != "" {
		fmt.Fprintf(tw, "Choice:\t%s\n", res.PairwiseChoice)
	}
	fmt.Fprintf(tw, "Explanation:\t%s\n", res.Explanation)
	if len(res.CustomOutput) > 0 {
		keys := make([]string, 0, len(res.CustomOutput))
		for k := range res.CustomOutput {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(tw, "CustomOutput[%s]:\t%v\n", k, res.CustomOutput[k])
		}
	}
	if showStats {
		fmt.Fprintf(tw, "Duration:\t%s\n", res.Stats.Duration.Round(time.Millisecond))
		if tu := res.Stats.TokenUsage; tu != nil {
			fmt.Fprintf(tw, "Tokens:\tprompt=%d candidates=%d total=%d\n",
				tu.PromptTokens, tu.CandidatesTokens, tu.TotalTokens)
		} else {
			fmt.Fprintf(tw, "Tokens:\ttoken usage not available on this path (native EvaluateInstances returns no usage)\n")
		}
	}
	return tw.Flush()
}

// renderStatsFooter appends the opt-in --stats footer (duration always; token
// usage on the genai path, else the native "not available" note) to tw.
func renderStatsFooter(tw io.Writer, res eval.Result) {
	fmt.Fprintf(tw, "Duration:\t%s\n", res.Stats.Duration.Round(time.Millisecond))
	if tu := res.Stats.TokenUsage; tu != nil {
		fmt.Fprintf(tw, "Tokens:\tprompt=%d candidates=%d total=%d\n",
			tu.PromptTokens, tu.CandidatesTokens, tu.TotalTokens)
	} else {
		fmt.Fprintf(tw, "Tokens:\ttoken usage not available on this path (native EvaluateInstances returns no usage)\n")
	}
}

// renderRubricDetailResult prints a rubric per-criterion result: the overall
// Score + Explanation (the explanation comes from CustomOutput on the genai
// path), then a readable per-criterion table (group / criterion / score /
// rationale) in its own aligned block, then the optional --stats footer.
func renderRubricDetailResult(w io.Writer, res eval.Result, showStats bool) error {
	tw := newTabWriter(w)
	if res.Score != nil {
		fmt.Fprintf(tw, "Score:\t%g\n", *res.Score)
	} else {
		fmt.Fprintf(tw, "Score:\t(none)\n")
	}
	explanation := res.Explanation
	if explanation == "" {
		if s, ok := res.CustomOutput["explanation"].(string); ok {
			explanation = s
		}
	}
	// The explanation and all per-criterion cells below are judge-controlled text.
	// Sanitize them (strip ANSI escapes and control chars) before writing to
	// stdout so a hostile/garbled judge response cannot inject terminal escape
	// sequences or break the table's one-row-per-criterion layout (security O1).
	// Scope is ONLY this rubric-detail renderer; the generic CustomOutput renderer
	// is left unchanged.
	fmt.Fprintf(tw, "Explanation:\t%s\n", sanitizeCell(explanation))
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w, "Per-criterion:")
	ptw := newTabWriter(w)
	fmt.Fprintln(ptw, "GROUP\tCRITERION\tSCORE\tRATIONALE")
	if pc, ok := res.CustomOutput["per_criterion"].([]any); ok {
		for _, item := range pc {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(ptw, "%s\t%s\t%v\t%s\n",
				sanitizeCell(fieldString(m, "group")), sanitizeCell(fieldString(m, "criterion")),
				fieldValue(m, "score"), sanitizeCell(fieldString(m, "rationale")))
		}
	}
	if err := ptw.Flush(); err != nil {
		return err
	}

	if showStats {
		stw := newTabWriter(w)
		renderStatsFooter(stw, res)
		return stw.Flush()
	}
	return nil
}

// fieldString returns m[key] as a string, or "" if absent/non-string.
func fieldString(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// fieldValue returns m[key] for %v rendering (scores may be int after clamping
// or a raw JSON float64), or "" if absent.
func fieldValue(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	return ""
}

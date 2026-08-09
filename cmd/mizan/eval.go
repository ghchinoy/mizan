package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
		metric string
		model  string
		stats  bool
		fields []string
		files  []string
		gcs    []string
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
			defer closeSvc()

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

			eng, closeEng, err := openEngine(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer closeEng()

			// Pre-flight echo (WI-F7): the resolved project/location/model line is
			// on by default (cheap, high-value) and reflects the actual per-path
			// location (native=regional, genai=global).
			printPreflight(cmd.ErrOrStderr(), eng.Resolve(*tmpl, model))

			res, err := eng.Run(cmd.Context(), *tmpl, inst, eval.WithModel(model))
			if err != nil {
				return err
			}
			return renderResult(cmd.OutOrStdout(), res, stats)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "template id to run (required)")
	cmd.Flags().StringVar(&model, "model", "", "override autorater model for this run (highest precedence)")
	cmd.Flags().BoolVar(&stats, "stats", false, "print per-run stats (timing always; token usage on the genai/custom_schema path only)")
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
			defer closeSvc()

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
			defer closeEng()

			// Pre-flight echo (WI-F7): default-on resolved project/location/model.
			printPreflight(cmd.ErrOrStderr(), eng.Resolve(*tmpl, model))

			res, err := eng.Run(cmd.Context(), *tmpl, inst, eval.WithModel(model))
			if err != nil {
				return err
			}
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
func printPreflight(w io.Writer, t eval.ResolvedTarget) {
	fmt.Fprintf(w, "mizan: autorater → project=%s location=%s model=%s (path=%s)\n",
		t.Project, t.Location, t.Model, t.Path)
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
	tw := newTabWriter(w)
	if res.Score != nil {
		fmt.Fprintf(tw, "Score:\t%g\n", *res.Score)
	} else {
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

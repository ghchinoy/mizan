package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

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
	// eval pairwise (WI-P1-4) and eval batch (P3) are intentionally not wired
	// in this slice.
	return cmd
}

func newEvalRunCmd() *cobra.Command {
	var (
		metric string
		fields []string
	)
	cmd := &cobra.Command{
		Use:   "run --metric <id> --field key=value [--field ...]",
		Short: "Run a text pointwise metric against a live EvaluateInstances call",
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

			inst, err := parseFields(fields)
			if err != nil {
				return err
			}

			eng, closeEng, err := openEngine(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer closeEng()

			res, err := eng.Run(cmd.Context(), *tmpl, inst)
			if err != nil {
				return err
			}
			return renderResult(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&metric, "metric", "", "template id to run (required)")
	cmd.Flags().StringArrayVar(&fields, "field", nil, "instance field as key=value (repeatable)")
	return cmd
}

// parseFields turns --field key=value pairs into a text Instance.
func parseFields(fields []string) (eval.Instance, error) {
	inst := eval.Instance{Fields: map[string]eval.AssetRef{}}
	for _, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return eval.Instance{}, fmt.Errorf("invalid --field %q (want key=value)", f)
		}
		inst.Fields[k] = eval.AssetRef{Modality: registry.ModalityText, Text: v}
	}
	return inst, nil
}

// renderResult prints an eval result as JSON or a small table.
func renderResult(w io.Writer, res eval.Result) error {
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
	return tw.Flush()
}

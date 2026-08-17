package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/evalset"
	"github.com/ghchinoy/mizan/internal/registry/pack"
	"github.com/ghchinoy/mizan/internal/wire"
)

// maxEvalSetManifestBytes bounds the CLI-side read of an --set manifest, matching
// pack.ParseEvalSet's internal 1 MiB parse limit so an oversized file is rejected
// before os.ReadFile loads it whole into memory.
const maxEvalSetManifestBytes = 1 << 20 // 1 MiB

// runEvalSet is the `eval run --set <path>` branch, kept out of eval.go (shared
// with other teams) to hold that file's footprint minimal. --set is PATH-BASED
// in Phase 1: setPath is a filesystem path to a §3.4a EvalSet manifest, parsed
// with pack.ParseEvalSet + evalset.FromDoc (no store, no id resolution;
// id-from-packs-tree is a documented fast-follow).
//
// It mirrors the metric path's project/model validation, builds the set inputs
// via the SHARED buildInstance (its Instance.Fields become RunOptions.Inputs by
// identity binding), wires the registry service (TemplateGetter) and eval engine
// (MemberRunner) through the composition root, runs the set, renders the
// scorecard to stdout, and finally applies the opt-in gate exit-code rule.
func runEvalSet(cmd *cobra.Command, setPath, model string, fields, files, gcs []string, failFast bool) error {
	cfg, err := mustConfig()
	if err != nil {
		return err
	}
	// Validate + apply the per-invocation --project flag BEFORE any client is
	// built or echoed, mirroring the metric path.
	if err := config.ValidateProjectID(projectOverride); err != nil {
		return err
	}
	applyProjectOverride(cfg, projectOverride)

	// Validate --model locally before it is composed into a Vertex resource name,
	// mirroring the metric path.
	if err := eval.ValidateModel(model); err != nil {
		return err
	}

	// Read + parse the manifest FILE (path-based), then convert to a runnable Set.
	// Bound the read at the CLI boundary so the documented 1 MiB manifest limit
	// (pack.ParseEvalSet's internal LimitReader) is enforced BEFORE the whole
	// file is slurped into memory — os.ReadFile alone would resident-load a
	// multi-GB file and OOM before parsing ever caps it.
	if err := checkEvalSetManifestSize(setPath); err != nil {
		return err
	}
	data, err := os.ReadFile(setPath)
	if err != nil {
		return fmt.Errorf("read eval-set manifest: %w", err)
	}
	doc, err := pack.ParseEvalSet(data)
	if err != nil {
		return err
	}
	set, err := evalset.FromDoc(doc)
	if err != nil {
		return err
	}

	// Build the SHARED set inputs via the same buildInstance path as the metric
	// run; its Fields are passed by identity to every member (RunOptions.Inputs).
	inst, err := buildInstance(fields, files, gcs)
	if err != nil {
		return err
	}

	svc, closeSvc, err := wire.OpenService(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeSvc() }()

	eng, closeEng, err := openEngine(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeEng() }()

	res, err := evalset.New(svc, eng).Run(cmd.Context(), set, evalset.RunOptions{
		Inputs:   inst.Fields,
		Model:    model,
		FailFast: failFast,
	})
	if err != nil {
		return err
	}

	if err := renderScorecard(cmd.OutOrStdout(), res, false); err != nil {
		return err
	}

	// Opt-in gate exit-code: a non-zero exit ONLY when the set is a gate AND the
	// verdict is FAILED. Silence usage so the concise gate error (printed to
	// stderr by cobra) is not buried under a usage dump; stdout already carries
	// the scorecard.
	if gateErr := evalSetGateError(res); gateErr != nil {
		cmd.SilenceUsage = true
		return gateErr
	}
	return nil
}

// checkEvalSetManifestSize rejects an --set manifest whose on-disk size exceeds
// the 1 MiB bound BEFORE os.ReadFile loads it whole into memory, so the documented
// limit (pack.ParseEvalSet's internal LimitReader) is enforced end-to-end and a
// multi-GB file cannot OOM the process ahead of parsing. A stat error is left for
// the subsequent os.ReadFile to surface with its own message.
func checkEvalSetManifestSize(setPath string) error {
	if fi, err := os.Stat(setPath); err == nil && fi.Size() > maxEvalSetManifestBytes {
		return fmt.Errorf("eval-set manifest %q too large: %d bytes (max %d)", setPath, fi.Size(), maxEvalSetManifestBytes)
	}
	return nil
}

// renderScorecard prints an EvalSetResult as JSON or a human scorecard table
// (design §10.3). It reuses the shared newTabWriter/printJSON/outputFormat
// helpers and applies sanitizeCell to every judge- or manifest-derived string
// cell (member id, error/note text) — that content is untrusted and could
// otherwise inject terminal escapes or break the tabwriter column layout.
//
// The scorecard ALWAYS shows the pass/fail verdict; it does NOT decide the
// process exit code (that is the gate's job, see evalSetGateError). showStats is
// accepted for symmetry with renderResult and reserved for a future per-member
// stats footer; Phase 1 keeps the summary scannable and defers per-member
// explanations to JSON / `eval-set get`.
func renderScorecard(w io.Writer, res evalset.EvalSetResult, _ bool) error {
	if outputFormat == outputJSON {
		return printJSON(w, res)
	}

	// Header line: set id / version / asset-class.
	header := fmt.Sprintf("EvalSet: %s", sanitizeCell(res.SetID))
	if res.Version != "" {
		header += fmt.Sprintf(" (v%s)", sanitizeCell(res.Version))
	}
	if res.AssetClass != "" {
		header += fmt.Sprintf("  asset-class: %s", sanitizeCell(res.AssetClass))
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w)

	// Per-member table.
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "MEMBER\tSTATUS\tWEIGHT\tSCORE\tNOTE")
	for _, m := range res.Members {
		score := "-"
		if m.Score != nil {
			score = fmt.Sprintf("%.2f", *m.Score)
		}
		fmt.Fprintf(tw, "%s\t%s\t%g\t%s\t%s\n",
			sanitizeCell(m.MetricID),
			memberStatusLabel(m.Status),
			m.Weight,
			score,
			sanitizeCell(m.Error))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// Aggregate line.
	fmt.Fprintln(w)
	fmt.Fprint(w, aggregateLine(res.Aggregate))
	if res.Aggregate.Threshold != nil {
		fmt.Fprintf(w, "   threshold: %g", *res.Aggregate.Threshold)
	}
	fmt.Fprintf(w, "   %s\n", res.Verdict)
	return nil
}

// aggregateLine renders the leading "Aggregate (<method> over <N> scored): <score>"
// portion of the summary line. The score is "-" when no member produced a numeric
// score. Method is empty when the manifest declared no aggregation block; in that
// case the method token is omitted so the line stays readable.
func aggregateLine(agg evalset.Aggregate) string {
	score := "-"
	if agg.Score != nil {
		score = fmt.Sprintf("%.2f", *agg.Score)
	}
	if agg.Method == "" {
		return fmt.Sprintf("Aggregate (over %d scored): %s", agg.Scored, score)
	}
	return fmt.Sprintf("Aggregate (%s over %d scored): %s", agg.Method, agg.Scored, score)
}

// memberStatusLabel maps a MemberStatus to its lowercase scorecard label
// (design §10.3 shows lowercase status tokens).
func memberStatusLabel(s evalset.MemberStatus) string {
	switch s {
	case evalset.OK:
		return "ok"
	case evalset.Errored:
		return "error"
	case evalset.Missing:
		return "missing"
	case evalset.Skipped:
		return "skipped"
	default:
		return string(s)
	}
}

// evalSetGateError implements the OPT-IN gate exit-code rule (owner-locked): a
// non-zero process exit happens ONLY when the set is a gate (res.Gate) AND the
// verdict is FAILED. It returns a concise error the caller returns to cobra
// (which prints it to stderr and exits non-zero); the caller sets
// SilenceUsage=true so the failure does not print usage. Gate off, or a passing
// verdict, returns nil (exit 0) — the scorecard already showed the verdict on
// stdout regardless.
func evalSetGateError(res evalset.EvalSetResult) error {
	if res.Gate && res.Verdict == evalset.Failed {
		// sanitizeCell strips terminal-escape/control sequences: res.SetID is the
		// manifest's metadata.id, untrusted content that cobra prints raw to stderr
		// otherwise (CWE-150). Mirrors the sanitization renderScorecard applies to
		// every other manifest-derived cell.
		return fmt.Errorf("eval-set gate failed: %s verdict for %s", res.Verdict, sanitizeCell(res.SetID))
	}
	return nil
}

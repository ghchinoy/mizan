package main

// pack.go wires the `mizan pack` command family. P2.2 ships `pack validate` (the
// creds-free PR gate); P2.4 adds the authoring subcommands `pack init` (scaffold
// a pack dir) and `pack add` (write one local template into a pack dir). Each
// phase keeps its own funcs/sections so the two rebase cleanly.
//
// The command depends ONLY on registry.Service / registry.ValidatePack, eval, and
// wire — never on the pack codec/sync constructors — preserving the architecture
// seam (design §3.1, enforced by cmd/mizan/seam_test.go).

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/wire"
)

// newPackCmd wires the `mizan pack` command family — validate (P2.2) plus the
// authoring surface (P2.4: init, add). It sits beside `registry` in the registry
// command group (design §3.7).
func newPackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pack",
		Short:   "Author and validate metric-template packs",
		GroupID: groupRegistry,
	}
	cmd.AddCommand(
		newPackValidateCmd(),
		newPackInitCmd(),
		newPackAddCmd(),
	)
	return cmd
}

// newPackValidateCmd wires `pack validate <path> [--dry-run]`. It runs the
// creds-free steps 1–5 (structural, identity, semantic, placeholder, lint) over
// every MetricTemplate and EvalSet manifest under <path>, reports ALL defects,
// and exits non-zero if any ERROR-severity finding is present (warnings never
// fail). <path> is a single pack dir OR a repo tree containing packs/.
//
// This is the binary mizan-templates CI `go install`s to gate PRs — steps 1–5
// need NO credentials. The optional --dry-run adds step 6: one live
// materialize+call per template to confirm API acceptance (needs creds).
func newPackValidateCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "validate <path>",
		Short: "Validate metric-template + eval-set pack manifests (creds-free PR gate)",
		Long: "Validate every MetricTemplate and EvalSet manifest under <path>.\n\n" +
			"<path> is a single pack directory or a repo tree that contains a packs/\n" +
			"directory. Steps 1–5 (structural schema, identity, kind-specific semantics,\n" +
			"placeholder consistency, and lint) run with NO credentials — this is the CI\n" +
			"PR gate. The command exits non-zero if any ERROR is found; lint WARNINGS are\n" +
			"advisory and never fail.\n\n" +
			"EvalSet manifests are validated as a carried, format-only unit (design §3.4a):\n" +
			"they are NOT imported into the registry or run in P2.\n\n" +
			"--dry-run additionally issues one live materialize+call per template to\n" +
			"confirm the autorater API accepts it (needs credentials + a configured\n" +
			"project); it runs only after steps 1–5 pass.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := registry.ValidatePack(args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			renderValidateReport(out, report)

			if dryRun {
				if report.HasErrors() {
					fmt.Fprintln(out, "\n--dry-run skipped: fix the errors above first (steps 1–5 must pass).")
				} else if err := runPackDryRun(cmd, report); err != nil {
					return err
				}
			}

			if report.HasErrors() {
				return fmt.Errorf("pack validate: %d error(s) found", report.Errors())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "after steps 1–5 pass, issue one live materialize+call per template to confirm API acceptance (needs credentials)")
	return cmd
}

// renderValidateReport prints findings grouped by file, then a summary line.
func renderValidateReport(w io.Writer, report *registry.Report) {
	byFile := map[string][]registry.Finding{}
	var files []string
	for _, f := range report.Findings {
		if _, seen := byFile[f.File]; !seen {
			files = append(files, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	sort.Strings(files)
	for _, file := range files {
		fmt.Fprintf(w, "%s:\n", file)
		for _, f := range byFile[file] {
			marker := "ERROR"
			if f.Severity == registry.SeverityWarning {
				marker = "warn "
			}
			fmt.Fprintf(w, "  [%s] %s\n", marker, f.Message)
		}
	}
	if len(report.Findings) == 0 {
		fmt.Fprintln(w, "OK: no defects found.")
	}
	fmt.Fprintf(w, "\n%d error(s), %d warning(s)\n", report.Errors(), report.Warnings())
}

// runPackDryRun performs step 6: for each structurally-valid template, build a
// live eval engine and issue one materialize+call to confirm the autorater API
// accepts the template's shape. Templates whose required inputs include a
// non-text modality are SKIPPED (a synthetic asset cannot be fabricated for a
// live probe); their text-only siblings are probed with a minimal synthetic
// instance. This is opt-in and needs credentials + a configured project.
func runPackDryRun(cmd *cobra.Command, report *registry.Report) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "\n--dry-run: live API acceptance probe")

	cfg, err := mustConfig()
	if err != nil {
		return err
	}
	if err := requireProject(cfg); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()

	engine, closeEngine, err := wire.NewEngine(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closeEngine() }()

	var probed, skipped, failed int
	for _, t := range report.Templates {
		inst, ok := syntheticInstance(t)
		if !ok {
			skipped++
			fmt.Fprintf(out, "  [skip ] %s (has a non-text input; live probe needs a real asset)\n", t.ID)
			continue
		}
		if _, err := engine.Run(ctx, t, inst); err != nil {
			failed++
			fmt.Fprintf(out, "  [FAIL ] %s: %v\n", t.ID, err)
			continue
		}
		probed++
		fmt.Fprintf(out, "  [ok   ] %s\n", t.ID)
	}
	fmt.Fprintf(out, "  probed %d, skipped %d, failed %d\n", probed, skipped, failed)
	if failed > 0 {
		return fmt.Errorf("pack validate --dry-run: %d template(s) rejected by the API", failed)
	}
	return nil
}

// syntheticInstance builds a minimal text-only Instance for the --dry-run probe.
// It returns ok=false when any declared input is a non-text modality (which
// would need a real staged asset the probe cannot fabricate).
func syntheticInstance(t registry.MetricTemplate) (eval.Instance, bool) {
	inst := eval.Instance{Fields: map[string]eval.AssetRef{}}
	for _, in := range t.Inputs {
		if in.Modality != "" && in.Modality != registry.ModalityText {
			return eval.Instance{}, false
		}
		inst.Fields[in.Name] = eval.AssetRef{
			Modality: registry.ModalityText,
			Text:     "sample text for validation dry-run",
		}
	}
	return inst, true
}

// newPackInitCmd wires `pack init <dir> --name <namespace>`. It scaffolds an
// empty, valid pack directory: a mizan-pack.yaml manifest (metadata.name =
// namespace), an empty templates/ dir, and an empty evalsets/ dir (the §3.4a
// EvalSet carriage hook). It emits NO CI workflow — the validate-packs workflow
// lives once in the mizan-templates repo, not in every scaffolded pack (design
// §6/P2.4). It goes through registry.Service (never the codec/YAML packages), so
// the cmd seam holds.
func newPackInitCmd() *cobra.Command {
	var namespace string
	cmd := &cobra.Command{
		Use:   "init <dir>",
		Short: "Scaffold a new pack directory (manifest + empty templates/ and evalsets/)",
		Long: "Scaffold a new template pack at <dir>: a mizan-pack.yaml manifest whose\n" +
			"metadata.name is the namespace, an empty templates/ directory, and an empty\n" +
			"evalsets/ directory (the EvalSet carriage hook). No CI workflow is emitted —\n" +
			"the validate-packs workflow lives in the mizan-templates repo.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if namespace == "" {
				return fmt.Errorf("--name is required (the pack namespace, e.g. google-brand)")
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

			if err := svc.InitPack(cmd.Context(), args[0], namespace); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "initialized pack %q (namespace %q)\n", args[0], namespace)
			return nil
		},
	}
	cmd.Flags().StringVar(&namespace, "name", "", "pack namespace (metadata.name; lowercase letters, digits, hyphens) (required)")
	return cmd
}

// newPackAddCmd wires `pack add <dir> --from <template-id>`. It is a thin
// convenience over export: it writes the one local template with the given id
// into the pack dir as a schema-valid file (design §3.7). It reuses
// Service.Export with a single-id selector, so the namespaced-id enforcement and
// responseSchema canonicalization on write apply identically.
func newPackAddCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "add <dir>",
		Short: "Write one local template into a pack dir (from the registry)",
		Long: "Write the local template <id> into the pack at <dir> as a schema-valid\n" +
			"template file (a thin convenience over `registry export --id`).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				return fmt.Errorf("--from is required (the template id to add, e.g. google-brand/video-brand-alignment)")
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

			report, err := svc.Export(cmd.Context(), args[0], registry.Selector{ID: from})
			if err != nil {
				return err
			}
			return renderExportReport(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "template id to add to the pack (required)")
	return cmd
}

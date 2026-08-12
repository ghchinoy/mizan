package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// projectOverride is the value of the per-invocation --project flag (FEAT-PROJECT).
// It is a PERSISTENT flag on the eval command, so it applies to both `eval run`
// and `eval pairwise` (mirroring how --model sits atop the model chain) WITHOUT
// polluting the unrelated registry/config command trees the way a root-level
// persistent flag would. Empty means "no override" — the env/.env/default
// precedence LoadConfig resolved is left intact.
var projectOverride string

// newEvalCmd wires the `mizan eval` command family.
func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "eval",
		Short:   "Run metric templates against assets",
		GroupID: groupEval,
	}
	// --project overrides the resolved GCP project for a single run at the TOP of
	// the precedence chain: flag > exported env (MIZAN_PROJECT_ID/PROJECT_ID) >
	// .env > default. Persistent so `eval run` and `eval pairwise` both inherit it.
	cmd.PersistentFlags().StringVar(&projectOverride, "project", "",
		"override the GCP project for this run (highest precedence: flag > env > .env > default)")
	cmd.AddCommand(newEvalRunCmd())
	cmd.AddCommand(newEvalPairwiseCmd())
	// eval batch (P3) is intentionally not wired in this slice.
	return cmd
}

// applyProjectOverride applies the per-invocation --project flag (FEAT-PROJECT)
// at the TOP of the project precedence chain:
//
//	flag > exported env (MIZAN_PROJECT_ID/PROJECT_ID) > .env > default
//
// LoadConfig has already resolved env/.env/default into cfg.ProjectID; when the
// flag is non-empty we replace that value AND re-attribute its source to `flag`
// so the pre-flight echo shows src=flag — reusing the FIX-CONFIG source-hint
// mechanism (config.Source / cfg.Sources) rather than inventing a parallel path.
// An empty flag leaves cfg untouched, so the env/.env/default precedence is
// preserved exactly (absent-flag = unchanged behavior).
func applyProjectOverride(cfg *config.Config, project string) {
	if project == "" {
		return
	}
	cfg.ProjectID = project
	if cfg.Sources == nil {
		cfg.Sources = map[string]config.Source{}
	}
	cfg.Sources["project-id"] = config.SourceFlag
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
		Use: "run --metric <id> [--field key=value] [--file key=/path] [--gcs key=gs://…]",
		// `single` is the plain-vernacular alias: `mizan eval single` == `mizan eval
		// run` — score ONE response (a.k.a. pointwise). run is also the entry for
		// rubric/custom_schema templates, dispatched by the template's kind.
		Aliases: []string{"single"},
		Short:   "single (a.k.a. pointwise) — score one response (also rubric/custom_schema) against a live eval call",
		Long: "Run a metric template against an instance.\n\n" +
			"Also available as `mizan eval single` — score ONE response (a.k.a.\n" +
			"pointwise). The compare/pairwise counterpart is `mizan eval compare`.\n\n" +
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
			// Validate the per-invocation --project flag BEFORE it overrides the
			// resolved project, is echoed to stderr, or reaches Vertex — so a
			// malformed value fails locally with a crisp error instead of an opaque
			// server-side InvalidArgument (mirrors the local --model guard below).
			if err := config.ValidateProjectID(projectOverride); err != nil {
				return err
			}
			// A per-invocation --project flag overrides the resolved project for
			// this run (flag > env > .env > default). Apply BEFORE the engine is
			// opened so the override reaches both the pre-flight echo and the call.
			applyProjectOverride(cfg, projectOverride)

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
			// structured-output path for per-criterion transparency. When the user
			// explicitly sets --rubric-scale, parse it locally (so a malformed value
			// fails before any call) and pass it as the highest-precedence scale.
			// Otherwise defer the scale to the template's declared rubricDetail.scale
			// (if any) or the engine default (1-5) — see Engine.resolveRubricScale
			// (H2, RFC-0001 §4.4).
			runOpts := []eval.RunOption{eval.WithModel(model)}
			if rubricDetail {
				if cmd.Flags().Changed("rubric-scale") {
					min, max, err := eval.ParseRubricScale(rubricScale)
					if err != nil {
						return err
					}
					runOpts = append(runOpts, eval.WithRubricDetail(min, max))
				} else {
					runOpts = append(runOpts, eval.WithRubricDetailDefaultScale())
				}
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
		Use: "pairwise --metric <id> (--baseline key=… --candidate key=… | --gcs key=gs://… …) [--field/--file/--gcs …]",
		// `compare` is the plain-vernacular alias: `mizan eval compare` == `mizan
		// eval pairwise` — compare TWO responses and pick the better (a.k.a.
		// pairwise). The single/pointwise counterpart is `mizan eval single`.
		Aliases: []string{"compare"},
		Short:   "compare (a.k.a. pairwise) — compare two responses (baseline vs candidate) and pick the better",
		Long: "Run a pairwise metric template. Also available as `mizan eval compare`\n" +
			"— compare TWO responses (a.k.a. pairwise). The single/pointwise\n" +
			"counterpart is `mizan eval single`.\n\n" +
			"Fill the baseline and candidate slots (keyed by the template's\n" +
			"baseline/candidate field names) from ANY source:\n" +
			"  --baseline key=value / --candidate key=value   TEXT responses\n" +
			"  --gcs  key=gs://…                               a pre-staged media asset\n" +
			"  --file key=/path                                a local media asset\n\n" +
			"A media pairwise (compare two videos/images/audio) is expressed by\n" +
			"supplying the baseline/candidate fields via --gcs/--file, e.g.\n" +
			"  mizan eval pairwise --metric M --gcs base=gs://a.mp4 --gcs cand=gs://b.mp4\n\n" +
			"A gs:// URI or local media path passed to a TEXT slot (--baseline/\n" +
			"--candidate/--field) is a hard error — use --gcs/--file so the judge\n" +
			"actually sees the media. Additional placeholders use --field/--file/--gcs.\n" +
			"Prints the PairwiseChoice (BASELINE/CANDIDATE/TIE) and explanation.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if metric == "" {
				return fmt.Errorf("--metric is required")
			}
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			// Validate the per-invocation --project flag BEFORE it overrides the
			// resolved project, is echoed to stderr, or reaches Vertex (see eval run).
			if err := config.ValidateProjectID(projectOverride); err != nil {
				return err
			}
			// A per-invocation --project flag overrides the resolved project for
			// this run (flag > env > .env > default). Apply BEFORE the engine is
			// opened so the override reaches both the pre-flight echo and the call.
			applyProjectOverride(cfg, projectOverride)

			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			tmpl, err := svc.Get(cmd.Context(), metric)
			if err != nil {
				return err
			}

			// The baseline/candidate responses are TEXT fields keyed by the
			// template's field names. Both flags are OPTIONAL: the baseline/
			// candidate slot may instead be filled by --gcs/--file so a MEDIA
			// pairwise (compare two videos/images/audio) is expressible. Guard the
			// text values against a mis-routed media reference (gs:// URI or local
			// media path) BEFORE they are sent verbatim to the judge (GAP B footgun),
			// then fold them into the field list handed to buildInstance.
			textFields := make([]string, 0, len(fields)+2)
			if baseline != "" {
				if k, v, ok := strings.Cut(baseline, "="); ok {
					if err := guardTextSlot("baseline", k, v); err != nil {
						return err
					}
				}
				textFields = append(textFields, baseline)
			}
			if candidate != "" {
				if k, v, ok := strings.Cut(candidate, "="); ok {
					if err := guardTextSlot("candidate", k, v); err != nil {
						return err
					}
				}
				textFields = append(textFields, candidate)
			}
			textFields = append(textFields, fields...)
			inst, err := buildInstance(textFields, files, gcs)
			if err != nil {
				return err
			}

			// The baseline/candidate requirement is satisfied when the template's
			// baseline and candidate field names are present in the instance from ANY
			// source (text --baseline/--candidate OR media --gcs/--file). This is
			// checked AFTER the template is fetched so the required keys are known,
			// replacing the old "--baseline and --candidate are required" text-only
			// gate that made media pairwise inexpressible.
			if err := requirePairwiseFields(*tmpl, inst); err != nil {
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
	cut := func(raw, flag string) (string, string, error) {
		k, v, ok := strings.Cut(raw, "=")
		if !ok || k == "" {
			return "", "", fmt.Errorf("invalid --%s %q (want key=value)", flag, raw)
		}
		if _, dup := inst.Fields[k]; dup {
			return "", "", fmt.Errorf("duplicate field key %q", k)
		}
		return k, v, nil
	}
	for _, f := range fields {
		k, v, err := cut(f, "field")
		if err != nil {
			return eval.Instance{}, err
		}
		// --field is a TEXT slot: reject a value that is really a media reference
		// (gs:// URI or local media path) instead of sending it verbatim to the
		// judge, which would confabulate from the string (GAP B footgun).
		if err := guardTextSlot("field", k, v); err != nil {
			return eval.Instance{}, err
		}
		inst.Fields[k] = eval.AssetRef{Modality: registry.ModalityText, Text: v}
	}
	for _, f := range files {
		k, v, err := cut(f, "file")
		if err != nil {
			return eval.Instance{}, err
		}
		inst.Fields[k] = eval.AssetRef{FilePath: v}
	}
	for _, f := range gcs {
		k, v, err := cut(f, "gcs")
		if err != nil {
			return eval.Instance{}, err
		}
		inst.Fields[k] = eval.AssetRef{GCSUri: v}
	}
	return inst, nil
}

// mediaExtensions is the conservative set of file extensions treated as media
// when guarding a TEXT field slot against a mis-routed LOCAL media path. Kept in
// sync in spirit with the engine's asset MIME detection, but intentionally small
// and explicit so the false-positive risk on ordinary prose is auditable.
var mediaExtensions = map[string]bool{
	// video
	".mp4": true, ".mov": true, ".webm": true, ".mkv": true, ".avi": true, ".m4v": true,
	".mpeg": true, ".mpg": true, ".3gp": true, ".wmv": true, ".flv": true,
	// image
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true,
	".tif": true, ".tiff": true, ".heic": true, ".heif": true,
	// audio
	".mp3": true, ".wav": true, ".m4a": true, ".aac": true, ".flac": true, ".ogg": true, ".opus": true,
}

// guardTextSlot rejects a value passed to a TEXT field slot (--field, --baseline,
// --candidate) that is almost certainly a media asset mis-routed as text, turning
// the silent GAP B footgun (a gs:// URI sent verbatim, the judge confabulating a
// comparison from the filename, exit 0) into a hard error. Two arms:
//
//  1. gs:// prefix — ALWAYS an error. A gs:// URI in a text slot is never
//     legitimate text for these fields; sent as text the judge sees only the
//     string. The caller must use --gcs KEY=gs://… to evaluate it as media.
//  2. local media path — a value that is an EXISTING local regular file whose
//     extension is a known media type (mediaExtensions). Requiring the value to
//     actually stat as a file on disk is the conservative guard against prose
//     false positives: ordinary text that merely ends in ".png" (or mentions a
//     filename) is not a stat-able regular file, so it passes untouched. The
//     caller must use --file KEY=/path to evaluate it as media.
//
// gs:// is the load-bearing case; the local-path arm is defense-in-depth and
// deliberately errs toward NOT blocking legitimate text.
func guardTextSlot(flag, key, value string) error {
	v := strings.TrimSpace(value)
	if strings.HasPrefix(v, "gs://") {
		return fmt.Errorf("--%s %s=<value> looks like a gs:// media URI passed as TEXT; a gs:// URI in a text slot is sent verbatim to the judge (which never sees the media). Use --gcs %s=%s to evaluate it as a media asset", flag, key, key, v)
	}
	if isLocalMediaPath(v) {
		return fmt.Errorf("--%s %s=<value> looks like a local media file (%q exists and has a media extension) passed as TEXT; it would be sent as the literal path string, not read as media. Use --file %s=%s to evaluate it as a media asset", flag, key, v, key, v)
	}
	return nil
}

// isLocalMediaPath reports whether v is an existing local regular file with a
// known media extension. Both conditions are required so ordinary prose does not
// trip the guard (see guardTextSlot).
func isLocalMediaPath(v string) bool {
	if v == "" {
		return false
	}
	if !mediaExtensions[strings.ToLower(filepath.Ext(v))] {
		return false
	}
	info, err := os.Stat(v)
	return err == nil && !info.IsDir()
}

// requirePairwiseFields enforces that the template's baseline and candidate field
// names are present in the instance, satisfied by ANY source (text --baseline/
// --candidate OR media --gcs/--file). It runs AFTER the template is fetched, so
// the required keys are the template's actual field names — this both enables a
// media pairwise (baseline/candidate supplied via --gcs/--file) and gives a clear
// error naming the missing slot. Empty field names are left to the engine's own
// "must set BaselineFieldName and CandidateFieldName" guard (malformed template).
func requirePairwiseFields(tmpl registry.MetricTemplate, inst eval.Instance) error {
	var missing []string
	if tmpl.BaselineFieldName != "" {
		if _, ok := inst.Fields[tmpl.BaselineFieldName]; !ok {
			missing = append(missing, fmt.Sprintf("baseline field %q", tmpl.BaselineFieldName))
		}
	}
	if tmpl.CandidateFieldName != "" {
		if _, ok := inst.Fields[tmpl.CandidateFieldName]; !ok {
			missing = append(missing, fmt.Sprintf("candidate field %q", tmpl.CandidateFieldName))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("pairwise requires the %s; supply text with --baseline/--candidate KEY=value, or media with --gcs KEY=gs://… / --file KEY=/path (KEY must match the template's field name)", strings.Join(missing, " and "))
	}
	return nil
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
// config source (env/env-file/default, or flag when a --project override is in
// effect); when a fully-qualified --model resource or a global path overrides
// them, that override is named instead so the echo never claims a config source
// it did not actually use. The location override tokens are distinct on purpose:
//   - global-path: the genai/custom_schema (or --rubric-detail) path is global
//     by design.
//   - global-route: the NATIVE path was auto-routed to the global host because
//     the resolved model is a known global-only judge (isGlobalOnlyModel).
//   - model: a fully-qualified "projects/.../locations/<loc>/..." model resource
//     carried its own location — REGIONAL or global. The resource-vs-routing
//     provenance for a "global" native location cannot be read from the location
//     string alone (both surface "global"), so it is threaded from the resolver
//     via ResolvedTarget.LocationFromModelResource (review OPTIONAL: a resource
//     explicitly pinned to global is src=model, not src=global-route).
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
	case t.LocationFromModelResource:
		// A fully-qualified model resource carried its own location. This is checked
		// BEFORE the global-route fallback so a resource explicitly pinned to
		// "global" reads src=model rather than being mislabeled as routing-forced
		// (the resource-derived-global vs routing-forced-global distinction).
		locSrc = "model"
	case t.Location == eval.GenaiLocation:
		// FIX-SRC: on the NATIVE path a "global" location NOT carried by a
		// fully-qualified model resource is forced by global-only ROUTING
		// (isGlobalOnlyModel auto-routes a known global-only judge to the global
		// host; route.go). Label it accurately so the echo never claims the
		// location came from the model. eval.GenaiLocation is the single global-
		// location const (it aliases the R-GLOBAL host location in route.go), so
		// this matches the exact string Engine.Resolve writes for that case.
		locSrc = "global-route"
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

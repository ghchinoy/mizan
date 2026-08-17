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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/rubricgen"
	"github.com/ghchinoy/mizan/internal/wire"
)

// knownRecipes is the curated allow-list of predefined generation recipes
// confirmed live in-project (rubric-generation-mechanics-research.md §5, probed
// against ghchinoy-genai-sa). It is the SINGLE source of truth for --recipe
// validation on both `rubric generate` (CUJ 7) and `eval adaptive` (CUJ 8), which
// share validateRecipe. Ordered for a stable help/error listing; the first entry
// is the default. Recipe availability can drift per project/date (general_quality_v2
// returned 400 live), so the escape hatch MIZAN_ALLOW_CUSTOM_RECIPE=1 relaxes this
// list to the bare-token regex for power users ahead of an mizan update.
var knownRecipes = []string{
	"general_quality_v1",       // default; prompt-aligned, encodes bound tokens in type codes
	"instruction_following_v1", // most prompt-alignment-focused (Gecko/DSG decomposition)
	"text_quality_v1",          // more holistic / generic quality dimensions
}

// defaultRecipe is the pinned predefined generation recipe exposed via --recipe.
// It is the first curated recipe (general_quality_v1): proven live in-project,
// while general_quality_v2 returned 400 (see the live probe).
var defaultRecipe = knownRecipes[0]

// draftTemplateVersion is the semver stamped on a generated draft so it validates
// under the strict pack schema and round-trips unchanged when the draft is wrapped
// in a pack and brought in via `mizan registry import <pack>`.
const draftTemplateVersion = "0.1.0"

// Adaptive-generation provenance constants (design §4.4/§4.7). These stamp HOW a
// generated rubric was drafted so a consumer can always tell an AI-drafted rubric
// from a hand-authored one and reproduce/audit the draft.
const (
	// adaptiveMethod is the RubricProvenance.Method value for adaptive generation.
	// CROSS-TEAM CONTRACT: mizan-em-resultsstore reads RubricRef.Method.
	adaptiveMethod = "adaptive-generated"
	// adaptiveAPIVersion identifies the RPC/surface that produced the rubric.
	adaptiveAPIVersion = "v1beta1:generateInstanceRubrics"
	// adaptiveGeneratorModel is the builtin generator model the predefined-recipe
	// path uses (design §4.2: default builtin gemini-2.5-flash). Phase 2 supports
	// the predefined path only; a custom generator model rides a later phase.
	adaptiveGeneratorModel = "gemini-2.5-flash"
	// maxSampleRefPreview bounds the sample-input preview stored in provenance so
	// the persisted YAML never carries an unbounded prompt blob (security): the
	// full input is pinned by its SHA-256, only a capped preview is human-readable.
	maxSampleRefPreview = 256
)

// sampleInputRef builds a BOUNDED, auditable reference to the sample input that
// drove generation: a length-capped, single-line preview plus the SHA-256 of the
// FULL sample. It deliberately does NOT dump the (possibly large,
// attacker-influenceable) sample verbatim into persisted, hashed YAML — the hash
// pins the exact input for reproducibility while the preview stays small and safe.
func sampleInputRef(sample string) string {
	sum := sha256.Sum256([]byte(sample))
	return fmt.Sprintf("inline:%q sha256:%s", previewText(sample, maxSampleRefPreview), hex.EncodeToString(sum[:]))
}

// previewText collapses whitespace runs to single spaces (so the preview is
// single-line) and rune-safely truncates to maxLen, appending an ellipsis when cut.
func previewText(s string, maxLen int) string {
	joined := strings.Join(strings.Fields(s), " ")
	r := []rune(joined)
	if len(r) > maxLen {
		return string(r[:maxLen]) + "…"
	}
	return joined
}

// rubricMetaFor preserves the API's per-criterion type/importance (Decision 2)
// alongside the flat RubricGroups, keyed by group+criterion in DECLARED order. It
// reads from the SAME rubricgen.UsableRubrics filter that backs ToRubricGroups, so
// RubricMeta stays aligned 1:1 with the criteria that actually landed in
// RubricGroups (no re-implemented filter that could drift).
func rubricMetaFor(groupName string, rubrics []rubricgen.Rubric) []registry.RubricMeta {
	usable := rubricgen.UsableRubrics(rubrics)
	meta := make([]registry.RubricMeta, 0, len(usable))
	for _, u := range usable {
		meta = append(meta, registry.RubricMeta{
			Group:      groupName,
			Criterion:  u.Criterion,
			Type:       u.Rubric.Type,
			Importance: u.Rubric.Importance,
		})
	}
	return meta
}

// buildRubricProvenance assembles the fully-populated RubricProvenance stamped on
// BOTH `rubric generate` (CUJ 7) drafts and `eval adaptive --save-as` (CUJ 8)
// frozen templates. Keeping it in one place is the owner's no-duplication
// constraint and guarantees the two commands stamp identical provenance for the
// rubrics actually used. PromptTemplate is left empty here (the custom
// generation-prompt path is a later phase).
func buildRubricProvenance(recipe, groupName, sample string, rubrics []rubricgen.Rubric) *registry.RubricProvenance {
	return &registry.RubricProvenance{
		Method:         adaptiveMethod,
		GeneratorModel: adaptiveGeneratorModel,
		Recipe:         recipe,
		SampleInputRef: sampleInputRef(sample),
		GeneratedAt:    time.Now().UTC(),
		APIVersion:     adaptiveAPIVersion,
		RubricMeta:     rubricMetaFor(groupName, rubrics),
	}
}

// newRubricGenerator is the composition-root seam for the Stage-1 generation
// client. It is a package-level var ONLY so command tests can substitute a fake
// (rubricgentest.FakeClient) and exercise `rubric generate` / `eval adaptive`
// without a live call or ADC. Production always uses wire.NewRubricGenerator.
var newRubricGenerator = wire.NewRubricGenerator

// recipePattern is a conservative structural allow-list for the --recipe token,
// which is placed into the request body's metric_spec_name. It keeps a
// hostile/garbled value from smuggling structure into the request. It is no longer
// the primary gate (knownRecipes is), but backs the MIZAN_ALLOW_CUSTOM_RECIPE=1
// escape hatch so an unlisted-but-well-formed recipe can still be passed.
var recipePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]*$`)

// allowCustomRecipeEnv is the escape-hatch env var (matching the house
// MIZAN_ALLOW_CUSTOM_ENDPOINT idiom): when set to "1", validateRecipe relaxes the
// curated allow-list to the bare-token regex so a power user can pass an unlisted
// recipe (e.g. a new version Vertex ships before mizan curates it).
const allowCustomRecipeEnv = "MIZAN_ALLOW_CUSTOM_RECIPE"

// groupNamePattern is a conservative allow-list for a rubric group name: letters,
// digits, spaces, and the separators '_' '-' '.'. It becomes a YAML map key and a
// heading in the judge prompt, so control characters and structural bytes are
// rejected up front.
var groupNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]*$`)

// recipeVersionSuffix strips a trailing "_v<digits>" so the default group name is
// the recipe's family name (general_quality_v1 -> general_quality).
var recipeVersionSuffix = regexp.MustCompile(`_v[0-9]+$`)

// validateRecipe accepts only a member of the curated knownRecipes allow-list so a
// recipe the API cannot serve (a typo, a not-yet-enabled _v2, an unsupported family)
// fails locally instead of after a billable 400 round-trip. As an escape hatch, when
// MIZAN_ALLOW_CUSTOM_RECIPE=1 is set an unlisted recipe is admitted if it still
// passes the conservative bare-token structural check (recipePattern), keeping power
// users unblocked against API drift without smuggling structure into the request.
func validateRecipe(recipe string) error {
	for _, r := range knownRecipes {
		if recipe == r {
			return nil
		}
	}
	if os.Getenv(allowCustomRecipeEnv) == "1" {
		if !recipePattern.MatchString(recipe) {
			return fmt.Errorf("invalid --recipe %q: expected a bare recipe token (lowercase letters, digits, '_') when %s=1", recipe, allowCustomRecipeEnv)
		}
		return nil
	}
	return fmt.Errorf("invalid --recipe %q: choose one of %s (set %s=1 to pass an unlisted recipe)", recipe, strings.Join(knownRecipes, ", "), allowCustomRecipeEnv)
}

// validateGroupName rejects an empty or unsafe rubric group name.
func validateGroupName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("--group-name must not be empty")
	}
	if !groupNamePattern.MatchString(name) {
		return fmt.Errorf("invalid --group-name %q: use letters, digits, spaces, and '_' '-' '.'", name)
	}
	return nil
}

// defaultRubricGroupName derives the default RubricGroups key from the recipe by
// dropping any trailing version suffix (general_quality_v1 -> general_quality).
func defaultRubricGroupName(recipe string) string {
	return recipeVersionSuffix.ReplaceAllString(recipe, "")
}

// recipeFlagUsage is the shared --recipe help string for both `rubric generate`
// and `eval adaptive`, listing the curated values so the enum is discoverable via
// `-h` (design §4.6 step 3). cobra/pflag appends the default value automatically
// from DefValue, so it is not repeated here.
func recipeFlagUsage() string {
	return fmt.Sprintf("predefined generation recipe; one of %s", strings.Join(knownRecipes, ", "))
}

// adaptiveMetricPrompt builds the metricPromptTemplate for a generated rubric
// template. It references every instance field key as a {{placeholder}} in stable
// (sorted) order, so the template's placeholder set exactly matches the fields the
// user supplies at eval time (satisfying the engine's symmetric field/placeholder
// parity check) and the rubric criteria (appended by the engine's runRubric) drive
// the score. The result is deterministic for a given key set.
func adaptiveMetricPrompt(keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	var b strings.Builder
	b.WriteString("You are an impartial evaluator. Assess the response below against the rubric criteria that follow.")
	for _, k := range sorted {
		b.WriteString("\n\n")
		b.WriteString(k)
		b.WriteString(":\n{{")
		b.WriteString(k)
		b.WriteString("}}")
	}
	return b.String()
}

// generateRubricGroups is the SINGLE shared generation+conversion path used by
// BOTH `rubric generate` (CUJ 7) and `eval adaptive` (CUJ 8): call the Stage-1
// client, convert the returned []Rubric into a mizan RubricGroups map, and fail
// clearly when no usable criteria came back. Keeping this in one place is the
// owner's explicit no-duplication constraint.
func generateRubricGroups(ctx context.Context, gen rubricgen.Client, sample, recipe, groupName string) (map[string][]string, []rubricgen.Rubric, error) {
	rubrics, err := gen.Generate(ctx, rubricgen.TextContents(sample), rubricgen.Spec{PredefinedMetric: recipe})
	if err != nil {
		return nil, nil, err
	}
	groups := rubricgen.ToRubricGroups(rubrics, groupName)
	if len(groups[groupName]) == 0 {
		return nil, nil, fmt.Errorf("no usable rubric criteria were generated (the API returned %d rubric(s), none with a criterion description)", len(rubrics))
	}
	return groups, rubrics, nil
}

// renderRubricCriteria prints the generated criteria: a GROUP/CRITERION/TYPE/
// IMPORTANCE table (text) or the raw rubrics (json). Every cell is sanitized —
// the criteria are model-generated (untrusted) text.
func renderRubricCriteria(w io.Writer, groupName string, rubrics []rubricgen.Rubric) error {
	if outputFormat == outputJSON {
		return printJSON(w, rubrics)
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "GROUP\tCRITERION\tTYPE\tIMPORTANCE")
	for _, r := range rubrics {
		desc := strings.TrimSpace(r.Content.Property.Description)
		if desc == "" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			sanitizeCell(groupName), sanitizeCell(desc),
			sanitizeCell(r.Type), sanitizeCell(r.Importance))
	}
	return tw.Flush()
}

// draftRubricTemplate builds the frozen KindRubric template Mizan writes/saves for
// a generated rubric. The instance field keys drive both the metricPromptTemplate
// placeholders and the declared inputs. When prov is non-nil it stamps the
// adaptive-generation provenance (Phase 2, design §4.4) — a persisted, hashed,
// schema-valid field — so the draft records how it was AI-drafted; a nil prov
// leaves the template hand-authored-equivalent. The result validates under the
// strict schema and round-trips unchanged when wrapped in a pack and brought in
// via `mizan registry import <pack>`.
func draftRubricTemplate(id, name, groupName string, fieldKeys []string, groups map[string][]string, prov *registry.RubricProvenance) registry.MetricTemplate {
	sorted := append([]string(nil), fieldKeys...)
	sort.Strings(sorted)
	inputs := make([]registry.InputSpec, 0, len(sorted))
	for _, k := range sorted {
		inputs = append(inputs, registry.InputSpec{Name: k, Modality: registry.ModalityText, Required: true})
	}
	if name == "" {
		name = "Adaptive rubric: " + groupName
	}
	return registry.MetricTemplate{
		ID:                   id,
		Name:                 name,
		Description:          "Adaptive-generated rubric (authoring aid). Review and edit before use; once frozen it is an ordinary reproducible mizan rubric.",
		Version:              draftTemplateVersion,
		Kind:                 registry.KindRubric,
		Modalities:           []registry.Modality{registry.ModalityText},
		Inputs:               inputs,
		MetricPromptTemplate: adaptiveMetricPrompt(sorted),
		RubricGroups:         groups,
		RubricProvenance:     prov,
	}
}

// newRubricCmd wires the `mizan rubric` command family (adaptive-rubric authoring).
func newRubricCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rubric",
		Short:   "Author rubric templates (adaptive generation authoring aid)",
		GroupID: groupRubric,
	}
	cmd.AddCommand(newRubricGenerateCmd())
	return cmd
}

// newRubricGenerateCmd wires `rubric generate` (CUJ 7): draft a KindRubric
// template from a sample prompt via the Stage-1 generation RPC, print the
// criteria, and WRITE a draft YAML. It writes NOTHING to the registry. To bring a
// reviewed draft into the registry, wrap it under a pack's templates/ dir and
// `mizan registry import <pack>` (a loose draft file is NOT a valid import source,
// and `registry create` has no whole-file input); or skip the draft entirely and
// freeze in one step with `mizan eval adaptive --save-as <id>`. Adaptive
// generation is an AUTHORING AID; the frozen draft is an ordinary reproducible
// static mizan rubric.
func newRubricGenerateCmd() *cobra.Command {
	var (
		sample    string
		recipe    string
		groupName string
		id        string
		name      string
		out       string
		project   string
		location  string
	)
	cmd := &cobra.Command{
		Use:   "generate --sample <prompt> --id <ns/slug> --out <draft.yaml>",
		Short: "Draft a rubric template from a sample prompt (writes a draft; does NOT touch the registry)",
		Long: "Draft a KindRubric template from a sample prompt using Vertex AI adaptive\n" +
			"rubric generation, then write a draft YAML for review.\n\n" +
			"Adaptive generation is an AUTHORING AID: Gemini drafts the criteria, you\n" +
			"review/edit/freeze them, and from that moment the template is an ordinary\n" +
			"reproducible static mizan rubric. This command writes NOTHING to the\n" +
			"registry.\n\n" +
			"To bring a reviewed draft into the registry, wrap it in a pack and import\n" +
			"the pack — `registry import` reads a pack tree (a mizan-pack.yaml manifest\n" +
			"plus a templates/ directory), not a loose template file, and `registry\n" +
			"create` has no whole-file input:\n\n" +
			"  mizan pack init packs/acme --name acme\n" +
			"  cp <draft>.yaml packs/acme/templates/<slug>.yaml   # after review/edit\n" +
			"  mizan registry import packs/acme\n" +
			"  mizan eval run --metric <id> --field prompt=… --field response=…\n\n" +
			"Or skip the draft file entirely and freeze the generated rubric straight\n" +
			"into the registry in one step:\n\n" +
			"  mizan eval adaptive --prompt … --response … --save-as <id>\n" +
			"  mizan eval run --metric <id> --field prompt=… --field response=…",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(sample) == "" {
				return fmt.Errorf("--sample is required")
			}
			if out == "" {
				return fmt.Errorf("--out is required (path to write the draft template YAML)")
			}
			if err := registry.ValidateTemplateID(id); err != nil {
				return err
			}
			if err := validateRecipe(recipe); err != nil {
				return err
			}
			if groupName == "" {
				groupName = defaultRubricGroupName(recipe)
			}
			if err := validateGroupName(groupName); err != nil {
				return err
			}

			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			if err := applyRubricTarget(cfg, project, location); err != nil {
				return err
			}

			gen, err := newRubricGenerator(cmd.Context(), cfg)
			if err != nil {
				return err
			}

			groups, rubrics, err := generateRubricGroups(cmd.Context(), gen, sample, recipe, groupName)
			if err != nil {
				return err
			}

			prov := buildRubricProvenance(recipe, groupName, sample, rubrics)
			tmpl := draftRubricTemplate(id, name, groupName, []string{"prompt", "response"}, groups, prov)
			yamlBytes, err := registry.MarshalTemplate(&tmpl)
			if err != nil {
				return err
			}
			if err := writeDraft(out, yamlBytes); err != nil {
				return err
			}

			if err := renderRubricCriteria(cmd.OutOrStdout(), groupName, rubrics); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(),
				"mizan: wrote draft template %s to %s — review/edit, then wrap it under a pack's templates/ dir and `mizan registry import <pack>` (a loose draft file is not a valid import source), then `mizan eval run --metric %s` (or freeze in one step with `mizan eval adaptive --save-as %s`)\n",
				id, out, id, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&sample, "sample", "", "sample prompt to generate rubric criteria from (required)")
	cmd.Flags().StringVar(&recipe, "recipe", defaultRecipe, recipeFlagUsage())
	cmd.Flags().StringVar(&groupName, "group-name", "", "RubricGroups key for the output (default: the recipe family name)")
	cmd.Flags().StringVar(&id, "id", "", "draft template id, <namespace>/<slug> (required)")
	cmd.Flags().StringVar(&name, "name", "", "draft template human-readable name")
	cmd.Flags().StringVar(&out, "out", "", "path to write the draft template YAML (required)")
	cmd.Flags().StringVar(&project, "project", "", "override the GCP project for generation (flag > env > .env > default)")
	cmd.Flags().StringVar(&location, "location", "", "override the location for generation (default: configured location)")
	return cmd
}

// writeDraft writes the draft template YAML without following a symlink at the
// target path. os.WriteFile would follow (and truncate) a pre-planted symlink,
// letting an attacker who controls a predictable --out path redirect the write;
// O_EXCL|O_NOFOLLOW refuses both an existing file and a symlink so the draft only
// ever lands at a fresh, regular path. Perms stay restrictive (0600). (LOW-2)
func writeDraft(out string, data []byte) error {
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // out is an explicit operator-supplied path; O_EXCL|O_NOFOLLOW hardens it
	if err != nil {
		return fmt.Errorf("write draft %q: %w", out, err)
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return fmt.Errorf("write draft %q: %w", out, werr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("write draft %q: %w", out, cerr)
	}
	return nil
}

// applyRubricTarget validates and applies the per-invocation --project/--location
// overrides used by the rubric-generation commands, at the TOP of the precedence
// chain (flag > env > .env > default). It validates --project and --location with
// the same canonical guards the eval/rubricgen paths use BEFORE they reach Vertex,
// so a hostile value is a crisp local error rather than a redirected bearer token.
func applyRubricTarget(cfg *config.Config, project, location string) error {
	if err := config.ValidateProjectID(project); err != nil {
		return err
	}
	if err := config.ValidateLocation(location); err != nil {
		return err
	}
	applyProjectOverride(cfg, project)
	if location != "" {
		cfg.Location = location
		if cfg.Sources == nil {
			cfg.Sources = map[string]config.Source{}
		}
		cfg.Sources["location"] = config.SourceFlag
	}
	return nil
}

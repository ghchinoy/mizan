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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/wire"
)

// createDefaultVersion is the semver every newly created template starts at when
// --version is omitted. This is a CREATE-TIME default, deliberately NOT a
// config.Fields entry: every new template reasonably starts at 0.1.0, and a
// config-level override would encourage authoring many templates at the same
// non-initial version.
const createDefaultVersion = "0.1.0"

// templateFlags collects the fields a user can set on create/update.
type templateFlags struct {
	id            string
	name          string
	description   string
	kind          string
	prompt        string
	system        string
	model         string
	samplingCount int32
	flipEnabled   bool
	modalities    []string
	tags          []string
	candidate     string
	baseline      string
	// authoring metadata
	version string
	license string
	author  string
	inputs  []string // repeatable "name:modality[:required]" -> spec.inputs
	// inferInputs, when set, scans the (post-apply) prompt/system text for
	// {{name}} placeholders and ADDS an input for each one not already declared
	// and not reserved (placeholder-inference design D1). Opt-in, additive.
	inferInputs bool
	// rubric (KindRubric) authoring
	rubricGroups     []string // repeatable "name=criterion one;criterion two"
	rubricGroupsFile string   // JSON object {"group": ["crit1", ...], ...}
	// custom_schema (KindCustomSchema) authoring
	responseSchema     string // inline JSON-Schema string
	responseSchemaFile string // path to a JSON-Schema file
	// heuristic (KindHeuristic) authoring — non-LLM deterministic checks (§4.B)
	heuristicType            string // contains|regex|equals|json-valid|json-schema-valid
	heuristicTarget          string // input field to check (must be declared in inputs)
	heuristicValue           string // operand for contains/regex/equals
	heuristicCaseInsensitive bool   // fold case (contains/equals; (?i) for regex)
	heuristicSchema          string // inline JSON-Schema string (json-schema-valid)
	heuristicSchemaFile      string // path to a JSON-Schema file (json-schema-valid)
}

func (f *templateFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.id, "id", "", "stable template id, <namespace>/<slug> (required)")
	fl.StringVar(&f.name, "name", "", "human-readable name")
	fl.StringVar(&f.description, "description", "", "description")
	fl.StringVar(&f.kind, "kind", string(registry.KindPointwise), "metric kind: single (a.k.a. pointwise) — score one response | compare (a.k.a. pairwise) — compare two | rubric | custom_schema | heuristic (non-LLM deterministic check)")
	fl.StringVar(&f.prompt, "prompt", "", "metric prompt template ({{var}} placeholders)")
	fl.StringVar(&f.system, "system", "", "system instruction")
	// Default is empty (WI-F3): an unset --model leaves the template's
	// AutoraterModel blank so the eval-time resolution chain
	// (flag > template > config default-model > built-in) applies, instead of
	// baking the built-in id into every created template. The single built-in
	// default lives in eval.BuiltinDefaultModel.
	fl.StringVar(&f.model, "model", "", "autorater model (publisher-relative id; empty = resolve default at eval time)")
	fl.Int32Var(&f.samplingCount, "sampling-count", 4, "autorater sampling count (1-32)")
	fl.BoolVar(&f.flipEnabled, "flip-enabled", true, "pairwise: flip candidate/baseline positions to reduce position bias (default true; the Choice is the authoritative de-biased verdict, but flip can scramble the explanation's baseline/candidate wording — set false to keep the explanation aligned with the presented order)")
	fl.StringSliceVar(&f.modalities, "modality", []string{"text"}, "accepted modalities (repeatable)")
	fl.StringSliceVar(&f.tags, "tag", nil, "tags (repeatable)")
	fl.StringVar(&f.candidate, "candidate-field", "", "pairwise: candidate response field name")
	fl.StringVar(&f.baseline, "baseline-field", "", "pairwise: baseline response field name")
	// StringArrayVar (not StringSliceVar): each flag value is kept intact so a
	// criterion may itself contain commas; criteria are split on ';' below.
	fl.StringArrayVar(&f.rubricGroups, "rubric-group", nil, `rubric: group as "name=criterion one;criterion two" (repeatable; same name accumulates)`)
	fl.StringVar(&f.rubricGroupsFile, "rubric-groups-file", "", `rubric: path to a JSON object file {"group": ["crit1","crit2"], ...}`)
	fl.StringVar(&f.responseSchema, "response-schema", "", "custom_schema: inline JSON-Schema string")
	fl.StringVar(&f.responseSchemaFile, "response-schema-file", "", "custom_schema: path to a JSON-Schema file")
	// heuristic (KindHeuristic): non-LLM, credential-free deterministic checks (§4.B)
	fl.StringVar(&f.heuristicType, "heuristic-type", "", "heuristic: check type — contains | regex | equals | json-valid | json-schema-valid")
	fl.StringVar(&f.heuristicTarget, "heuristic-target", "", "heuristic: input field to check (must be declared via --input)")
	fl.StringVar(&f.heuristicValue, "heuristic-value", "", "heuristic: operand — substring (contains), pattern (regex), or expected text (equals)")
	fl.BoolVar(&f.heuristicCaseInsensitive, "heuristic-case-insensitive", false, "heuristic: fold case for contains/equals (applied as the (?i) flag for regex)")
	fl.StringVar(&f.heuristicSchema, "heuristic-schema", "", "heuristic: inline JSON-Schema string (json-schema-valid)")
	fl.StringVar(&f.heuristicSchemaFile, "heuristic-schema-file", "", "heuristic: path to a JSON-Schema file (json-schema-valid)")
	// Authoring metadata (exposes MetricTemplate fields the model+codec already
	// carry). --version defaults to createDefaultVersion at create time; --author
	// and --license fall back to config (author-name / default-license) when
	// omitted on create.
	fl.StringVar(&f.version, "version", "", fmt.Sprintf("semver version (validated); default %q at create when omitted", createDefaultVersion))
	fl.StringVar(&f.license, "license", "", "license id, e.g. Apache-2.0 (falls back to config default-license / MIZAN_DEFAULT_LICENSE when omitted at create)")
	fl.StringVar(&f.author, "author", "", "primary author name (falls back to config author-name / MIZAN_AUTHOR_NAME when omitted at create)")
	// StringArrayVar (not StringSliceVar): each value is kept intact and parsed
	// on ':' below, mirroring --rubric-group's repeatable pattern.
	fl.StringArrayVar(&f.inputs, "input", nil, `declared input as "name:modality[:required]" (repeatable; modality one of text|image|audio|video|music; required defaults to false). On update, replaces all inputs`)
	// Opt-in placeholder inference (placeholder-inference design D1). Off by
	// default: inference guesses modality=text, which can be wrong, so it stays
	// explicit and reversible. Additive to --input (D5): explicit entries win,
	// inference only ADDS placeholders not already declared and not reserved.
	fl.BoolVar(&f.inferInputs, "infer-inputs", false, `also declare spec.inputs by scanning the prompt/system text for {{name}} placeholders (defaults each to modality=text, required=true). Additive: explicit --input entries win and are never overridden; the reserved {{response}} token is excluded. On update, runs after --input replaces the set, adding only placeholders not already declared`)
}

// buildInputs parses the repeatable --input flags into spec.inputs. Each spec is
// "name:modality[:required]"; the modality is validated against the allowed set
// and a duplicate input name is rejected. Returns (nil, nil) when no --input was
// given (leaving the template's inputs unchanged).
func (f *templateFlags) buildInputs() ([]registry.InputSpec, error) {
	if len(f.inputs) == 0 {
		return nil, nil
	}
	var out []registry.InputSpec
	seen := map[string]bool{}
	for _, spec := range f.inputs {
		in, err := parseInputSpec(spec)
		if err != nil {
			return nil, err
		}
		if seen[in.Name] {
			return nil, fmt.Errorf("--input %q: duplicate input name %q", spec, in.Name)
		}
		seen[in.Name] = true
		out = append(out, in)
	}
	return out, nil
}

// parseInputSpec parses one "name:modality[:required]" spec into an InputSpec.
// The name is required, the modality is validated against the allowed set, and
// the optional required flag parses as a bool (default false).
func parseInputSpec(spec string) (registry.InputSpec, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return registry.InputSpec{}, fmt.Errorf(`--input %q: want "name:modality[:required]"`, spec)
	}
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return registry.InputSpec{}, fmt.Errorf(`--input %q: input name is required ("name:modality[:required]")`, spec)
	}
	modality, err := registry.ParseModality(strings.TrimSpace(parts[1]))
	if err != nil {
		return registry.InputSpec{}, fmt.Errorf("--input %q: %w", spec, err)
	}
	required := false
	if len(parts) == 3 {
		r := strings.TrimSpace(parts[2])
		parsed, perr := strconv.ParseBool(r)
		if perr != nil {
			return registry.InputSpec{}, fmt.Errorf(`--input %q: required must be true or false, got %q`, spec, r)
		}
		required = parsed
	}
	return registry.InputSpec{Name: name, Modality: modality, Required: required}, nil
}

// applyMetadataConfigDefaults fills author/license from config when the
// corresponding flag was not passed (precedence: explicit flag > config value).
// It is CREATE-ONLY: update never injects config defaults, so an existing
// template's metadata is only touched by an explicit flag (update-safe).
func applyMetadataConfigDefaults(cmd *cobra.Command, t *registry.MetricTemplate, cfg *config.Config) {
	if cfg == nil {
		return
	}
	if !cmd.Flags().Changed("license") && t.License == "" && cfg.DefaultLicense != "" {
		t.License = cfg.DefaultLicense
	}
	if !cmd.Flags().Changed("author") && len(t.Authors) == 0 && cfg.AuthorName != "" {
		t.Authors = []registry.Author{{Name: cfg.AuthorName}}
	}
}

// readTemplateFile reads a small CLI-supplied config file safely. It mirrors the
// asset stager's guards (internal/asset): symlinks are resolved, the target must
// be a regular file (rejecting dirs, devices, FIFOs and sockets), and the size is
// capped so a large or special file cannot be read without bound.
func readTemplateFile(path string) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", path, err)
	}
	fi, err := os.Lstat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file (mode %s); refusing to read", path, fi.Mode().Type())
	}
	if fi.Size() > registry.MaxTemplateFileBytes {
		return nil, fmt.Errorf("%q is %d bytes, exceeds the %d-byte cap", path, fi.Size(), registry.MaxTemplateFileBytes)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return data, nil
}

// buildRubricGroups constructs the RubricGroups map from the rubric authoring
// flags. Precedence: --rubric-groups-file is parsed first to seed the map, then
// --rubric-group flags merge in, OVERRIDING the file's entry for any group name
// they name. Multiple --rubric-group flags with the same name accumulate. Each
// flag's criteria are split on ';', trimmed, and empties dropped. Returns
// (nil, nil) when no rubric flag was set (leaving the template unchanged).
func (f *templateFlags) buildRubricGroups(cmd *cobra.Command) (map[string][]string, error) {
	changed := func(name string) bool { return cmd.Flags().Changed(name) }
	groups := map[string][]string{}

	if changed("rubric-groups-file") && f.rubricGroupsFile != "" {
		raw, err := readTemplateFile(f.rubricGroupsFile)
		if err != nil {
			return nil, fmt.Errorf("--rubric-groups-file: %w", err)
		}
		var fileGroups map[string][]string
		if err := json.Unmarshal(raw, &fileGroups); err != nil {
			return nil, fmt.Errorf("--rubric-groups-file %q: invalid JSON: %w", f.rubricGroupsFile, err)
		}
		for name, crits := range fileGroups {
			groups[name] = crits
		}
	}

	// Repeatable flags override the file per group name; among themselves they
	// accumulate.
	flagGroups := map[string][]string{}
	for _, spec := range f.rubricGroups {
		name, list, ok := strings.Cut(spec, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf(`--rubric-group %q: want "name=criterion one;criterion two"`, spec)
		}
		var crits []string
		for _, c := range strings.Split(list, ";") {
			if c = strings.TrimSpace(c); c != "" {
				crits = append(crits, c)
			}
		}
		flagGroups[name] = append(flagGroups[name], crits...)
	}
	for name, crits := range flagGroups {
		groups[name] = crits
	}

	if len(groups) == 0 {
		return nil, nil
	}
	return groups, nil
}

// buildResponseSchema constructs the custom_schema ResponseSchema from the
// authoring flags. Precedence: --response-schema-file takes precedence over the
// inline --response-schema when both are given. The raw content must be
// well-formed JSON. Returns (nil, nil) when no schema flag was set.
func (f *templateFlags) buildResponseSchema(cmd *cobra.Command) (*registry.Schema, error) {
	changed := func(name string) bool { return cmd.Flags().Changed(name) }
	var raw string
	switch {
	case changed("response-schema-file") && f.responseSchemaFile != "":
		b, err := readTemplateFile(f.responseSchemaFile)
		if err != nil {
			return nil, fmt.Errorf("--response-schema-file: %w", err)
		}
		raw = string(b)
	case changed("response-schema") && f.responseSchema != "":
		raw = f.responseSchema
	default:
		return nil, nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("response schema is not well-formed JSON")
	}
	return &registry.Schema{JSON: raw}, nil
}

// buildHeuristic constructs the KindHeuristic HeuristicSpec from the authoring
// flags. Precedence for the schema operand: --heuristic-schema-file over the
// inline --heuristic-schema when both are given. The check type is validated
// against the allowed set. Returns (nil, nil) when no heuristic flag was set.
func (f *templateFlags) buildHeuristic(cmd *cobra.Command) (*registry.HeuristicSpec, error) {
	changed := func(name string) bool { return cmd.Flags().Changed(name) }
	anySet := changed("heuristic-type") || changed("heuristic-target") ||
		changed("heuristic-value") || changed("heuristic-case-insensitive") ||
		changed("heuristic-schema") || changed("heuristic-schema-file")
	if !anySet {
		return nil, nil
	}

	spec := &registry.HeuristicSpec{
		Target:          strings.TrimSpace(f.heuristicTarget),
		Value:           f.heuristicValue,
		CaseInsensitive: f.heuristicCaseInsensitive,
	}
	if strings.TrimSpace(f.heuristicType) != "" {
		ht, err := registry.ParseHeuristicType(strings.TrimSpace(f.heuristicType))
		if err != nil {
			return nil, fmt.Errorf("--heuristic-type: %w", err)
		}
		spec.Type = ht
	}
	switch {
	case changed("heuristic-schema-file") && f.heuristicSchemaFile != "":
		b, err := readTemplateFile(f.heuristicSchemaFile)
		if err != nil {
			return nil, fmt.Errorf("--heuristic-schema-file: %w", err)
		}
		spec.Schema = string(b)
	case changed("heuristic-schema") && f.heuristicSchema != "":
		spec.Schema = f.heuristicSchema
	}
	if strings.TrimSpace(spec.Schema) != "" && !json.Valid([]byte(spec.Schema)) {
		return nil, fmt.Errorf("heuristic schema is not well-formed JSON")
	}
	return spec, nil
}

// validateTemplate enforces that kind-specific required data is present, so the
// failure surfaces at create/update rather than later at eval run.
func validateTemplate(t *registry.MetricTemplate) error {
	switch t.Kind {
	case registry.KindRubric:
		if len(t.RubricGroups) == 0 {
			return fmt.Errorf(`kind %q requires rubric groups; pass --rubric-group "name=crit1;crit2" (repeatable) or --rubric-groups-file <path>`, t.Kind)
		}
	case registry.KindCustomSchema:
		if t.ResponseSchema == nil || strings.TrimSpace(t.ResponseSchema.JSON) == "" {
			return fmt.Errorf("kind %q requires a response schema; pass --response-schema '<json>' or --response-schema-file <path>", t.Kind)
		}
	case registry.KindHeuristic:
		if err := validateHeuristicTemplate(t); err != nil {
			return err
		}
	}
	return nil
}

// validateHeuristicTemplate enforces the kind:heuristic authoring contract at
// create/update time so a broken check fails here, not at eval run. It mirrors
// the pack-validate rules (design §4.B): a spec with a known type, a target
// declared in inputs, the operand required by the chosen check, and — for
// json-schema-valid — a schema that COMPILES.
func validateHeuristicTemplate(t *registry.MetricTemplate) error {
	spec := t.Heuristic
	if spec == nil {
		return fmt.Errorf("kind %q requires a heuristic check; pass --heuristic-type and --heuristic-target", t.Kind)
	}
	if !registry.ValidHeuristicType(spec.Type) {
		return fmt.Errorf("--heuristic-type is required and must be one of contains|regex|equals|json-valid|json-schema-valid")
	}
	if spec.Target == "" {
		return fmt.Errorf("kind %q requires --heuristic-target", t.Kind)
	}
	declared := false
	for _, in := range t.Inputs {
		if in.Name == spec.Target {
			declared = true
			break
		}
	}
	if !declared {
		return fmt.Errorf("--heuristic-target %q is not declared in --input", spec.Target)
	}
	switch spec.Type {
	case registry.HeuristicContains, registry.HeuristicEquals:
		if spec.Value == "" {
			return fmt.Errorf("--heuristic-type %q requires --heuristic-value", spec.Type)
		}
	case registry.HeuristicRegex:
		if spec.Value == "" {
			return fmt.Errorf("--heuristic-type %q requires --heuristic-value (the pattern)", spec.Type)
		}
		if _, err := registry.CompileHeuristicRegex(spec); err != nil {
			return fmt.Errorf("--heuristic-value is not a valid RE2 regex: %w", err)
		}
	case registry.HeuristicJSONSchemaValid:
		if strings.TrimSpace(spec.Schema) == "" {
			return fmt.Errorf("--heuristic-type %q requires --heuristic-schema or --heuristic-schema-file", spec.Type)
		}
	}
	return nil
}

// apply overlays the set flags onto t. When update is true, only flags the user
// explicitly changed are applied (so unspecified fields are preserved).
func (f *templateFlags) apply(cmd *cobra.Command, t *registry.MetricTemplate, update bool) error {
	changed := func(name string) bool { return cmd.Flags().Changed(name) }
	set := func(name string, fn func()) {
		if !update || changed(name) {
			fn()
		}
	}
	if t.ID == "" {
		t.ID = f.id
	}
	set("name", func() { t.Name = f.name })
	set("description", func() { t.Description = f.description })
	// Normalize the kind spelling at the parse boundary (ITEM C): the vernacular
	// aliases single/compare fold to pointwise/pairwise and an unknown value is
	// rejected here, so t.Kind is always a canonical MetricKind and downstream
	// logic never sees an alias. Kept out of the plain set() helper because it can
	// error; the same !update||changed("kind") gating is applied by hand.
	if !update || changed("kind") {
		k, err := registry.NormalizeKind(f.kind)
		if err != nil {
			return err
		}
		t.Kind = k
	}
	// Version: on create, default to createDefaultVersion when --version is
	// omitted; on update, only set when explicitly changed. Always validated as
	// semver via the P2.3 parser so a malformed value fails fast at authoring
	// time. Kept out of set() because it can error and needs the create-default.
	if !update {
		v := createDefaultVersion
		if changed("version") {
			v = f.version
		}
		if err := registry.ValidateSemver(v); err != nil {
			return fmt.Errorf("--version: %w", err)
		}
		t.Version = v
	} else if changed("version") {
		if err := registry.ValidateSemver(f.version); err != nil {
			return fmt.Errorf("--version: %w", err)
		}
		t.Version = f.version
	}

	set("license", func() { t.License = f.license })
	// Author: set from the flag when given (create always applies, update only
	// when changed). On create an omitted --author leaves Authors untouched so the
	// create command's config fallback (applyMetadataConfigDefaults) can fill it;
	// the config fallback is deliberately create-only.
	if (!update || changed("author")) && f.author != "" {
		t.Authors = []registry.Author{{Name: f.author}}
	}
	// Inputs (spec.inputs): on update, left untouched unless --input was set
	// (update-safe, REPLACE semantics — the given inputs replace all existing
	// ones); on create, populated from whatever --input flags were given.
	if !update || changed("input") {
		inputs, err := f.buildInputs()
		if err != nil {
			return err
		}
		if inputs != nil {
			t.Inputs = inputs
		}
	}

	set("prompt", func() { t.MetricPromptTemplate = f.prompt })
	set("system", func() { t.SystemInstruction = f.system })
	set("model", func() { t.AutoraterModel = f.model })
	set("sampling-count", func() { t.SamplingCount = f.samplingCount })
	set("flip-enabled", func() { t.FlipEnabled = f.flipEnabled })
	set("candidate-field", func() { t.CandidateFieldName = f.candidate })
	set("baseline-field", func() { t.BaselineFieldName = f.baseline })
	set("modality", func() {
		var ms []registry.Modality
		for _, m := range f.modalities {
			ms = append(ms, registry.Modality(strings.TrimSpace(m)))
		}
		t.Modalities = ms
	})
	set("tag", func() { t.Tags = f.tags })

	// Rubric groups (KindRubric). On update, left untouched unless a rubric flag
	// was explicitly set (update-safe); on create, populated from whatever flags
	// were given.
	if !update || changed("rubric-group") || changed("rubric-groups-file") {
		groups, err := f.buildRubricGroups(cmd)
		if err != nil {
			return err
		}
		if groups != nil {
			t.RubricGroups = groups
		}
	}

	// Response schema (KindCustomSchema). Same update-safe gating as above.
	if !update || changed("response-schema") || changed("response-schema-file") {
		schema, err := f.buildResponseSchema(cmd)
		if err != nil {
			return err
		}
		if schema != nil {
			t.ResponseSchema = schema
		}
	}

	// Heuristic spec (KindHeuristic). Same update-safe gating: on update, left
	// untouched unless a heuristic flag was explicitly set; on create, built from
	// whatever flags were given.
	if !update || changed("heuristic-type") || changed("heuristic-target") ||
		changed("heuristic-value") || changed("heuristic-case-insensitive") ||
		changed("heuristic-schema") || changed("heuristic-schema-file") {
		spec, err := f.buildHeuristic(cmd)
		if err != nil {
			return err
		}
		if spec != nil {
			t.Heuristic = spec
		}
	}

	// Placeholder inference (--infer-inputs, placeholder-inference design). Runs
	// LAST so it scans the FINAL prompt/system text — after --prompt/--system are
	// applied (create) and after the explicit --input set is applied or REPLACED
	// (update, D6). Inference is ADDITIVE + NON-DESTRUCTIVE (D5): it only APPENDS
	// inputs for {{name}} placeholders not already declared by name and not
	// reserved (e.g. {{response}}, D3), each defaulted to modality=text,
	// required=true (D4). Explicit --input entries keep their position and win on
	// any name collision. A one-line stderr summary reports what was inferred so
	// the author can correct a wrong modality by declaring that input explicitly.
	// Gated on the flag VALUE (not merely "changed"), so --infer-inputs=false is a
	// no-op on both create and update.
	if f.inferInputs {
		inferred := registry.InferInputs(t.Inputs, t.MetricPromptTemplate, t.SystemInstruction)
		t.Inputs = append(t.Inputs, inferred...)
		if len(inferred) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "--infer-inputs: no new input placeholders inferred from prompt text")
		} else {
			names := make([]string, len(inferred))
			for i, in := range inferred {
				names[i] = in.Name
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "--infer-inputs: inferred %d input(s) (modality=text, required=true): %s\n", len(inferred), strings.Join(names, ", "))
		}
	}
	return nil
}

// newRegistryCmd wires the `mizan registry` command family over registry.Service.
func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "registry",
		Short:   "Create, list, get, update, and delete metric templates",
		GroupID: groupRegistry,
	}
	cmd.AddCommand(
		newRegistryCreateCmd(),
		newRegistryListCmd(),
		newRegistryGetCmd(),
		newRegistryUpdateCmd(),
		newRegistryDeleteCmd(),
		newRegistryImportCmd(),
		newRegistryExportCmd(),
	)
	return cmd
}

// newRegistryExportCmd wires `registry export --out <dir> [--id | --namespace |
// --all]`. It writes selected local templates into a pack dir (one file per
// template under <dir>/templates/), the write side of the export→PR→import
// round-trip (design §3.7). Exactly one selector must be given. It depends only
// on registry.Service via wire — no sync/codec/yaml symbols — preserving the
// seam.
func newRegistryExportCmd() *cobra.Command {
	var (
		out       string
		id        string
		namespace string
		all       bool
	)
	cmd := &cobra.Command{
		Use:   "export --out <dir> (--id <id> | --namespace <ns> | --all)",
		Short: "Export local templates into a pack dir",
		Long: "Export metric templates from the local registry into a pack directory.\n\n" +
			"Writes one file per template under <dir>/templates/. Select what to export\n" +
			"with exactly one of --id, --namespace, or --all. Commit the pack dir and open\n" +
			"a PR to share it (Mizan does not push).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if out == "" {
				return fmt.Errorf("--out is required (the pack dir to write, e.g. packs/<name>)")
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

			report, err := svc.Export(cmd.Context(), out, registry.Selector{
				ID:        id,
				Namespace: namespace,
				All:       all,
			})
			if err != nil {
				return err
			}
			return renderExportReport(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "destination pack dir (e.g. packs/<name>) (required)")
	cmd.Flags().StringVar(&id, "id", "", "export the single template with this id")
	cmd.Flags().StringVar(&namespace, "namespace", "", "export every template in this namespace")
	cmd.Flags().BoolVar(&all, "all", false, "export every template in the registry")
	return cmd
}

// newRegistryImportCmd wires `registry import <path>`. It imports metric
// templates from a LOCAL pack tree (a mizan-templates checkout with a packs/
// dir, or a single pack dir) into the local registry, reconciling id collisions
// per the chosen --strategy (design §3.8). --dry-run previews the outcome without
// writing. Git-URL/default-source import lands in P2.5. It depends ONLY on
// registry.Service via wire — it imports no sync/codec/yaml symbols, preserving
// the seam.
func newRegistryImportCmd() *cobra.Command {
	var (
		strategy  string
		namespace string
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:   "import [<src>]",
		Short: "Import metric templates from a pack tree or git URL",
		Long: "Import metric templates from a pack source into the local registry.\n\n" +
			"<src> is one of:\n" +
			"  - a local checkout that contains a packs/ directory, or a single pack dir\n" +
			"  - a git URL (e.g. https://github.com/ghchinoy/mizan-templates or the\n" +
			"    scheme-less github.com/ghchinoy/mizan-templates); Mizan shells out to\n" +
			"    your git to clone/pull it into the pack cache, then reads its packs/ tree\n\n" +
			"With NO <src>, Mizan imports from the configured default templates repo\n" +
			"(templates-repo; default github.com/ghchinoy/mizan-templates).\n\n" +
			"Use --namespace to import only the packs under one namespace. Incoming\n" +
			"templates are reconciled against the local registry by id using --strategy:\n\n" +
			"  newer      (default) take the higher version; on an equal-version but\n" +
			"             changed-content clash, report a conflict and skip (never clobber)\n" +
			"  skip       only insert absent templates; never overwrite\n" +
			"  overwrite  replace the local copy unconditionally (including dirty edits)\n" +
			"  fork       import a conflicting/dirty upstream under <ns>-fork/<slug>,\n" +
			"             keeping the local copy\n\n" +
			"A template you have edited locally (dirty) is protected: under the default\n" +
			"'newer' it is skipped with a warning rather than overwritten. Re-importing an\n" +
			"unchanged pack is a no-op. Use --dry-run to preview without writing anything.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			var src string
			if len(args) == 1 {
				src = args[0]
			}
			report, err := svc.Import(cmd.Context(), src, registry.ImportOptions{
				Strategy:  registry.ImportStrategy(strategy),
				Namespace: namespace,
				DryRun:    dryRun,
			})
			if err != nil {
				return err
			}
			return renderImportReport(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().StringVar(&strategy, "strategy", string(registry.StrategyNewer), "conflict resolution: newer|skip|overwrite|fork")
	cmd.Flags().StringVar(&namespace, "namespace", "", "import only packs under this namespace")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "compute and print the import report without writing anything")
	return cmd
}

func newRegistryCreateCmd() *cobra.Command {
	var f templateFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a metric template",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.id == "" {
				return fmt.Errorf("--id is required")
			}
			// Load config first (no DB is opened here — only wire.OpenService
			// below does that) so --author/--license can fall back to the
			// configured author-name / default-license when omitted.
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			// Build, apply config fallbacks, then validate the template before
			// acquiring any backend, so a missing rubric/schema fails fast
			// without opening a DB.
			var t registry.MetricTemplate
			if err := f.apply(cmd, &t, false); err != nil {
				return err
			}
			applyMetadataConfigDefaults(cmd, &t, cfg)
			if err := validateTemplate(&t); err != nil {
				return err
			}
			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			if err := svc.Create(cmd.Context(), t); err != nil {
				return err
			}
			created, err := svc.Get(cmd.Context(), t.ID)
			if err != nil {
				return err
			}
			return renderTemplate(cmd.OutOrStdout(), created)
		},
	}
	f.bind(cmd)
	return cmd
}

func newRegistryListCmd() *cobra.Command {
	var (
		namespace string
		kind      string
		tags      []string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List metric templates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}

			// Normalize/validate the --kind filter BEFORE opening the DB so an
			// invalid filter fails fast with the clear enumerated error and does
			// no DB work. Accept the vernacular aliases (single/compare) here too,
			// folding them to the canonical kind before filtering (ITEM C).
			filter := registry.ListFilter{Namespace: namespace, Tags: tags}
			if kind != "" {
				k, err := registry.NormalizeKind(kind)
				if err != nil {
					return err
				}
				filter.Kinds = []registry.MetricKind{k}
			}

			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			ts, err := svc.List(cmd.Context(), filter)
			if err != nil {
				return err
			}
			return renderTemplateList(cmd.OutOrStdout(), ts)
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "filter by id namespace prefix")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by metric kind (accepts single|pointwise, compare|pairwise, rubric, custom_schema)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "filter to templates carrying ALL given tags (repeatable)")
	return cmd
}

func newRegistryGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a metric template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			t, err := svc.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return renderTemplate(cmd.OutOrStdout(), t)
		},
	}
	return cmd
}

func newRegistryUpdateCmd() *cobra.Command {
	var f templateFlags
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a metric template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			t, err := svc.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := f.apply(cmd, t, true); err != nil {
				return err
			}
			if err := validateTemplate(t); err != nil {
				return err
			}
			if err := svc.Update(cmd.Context(), *t); err != nil {
				return err
			}
			updated, err := svc.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return renderTemplate(cmd.OutOrStdout(), updated)
		},
	}
	f.bind(cmd)
	return cmd
}

func newRegistryDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a metric template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			if err := svc.Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}
	return cmd
}

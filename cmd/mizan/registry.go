package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/registry"
	"github.com/ghchinoy/mizan/internal/wire"
)

// maxTemplateFileBytes caps the size of a --*-file input the CLI will read.
// Rubric/schema definitions are small config documents; this is a defense-in-depth
// bound so a large or special file cannot be read without limit.
const maxTemplateFileBytes int64 = 1 << 20 // 1 MiB

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
	// rubric (KindRubric) authoring
	rubricGroups     []string // repeatable "name=criterion one;criterion two"
	rubricGroupsFile string   // JSON object {"group": ["crit1", ...], ...}
	// custom_schema (KindCustomSchema) authoring
	responseSchema     string // inline JSON-Schema string
	responseSchemaFile string // path to a JSON-Schema file
}

func (f *templateFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.id, "id", "", "stable template id, <namespace>/<slug> (required)")
	fl.StringVar(&f.name, "name", "", "human-readable name")
	fl.StringVar(&f.description, "description", "", "description")
	fl.StringVar(&f.kind, "kind", string(registry.KindPointwise), "metric kind: pointwise|pairwise|rubric|custom_schema")
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
	if fi.Size() > maxTemplateFileBytes {
		return nil, fmt.Errorf("%q is %d bytes, exceeds the %d-byte cap", path, fi.Size(), maxTemplateFileBytes)
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
	set("kind", func() { t.Kind = registry.MetricKind(f.kind) })
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
	)
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
			// Build and validate the template before acquiring any backend, so a
			// missing rubric/schema fails fast without opening a DB.
			var t registry.MetricTemplate
			if err := f.apply(cmd, &t, false); err != nil {
				return err
			}
			if err := validateTemplate(&t); err != nil {
				return err
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
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List metric templates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			svc, closeSvc, err := wire.OpenService(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = closeSvc() }()

			filter := registry.ListFilter{Namespace: namespace}
			if kind != "" {
				filter.Kinds = []registry.MetricKind{registry.MetricKind(kind)}
			}
			ts, err := svc.List(cmd.Context(), filter)
			if err != nil {
				return err
			}
			return renderTemplateList(cmd.OutOrStdout(), ts)
		},
	}
	cmd.Flags().StringVar(&namespace, "namespace", "", "filter by id namespace prefix")
	cmd.Flags().StringVar(&kind, "kind", "", "filter by metric kind")
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

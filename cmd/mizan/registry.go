package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/registry"
)

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
}

func (f *templateFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.id, "id", "", "stable template id, <namespace>/<slug> (required)")
	fl.StringVar(&f.name, "name", "", "human-readable name")
	fl.StringVar(&f.description, "description", "", "description")
	fl.StringVar(&f.kind, "kind", string(registry.KindPointwise), "metric kind: pointwise|pairwise|rubric|custom_schema")
	fl.StringVar(&f.prompt, "prompt", "", "metric prompt template ({{var}} placeholders)")
	fl.StringVar(&f.system, "system", "", "system instruction")
	fl.StringVar(&f.model, "model", "gemini-2.5-flash", "autorater model (publisher-relative id)")
	fl.Int32Var(&f.samplingCount, "sampling-count", 4, "autorater sampling count (1-32)")
	fl.BoolVar(&f.flipEnabled, "flip-enabled", true, "pairwise: flip candidate/baseline to reduce bias")
	fl.StringSliceVar(&f.modalities, "modality", []string{"text"}, "accepted modalities (repeatable)")
	fl.StringSliceVar(&f.tags, "tag", nil, "tags (repeatable)")
	fl.StringVar(&f.candidate, "candidate-field", "", "pairwise: candidate response field name")
	fl.StringVar(&f.baseline, "baseline-field", "", "pairwise: baseline response field name")
}

// apply overlays the set flags onto t. When update is true, only flags the user
// explicitly changed are applied (so unspecified fields are preserved).
func (f *templateFlags) apply(cmd *cobra.Command, t *registry.MetricTemplate, update bool) {
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
			cfg, err := mustConfig()
			if err != nil {
				return err
			}
			svc, closeSvc, err := openService(cfg)
			if err != nil {
				return err
			}
			defer closeSvc()

			var t registry.MetricTemplate
			f.apply(cmd, &t, false)
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
			svc, closeSvc, err := openService(cfg)
			if err != nil {
				return err
			}
			defer closeSvc()

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
			svc, closeSvc, err := openService(cfg)
			if err != nil {
				return err
			}
			defer closeSvc()

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
			svc, closeSvc, err := openService(cfg)
			if err != nil {
				return err
			}
			defer closeSvc()

			t, err := svc.Get(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			f.apply(cmd, t, true)
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
			svc, closeSvc, err := openService(cfg)
			if err != nil {
				return err
			}
			defer closeSvc()

			if err := svc.Delete(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}
	return cmd
}

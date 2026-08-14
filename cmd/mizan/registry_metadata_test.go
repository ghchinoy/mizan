package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/config"
	"github.com/ghchinoy/mizan/internal/registry"
)

// TestVersionDefaultOnCreate: an omitted --version defaults to createDefaultVersion.
func TestVersionDefaultOnCreate(t *testing.T) {
	got, err := applyFromArgs(t, false, nil, "--kind", "pointwise")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Version != createDefaultVersion {
		t.Errorf("Version = %q, want default %q", got.Version, createDefaultVersion)
	}
}

// TestVersionExplicitAccepted: an explicit valid semver is honored on create.
func TestVersionExplicitAccepted(t *testing.T) {
	got, err := applyFromArgs(t, false, nil, "--version", "2.3.4")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Version != "2.3.4" {
		t.Errorf("Version = %q, want 2.3.4", got.Version)
	}
}

// TestVersionBadSemverRejected: a malformed --version is rejected at authoring
// time (both create and update).
func TestVersionBadSemverRejected(t *testing.T) {
	for _, update := range []bool{false, true} {
		var base *registry.MetricTemplate
		if update {
			base = &registry.MetricTemplate{ID: "test/x", Kind: registry.KindPointwise, Version: "1.0.0"}
		}
		_, err := applyFromArgs(t, update, base, "--version", "not-a-version")
		if err == nil || !strings.Contains(err.Error(), "not valid semver") {
			t.Errorf("update=%v: err = %v, want a semver error", update, err)
		}
	}
}

// TestVersionUpdatePreservedWhenOmitted: an update without --version does not
// clobber the stored version (update-safe), and no create default is injected.
func TestVersionUpdatePreservedWhenOmitted(t *testing.T) {
	base := &registry.MetricTemplate{ID: "test/x", Kind: registry.KindPointwise, Version: "3.1.4"}
	got, err := applyFromArgs(t, true, base, "--name", "New")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Version != "3.1.4" {
		t.Errorf("Version = %q, want preserved 3.1.4", got.Version)
	}
}

// TestLicenseAndAuthorFromFlags: --license/--author populate the template.
func TestLicenseAndAuthorFromFlags(t *testing.T) {
	got, err := applyFromArgs(t, false, nil, "--license", "Apache-2.0", "--author", "Jane Doe")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.License != "Apache-2.0" {
		t.Errorf("License = %q, want Apache-2.0", got.License)
	}
	if len(got.Authors) != 1 || got.Authors[0].Name != "Jane Doe" {
		t.Errorf("Authors = %#v, want [{Jane Doe}]", got.Authors)
	}
}

// TestInputRepeatableParsed: multiple --input flags become spec.inputs with the
// modality validated and the required flag parsed.
func TestInputRepeatableParsed(t *testing.T) {
	got, err := applyFromArgs(t, false, nil,
		"--input", "prompt:text:true",
		"--input", "image:image",
		"--input", "clip:video:false")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []registry.InputSpec{
		{Name: "prompt", Modality: registry.ModalityText, Required: true},
		{Name: "image", Modality: registry.ModalityImage, Required: false},
		{Name: "clip", Modality: registry.ModalityVideo, Required: false},
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Errorf("Inputs = %#v, want %#v", got.Inputs, want)
	}
}

// TestInputRequiredDefaultsFalse: an --input without the third field defaults to
// required=false (the documented convention).
func TestInputRequiredDefaultsFalse(t *testing.T) {
	got, err := applyFromArgs(t, false, nil, "--input", "prompt:text")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(got.Inputs) != 1 || got.Inputs[0].Required {
		t.Errorf("Inputs = %#v, want required=false", got.Inputs)
	}
}

// TestInputMalformedRejected: malformed --input specs are rejected with a clear
// error.
func TestInputMalformedRejected(t *testing.T) {
	cases := map[string]string{
		"missing modality":     "prompt",
		"too many fields":      "a:text:true:extra",
		"empty name":           ":text",
		"unknown modality":     "prompt:txt",
		"bad required boolean": "prompt:text:maybe",
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := applyFromArgs(t, false, nil, "--input", spec); err == nil {
				t.Errorf("--input %q = nil error, want error", spec)
			}
		})
	}
}

// TestInputDuplicateNameRejected: two inputs with the same name are rejected.
func TestInputDuplicateNameRejected(t *testing.T) {
	_, err := applyFromArgs(t, false, nil, "--input", "a:text", "--input", "a:image")
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v, want duplicate-name error", err)
	}
}

// TestInputUpdateReplaces: on update, --input REPLACES all existing inputs, and
// an update without --input preserves them.
func TestInputUpdateReplaces(t *testing.T) {
	base := &registry.MetricTemplate{
		ID:     "test/x",
		Kind:   registry.KindPointwise,
		Inputs: []registry.InputSpec{{Name: "old", Modality: registry.ModalityText}},
	}
	// No --input -> preserved.
	got, err := applyFromArgs(t, true, base, "--name", "New")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(got.Inputs, base.Inputs) {
		t.Errorf("Inputs not preserved on update: %#v", got.Inputs)
	}
	// With --input -> replaced entirely.
	got, err = applyFromArgs(t, true, base, "--input", "new:image:true")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []registry.InputSpec{{Name: "new", Modality: registry.ModalityImage, Required: true}}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Errorf("Inputs = %#v, want replaced %#v", got.Inputs, want)
	}
}

// applyWithConfigDefaults mirrors applyFromArgs but also runs the create-time
// config fallback (applyMetadataConfigDefaults), so the flag>config precedence
// can be exercised without opening a DB.
func applyWithConfigDefaults(t *testing.T, cfg *config.Config, args ...string) (*registry.MetricTemplate, error) {
	t.Helper()
	var f templateFlags
	cmd := &cobra.Command{
		Use:           "x",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
	f.bind(cmd)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return nil, err
	}
	var tmpl registry.MetricTemplate
	if err := f.apply(cmd, &tmpl, false); err != nil {
		return nil, err
	}
	applyMetadataConfigDefaults(cmd, &tmpl, cfg)
	return &tmpl, nil
}

// TestAuthorLicenseConfigFallback: when --author/--license are omitted, create
// falls back to the configured author-name / default-license.
func TestAuthorLicenseConfigFallback(t *testing.T) {
	cfg := &config.Config{AuthorName: "Config Author", DefaultLicense: "MIT"}
	got, err := applyWithConfigDefaults(t, cfg)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.License != "MIT" {
		t.Errorf("License = %q, want config fallback MIT", got.License)
	}
	if len(got.Authors) != 1 || got.Authors[0].Name != "Config Author" {
		t.Errorf("Authors = %#v, want config fallback [{Config Author}]", got.Authors)
	}
}

// TestAuthorLicenseFlagOverridesConfig: an explicit flag wins over the config
// value.
func TestAuthorLicenseFlagOverridesConfig(t *testing.T) {
	cfg := &config.Config{AuthorName: "Config Author", DefaultLicense: "MIT"}
	got, err := applyWithConfigDefaults(t, cfg, "--author", "Flag Author", "--license", "Apache-2.0")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.License != "Apache-2.0" {
		t.Errorf("License = %q, want flag Apache-2.0 (flag>config)", got.License)
	}
	if len(got.Authors) != 1 || got.Authors[0].Name != "Flag Author" {
		t.Errorf("Authors = %#v, want flag [{Flag Author}]", got.Authors)
	}
}

// TestMetadataRoundTripThroughCodec: a template authored with the new fields
// round-trips through the YAML codec and validates against the pack schema.
func TestMetadataRoundTripThroughCodec(t *testing.T) {
	got, err := applyFromArgs(t, false, nil,
		"--id", "team/quality",
		"--kind", "pointwise",
		"--version", "1.2.3",
		"--license", "Apache-2.0",
		"--author", "Jane Doe",
		"--modality", "text",
		"--input", "response:text:true",
		"--prompt", "Rate {{response}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got.ID = "team/quality" // apply only sets ID when empty; make explicit here

	codec := registry.NewYAMLCodec()
	data, err := codec.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Version != "1.2.3" || back.License != "Apache-2.0" {
		t.Errorf("round-trip metadata lost: version=%q license=%q", back.Version, back.License)
	}
	if len(back.Authors) != 1 || back.Authors[0].Name != "Jane Doe" {
		t.Errorf("round-trip authors lost: %#v", back.Authors)
	}
	if !reflect.DeepEqual(back.Inputs, got.Inputs) {
		t.Errorf("round-trip inputs lost: %#v, want %#v", back.Inputs, got.Inputs)
	}
	if err := registry.ValidateTemplateSchema(data); err != nil {
		t.Errorf("authored template violates pack schema: %v", err)
	}
}

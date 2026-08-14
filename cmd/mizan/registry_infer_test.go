package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/registry"
)

// applyInfer mirrors applyFromArgs but also CAPTURES the command's stderr, so the
// --infer-inputs summary line (design D4) can be asserted. It runs the full
// flag-parse -> build -> apply path without opening a DB.
func applyInfer(t *testing.T, update bool, base *registry.MetricTemplate, args ...string) (*registry.MetricTemplate, string, error) {
	t.Helper()
	var f templateFlags
	var stderr bytes.Buffer
	cmd := &cobra.Command{
		Use:           "x",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(*cobra.Command, []string) error { return nil },
	}
	cmd.SetErr(&stderr)
	f.bind(cmd)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		return nil, stderr.String(), err
	}
	var tmpl registry.MetricTemplate
	if base != nil {
		tmpl = *base
	}
	if err := f.apply(cmd, &tmpl, update); err != nil {
		return nil, stderr.String(), err
	}
	return &tmpl, stderr.String(), nil
}

func names(in []registry.InputSpec) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.Name
	}
	return out
}

// TestInferOffNoInference: without --infer-inputs, prompt placeholders are NOT
// turned into inputs (behavior unchanged; design test matrix row 1).
func TestInferOffNoInference(t *testing.T) {
	got, stderr, err := applyInfer(t, false, nil, "--prompt", "Rate {{answer}} for {{question}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(got.Inputs) != 0 {
		t.Errorf("Inputs = %#v, want none without --infer-inputs", got.Inputs)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty without --infer-inputs", stderr)
	}
}

// TestInferOnCreatesTextRequired: --infer-inputs turns placeholders into
// text/required=true inputs in first-seen order, deduped (design D2/D4), and
// emits a one-line stderr summary naming them.
func TestInferOnCreatesTextRequired(t *testing.T) {
	got, stderr, err := applyInfer(t, false, nil,
		"--infer-inputs",
		"--prompt", "Compare {{question}} to {{answer}}; reconsider {{question}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []registry.InputSpec{
		{Name: "question", Modality: registry.ModalityText, Required: true},
		{Name: "answer", Modality: registry.ModalityText, Required: true},
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want %#v", got.Inputs, want)
	}
	// Non-vacuous stderr assertions: it must name both inferred inputs and state
	// the defaulted modality/required.
	for _, sub := range []string{"question", "answer", "modality=text", "required=true", "inferred 2"} {
		if !strings.Contains(stderr, sub) {
			t.Errorf("stderr %q missing %q", stderr, sub)
		}
	}
}

// TestInferExcludesResponse: the reserved {{response}} token is excluded from
// inference (design D3), while a non-reserved placeholder alongside it is added.
func TestInferExcludesResponse(t *testing.T) {
	got, stderr, err := applyInfer(t, false, nil,
		"--infer-inputs",
		"--prompt", "Judge {{response}} against {{rubric}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(names(got.Inputs), []string{"rubric"}) {
		t.Fatalf("Inputs = %#v, want only [rubric] ({{response}} reserved)", got.Inputs)
	}
	if strings.Contains(stderr, "response") {
		t.Errorf("stderr %q must not mention the reserved response token", stderr)
	}
}

// TestInferExplicitWins: an explicit --input foo:image alongside {{foo}} in the
// prompt keeps the explicit modality (image, not text) and is NOT duplicated
// (design D5); other placeholders are still inferred.
func TestInferExplicitWins(t *testing.T) {
	got, _, err := applyInfer(t, false, nil,
		"--infer-inputs",
		"--input", "foo:image:false",
		"--prompt", "Look at {{foo}} and read {{bar}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []registry.InputSpec{
		{Name: "foo", Modality: registry.ModalityImage, Required: false}, // explicit wins, position preserved
		{Name: "bar", Modality: registry.ModalityText, Required: true},   // inferred, appended after
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want %#v (explicit wins, no dup, inferred appended)", got.Inputs, want)
	}
}

// TestInferKeepsExplicitAbsentFromPrompt: an explicit input whose name never
// appears in the prompt is preserved (inference never drops explicit entries,
// design D5).
func TestInferKeepsExplicitAbsentFromPrompt(t *testing.T) {
	got, _, err := applyInfer(t, false, nil,
		"--infer-inputs",
		"--input", "ghost:audio:false",
		"--prompt", "Only mentions {{here}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []registry.InputSpec{
		{Name: "ghost", Modality: registry.ModalityAudio, Required: false},
		{Name: "here", Modality: registry.ModalityText, Required: true},
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want %#v (explicit-absent-from-prompt kept)", got.Inputs, want)
	}
}

// TestInferNoNewPlaceholders: --infer-inputs with a prompt whose only placeholder
// is already declared adds nothing and reports the "no new" summary.
func TestInferNoNewPlaceholders(t *testing.T) {
	got, stderr, err := applyInfer(t, false, nil,
		"--infer-inputs",
		"--input", "answer:text:true",
		"--prompt", "Rate {{answer}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []registry.InputSpec{{Name: "answer", Modality: registry.ModalityText, Required: true}}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want unchanged %#v", got.Inputs, want)
	}
	if !strings.Contains(stderr, "no new") {
		t.Errorf("stderr = %q, want a 'no new' summary", stderr)
	}
}

// TestInferFalseIsNoOp: --infer-inputs=false is an explicit no-op (gated on the
// flag value, not merely "changed").
func TestInferFalseIsNoOp(t *testing.T) {
	got, stderr, err := applyInfer(t, false, nil,
		"--infer-inputs=false",
		"--prompt", "Rate {{answer}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(got.Inputs) != 0 {
		t.Errorf("Inputs = %#v, want none with --infer-inputs=false", got.Inputs)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty with --infer-inputs=false", stderr)
	}
}

// TestInferScansSystemInstruction: placeholders in --system are inferred too
// (inference scans the author-written prompt AND system text), consistent with
// how validate.go treats both fields.
func TestInferScansSystemInstruction(t *testing.T) {
	got, _, err := applyInfer(t, false, nil,
		"--infer-inputs",
		"--prompt", "Rate {{answer}}",
		"--system", "You grade using {{policy}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(names(got.Inputs), []string{"answer", "policy"}) {
		t.Fatalf("Inputs = %#v, want [answer policy] (prompt then system)", got.Inputs)
	}
}

// TestInferUpdateReplaceThenInfer: on update, --input REPLACES the set, THEN
// inference adds placeholders not already present (design D6). Verified from an
// existing template that had a different input set and prompt.
func TestInferUpdateReplaceThenInfer(t *testing.T) {
	base := &registry.MetricTemplate{
		ID:                   "test/x",
		Kind:                 registry.KindPointwise,
		Version:              "1.0.0",
		Inputs:               []registry.InputSpec{{Name: "stale", Modality: registry.ModalityText, Required: true}},
		MetricPromptTemplate: "old prompt with {{stale}}",
	}
	got, stderr, err := applyInfer(t, true, base,
		"--infer-inputs",
		"--input", "kept:image:false",
		"--prompt", "New prompt uses {{kept}} and {{fresh}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The prior "stale" input is GONE (replaced by --input); "kept" is explicit
	// and wins its modality; "fresh" is inferred and appended.
	want := []registry.InputSpec{
		{Name: "kept", Modality: registry.ModalityImage, Required: false},
		{Name: "fresh", Modality: registry.ModalityText, Required: true},
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want %#v (replace-then-infer)", got.Inputs, want)
	}
	if !strings.Contains(stderr, "fresh") {
		t.Errorf("stderr = %q, want it to name the inferred 'fresh'", stderr)
	}
}

// TestInferUpdateNoInputInfersFromExistingPrompt: on update WITHOUT --input, the
// existing input set is the base; inference adds placeholders from the (possibly
// new) prompt not already present, leaving existing inputs untouched (D6, other
// direction).
func TestInferUpdateNoInputInfersFromExistingPrompt(t *testing.T) {
	base := &registry.MetricTemplate{
		ID:                   "test/x",
		Kind:                 registry.KindPointwise,
		Version:              "1.0.0",
		Inputs:               []registry.InputSpec{{Name: "existing", Modality: registry.ModalityVideo, Required: false}},
		MetricPromptTemplate: "refers to {{existing}} and {{added}}",
	}
	got, _, err := applyInfer(t, true, base, "--infer-inputs")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []registry.InputSpec{
		{Name: "existing", Modality: registry.ModalityVideo, Required: false}, // untouched
		{Name: "added", Modality: registry.ModalityText, Required: true},      // inferred from existing prompt
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want %#v (no --input: existing kept, prompt placeholder added)", got.Inputs, want)
	}
}

// TestInferRoundTripThroughCodecAndValidate: an inferred-input template
// round-trips through the YAML codec and passes pack-schema validation, proving
// inferred inputs are indistinguishable from explicit ones downstream (design D7
// + test matrix final row).
func TestInferRoundTripThroughCodecAndValidate(t *testing.T) {
	got, _, err := applyInfer(t, false, nil,
		"--id", "team/infer",
		"--kind", "pointwise",
		"--modality", "text",
		"--infer-inputs",
		"--prompt", "Rate {{response}} for {{clarity}} and {{depth}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got.ID = "team/infer" // apply only sets ID when empty
	// {{response}} excluded; clarity/depth inferred as text/required=true.
	want := []registry.InputSpec{
		{Name: "clarity", Modality: registry.ModalityText, Required: true},
		{Name: "depth", Modality: registry.ModalityText, Required: true},
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want %#v", got.Inputs, want)
	}

	codec := registry.NewYAMLCodec()
	data, err := codec.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := codec.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back.Inputs, got.Inputs) {
		t.Fatalf("round-trip inputs lost: %#v, want %#v", back.Inputs, got.Inputs)
	}
	if err := registry.ValidateTemplateSchema(data); err != nil {
		t.Fatalf("inferred-input template violates pack schema: %v", err)
	}
}

// TestInferPassesFullValidatePack: inferred inputs pass the SAME full pack
// validation as explicit ones (design D7) — including the placeholder-consistency
// rules (every {{x}} declared, every required input referenced, each modality in
// spec.modalities). The realistic pointwise recipe declares {{response}}
// explicitly (it is reserved, so inference never adds it) and lets inference add
// the remaining placeholders; the marshaled manifest then validates error-free.
func TestInferPassesFullValidatePack(t *testing.T) {
	got, _, err := applyInfer(t, false, nil,
		"--id", "team/infer-valid",
		"--name", "Infer Valid",
		"--description", "Exercises --infer-inputs full validation.",
		"--license", "Apache-2.0",
		"--author", "Jane Doe",
		"--kind", "pointwise",
		"--modality", "text",
		"--input", "response:text:true", // reserved token declared explicitly
		"--infer-inputs",
		"--prompt", "Rate {{response}} for {{clarity}} and {{depth}}")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got.ID = "team/infer-valid"
	// response explicit + clarity/depth inferred, all text/required=true.
	want := []registry.InputSpec{
		{Name: "response", Modality: registry.ModalityText, Required: true},
		{Name: "clarity", Modality: registry.ModalityText, Required: true},
		{Name: "depth", Modality: registry.ModalityText, Required: true},
	}
	if !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("Inputs = %#v, want %#v", got.Inputs, want)
	}

	data, err := registry.NewYAMLCodec().Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Lay the manifest into a minimal pack dir (<root>/templates/<file>.yaml) and
	// run the FULL pack validator, which layers placeholder-consistency on top of
	// the structural schema check.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "infer-valid.yaml"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rep, err := registry.ValidatePack(root)
	if err != nil {
		t.Fatalf("ValidatePack: %v", err)
	}
	if rep.HasErrors() {
		t.Fatalf("inferred-input template failed full validation: %d error(s): %#v", rep.Errors(), rep.Findings)
	}
}

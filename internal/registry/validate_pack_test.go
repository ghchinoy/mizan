package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles writes each rel->content under root, creating parent dirs.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
	}
}

// validTemplate is a minimal, fully-valid pointwise template.
const validTemplate = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
  id: acme/good
  name: Good
  description: A fine template.
  version: 1.0.0
  license: Apache-2.0
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - name: response
      modality: text
      required: true
  metricPromptTemplate: "Rate this: {{response}}"
  autorater:
    model: gemini-2.5-pro
    samplingCount: 4
`

// validatePackDir runs ValidatePack over a single pack dir holding the given
// templates/ and evalsets/ files (keys are relative to the pack dir).
func validatePackDir(t *testing.T, files map[string]string) *Report {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, files)
	rep, err := ValidatePack(root)
	if err != nil {
		t.Fatalf("ValidatePack: %v", err)
	}
	return rep
}

// errorMessages returns the error-severity messages joined for substring asserts.
func errorMessages(rep *Report) string {
	var b strings.Builder
	for _, f := range rep.Findings {
		if f.Severity == SeverityError {
			b.WriteString(f.Message)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func warningMessages(rep *Report) string {
	var b strings.Builder
	for _, f := range rep.Findings {
		if f.Severity == SeverityWarning {
			b.WriteString(f.Message)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// --- the scaffolded google-brand example passes (acceptance §9.3) ------------

func TestValidatePackAcceptsScaffoldedGoogleBrand(t *testing.T) {
	rep, err := ValidatePack("testdata")
	if err != nil {
		t.Fatalf("ValidatePack(testdata): %v", err)
	}
	if rep.HasErrors() {
		t.Fatalf("scaffolded google-brand pack should validate clean, got errors:\n%s", errorMessages(rep))
	}
}

// --- template defect classes (acceptance §6/P2.2 + §9.3) ---------------------

func TestValidatePackTemplateDefects(t *testing.T) {
	tmpl := func(spec string) map[string]string {
		return map[string]string{"templates/t.yaml": spec}
	}
	cases := []struct {
		name      string
		files     map[string]string
		wantError string // substring expected among the error messages
	}{
		{
			name:  "valid",
			files: tmpl(validTemplate),
		},
		{
			name: "unknown-kind-enum",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: bogus
  metricPromptTemplate: "hi"
`),
			wantError: "kind",
		},
		{
			name: "missing-kind-specific-rubric",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: rubric
  metricPromptTemplate: "hi"
`),
			wantError: "rubricGroups",
		},
		{
			name: "undeclared-placeholder",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "Rate {{response}} vs {{missing}}"
`),
			wantError: "undeclared placeholder {{missing}}",
		},
		{
			name: "invalid-semver",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: "1.0"}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`),
			wantError: "not valid semver",
		},
		{
			name: "input-modality-not-in-modalities",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: video, required: true}
  metricPromptTemplate: "Rate {{response}}"
`),
			wantError: "not listed in spec.modalities",
		},
		{
			name: "misspelled-key-additional-properties",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
  samplingCount: 4
`),
			wantError: "schema",
		},
		{
			name: "pairwise-missing-candidate-baseline",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pairwise
  metricPromptTemplate: "compare"
`),
			wantError: "candidateFieldName",
		},
		{
			name: "pointwise-forbids-rubric",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
  rubricGroups:
    quality: ["clear"]
`),
			wantError: "rubric-only",
		},
		{
			name: "custom-schema-invalid-response-schema",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: custom_schema
  metricPromptTemplate: "hi"
  responseSchema:
    type: not-a-real-type
`),
			wantError: "responseSchema is not a valid JSON Schema",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := validatePackDir(t, tc.files)
			if tc.wantError == "" {
				if rep.HasErrors() {
					t.Fatalf("want no errors, got:\n%s", errorMessages(rep))
				}
				return
			}
			if !rep.HasErrors() {
				t.Fatalf("want an error containing %q, got none", tc.wantError)
			}
			if !strings.Contains(errorMessages(rep), tc.wantError) {
				t.Fatalf("want an error containing %q, got:\n%s", tc.wantError, errorMessages(rep))
			}
		})
	}
}

// duplicate id in one pack is an error.
func TestValidatePackDuplicateID(t *testing.T) {
	rep := validatePackDir(t, map[string]string{
		"templates/a.yaml": validTemplate,
		"templates/b.yaml": validTemplate, // same id acme/good
	})
	if !strings.Contains(errorMessages(rep), "duplicate template id") {
		t.Fatalf("want duplicate id error, got:\n%s", errorMessages(rep))
	}
}

// both vernacular (single/compare) and canonical kinds are accepted.
func TestValidatePackAcceptsVernacularAndCanonicalKinds(t *testing.T) {
	for _, kind := range []string{"single", "pointwise"} {
		t.Run(kind, func(t *testing.T) {
			spec := strings.Replace(validTemplate, "kind: pointwise", "kind: "+kind, 1)
			rep := validatePackDir(t, map[string]string{"templates/t.yaml": spec})
			if rep.HasErrors() {
				t.Fatalf("kind %q should be accepted, got:\n%s", kind, errorMessages(rep))
			}
		})
	}
	// compare (pairwise) with candidate+baseline declared.
	pairwise := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/cmp, version: 1.0.0, description: d, license: Apache-2.0}
spec:
  kind: compare
  modalities: [text]
  inputs:
    - {name: cand, modality: text, required: true}
    - {name: base, modality: text, required: true}
  candidateFieldName: cand
  baselineFieldName: base
  metricPromptTemplate: "compare {{cand}} vs {{base}}"
  autorater: {model: gemini-2.5-pro}
`
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": pairwise})
	if rep.HasErrors() {
		t.Fatalf("compare kind should be accepted, got:\n%s", errorMessages(rep))
	}
}

// lint warnings do not fail the pack.
func TestValidatePackLintWarningsAreNonFatal(t *testing.T) {
	spec := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "Rate {{response}}"
`
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": spec})
	if rep.HasErrors() {
		t.Fatalf("lint-only defects must not error, got:\n%s", errorMessages(rep))
	}
	if rep.Warnings() == 0 {
		t.Fatalf("expected lint warnings (missing description/license/model), got none")
	}
}

// --- evalset defect classes (acceptance §9.3a) -------------------------------

const validEvalSet = `apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata:
  id: acme/suite
  name: Suite
  version: 1.0.0
  assetClass: product-video
spec:
  members:
    - metric: acme/good
  aggregation:
    method: mean
`

func TestValidatePackEvalSetDefects(t *testing.T) {
	// Each case ships alongside the valid template so acme/good resolves in-tree.
	base := func(evalset string) map[string]string {
		return map[string]string{
			"templates/good.yaml": validTemplate,
			"evalsets/s.yaml":     evalset,
		}
	}
	cases := []struct {
		name      string
		evalset   string
		wantError string
	}{
		{name: "valid", evalset: validEvalSet},
		{
			name: "empty-members",
			evalset: `apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata: {id: acme/suite, name: S, version: 1.0.0, assetClass: video}
spec:
  members: []
`,
			wantError: "members",
		},
		{
			name: "malformed-member-id",
			evalset: `apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata: {id: acme/suite, name: S, version: 1.0.0, assetClass: video}
spec:
  members:
    - metric: "Not A Valid Id"
`,
			wantError: "not a valid template id",
		},
		{
			name: "bad-semver",
			evalset: `apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata: {id: acme/suite, name: S, version: "v1", assetClass: video}
spec:
  members:
    - metric: acme/good
`,
			wantError: "not valid semver",
		},
		{
			name: "bad-aggregation-method",
			evalset: `apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata: {id: acme/suite, name: S, version: 1.0.0, assetClass: video}
spec:
  members:
    - metric: acme/good
  aggregation:
    method: bogus
`,
			wantError: "reserved methods",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := validatePackDir(t, base(tc.evalset))
			if tc.wantError == "" {
				if rep.HasErrors() {
					t.Fatalf("want no errors, got:\n%s", errorMessages(rep))
				}
				return
			}
			if !strings.Contains(errorMessages(rep), tc.wantError) {
				t.Fatalf("want error containing %q, got:\n%s", tc.wantError, errorMessages(rep))
			}
		})
	}
}

// an unresolved same-tree member reference is a WARNING, not an error.
func TestValidatePackEvalSetUnresolvedMemberWarns(t *testing.T) {
	evalset := `apiVersion: mizan.dev/v1alpha1
kind: EvalSet
metadata: {id: acme/suite, name: S, version: 1.0.0, assetClass: video}
spec:
  members:
    - metric: acme/missing
`
	rep := validatePackDir(t, map[string]string{
		"templates/good.yaml": validTemplate,
		"evalsets/s.yaml":     evalset,
	})
	if rep.HasErrors() {
		t.Fatalf("unresolved member must warn, not error, got errors:\n%s", errorMessages(rep))
	}
	if !strings.Contains(warningMessages(rep), "resolves to no template") {
		t.Fatalf("want unresolved-member warning, got warnings:\n%s", warningMessages(rep))
	}
}

// a well-formed suite (member resolves in-tree) passes with no findings.
func TestValidatePackEvalSetWellFormedPasses(t *testing.T) {
	rep := validatePackDir(t, map[string]string{
		"templates/good.yaml": validTemplate,
		"evalsets/s.yaml":     validEvalSet,
	})
	if rep.HasErrors() {
		t.Fatalf("well-formed suite should pass, got:\n%s", errorMessages(rep))
	}
	for _, f := range rep.Findings {
		if strings.Contains(f.Message, "resolves to no template") {
			t.Fatalf("in-tree member should resolve, got warning: %s", f.Message)
		}
	}
}

// unknown manifest kind is an error.
func TestValidatePackUnknownKind(t *testing.T) {
	rep := validatePackDir(t, map[string]string{
		"templates/x.yaml": `apiVersion: mizan.dev/v1alpha1
kind: Frobnicate
metadata: {id: acme/x, version: 1.0.0}
spec: {}
`,
	})
	if !strings.Contains(errorMessages(rep), "unknown manifest kind") {
		t.Fatalf("want unknown-kind error, got:\n%s", errorMessages(rep))
	}
}

// a repo tree with packs/ is discovered the same as a single pack dir.
func TestValidatePackDiscoversPacksTree(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"packs/acme/templates/good.yaml": validTemplate,
	})
	rep, err := ValidatePack(root)
	if err != nil {
		t.Fatalf("ValidatePack: %v", err)
	}
	if rep.HasErrors() {
		t.Fatalf("want clean, got:\n%s", errorMessages(rep))
	}
	if len(rep.Templates) != 1 {
		t.Fatalf("want 1 parsed template for dry-run carriage, got %d", len(rep.Templates))
	}
}

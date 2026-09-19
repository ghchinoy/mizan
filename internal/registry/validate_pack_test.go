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
		// --- kind: heuristic (B2, design §4.B) -------------------------------
		{
			name: "heuristic-contains-valid",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/contains, version: 1.0.0, description: ok}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  heuristic:
    type: contains
    target: response
    value: "OK"
`),
		},
		{
			name: "heuristic-regex-valid",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/regex, version: 1.0.0, description: ok}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  heuristic:
    type: regex
    target: response
    value: "^[0-9]{3}$"
`),
		},
		{
			name: "heuristic-json-schema-valid",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/schema, version: 1.0.0, description: ok}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  heuristic:
    type: json-schema-valid
    target: response
    schema: "{\"type\":\"object\",\"required\":[\"a\"]}"
`),
		},
		{
			name: "heuristic-missing-spec",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
`),
			wantError: "requires spec.heuristic",
		},
		{
			name: "heuristic-bad-type",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
  heuristic:
    type: bogus
    target: response
`),
			wantError: "spec.heuristic.type is invalid",
		},
		{
			name: "heuristic-missing-target",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
  heuristic:
    type: contains
    value: "OK"
`),
			wantError: "requires spec.heuristic.target",
		},
		{
			name: "heuristic-target-not-in-inputs",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
  heuristic:
    type: contains
    target: missing
    value: "OK"
`),
			wantError: "is not declared in spec.inputs",
		},
		{
			name: "heuristic-contains-missing-value",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
  heuristic:
    type: contains
    target: response
`),
			wantError: "requires a non-empty spec.heuristic.value",
		},
		{
			name: "heuristic-bad-regex",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
  heuristic:
    type: regex
    target: response
    value: "("
`),
			wantError: "not a valid RE2 regex",
		},
		{
			name: "heuristic-json-schema-missing-schema",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
  heuristic:
    type: json-schema-valid
    target: response
`),
			wantError: "requires a non-empty spec.heuristic.schema",
		},
		{
			name: "heuristic-forbids-rubric",
			files: tmpl(`apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: heuristic
  modalities: [text]
  inputs:
    - {name: response, modality: text}
  heuristic:
    type: contains
    target: response
    value: "OK"
  rubricGroups:
    quality: ["clear"]
`),
			wantError: "rubric-only",
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

// --- B1 (audit HIGH-1): a symlinked templates/ or evalsets/ dir is NOT
// followed. Pack discovery runs on untrusted PR content; a symlinked subdir
// pointing out-of-tree must be skipped, not read (CWE-59/22), so no out-of-tree
// file is read and no out-of-tree value leaks into findings.
func TestValidatePackDirSymlinkNotTraversed(t *testing.T) {
	if _, err := os.Lstat("/dev/null"); err != nil { // sanity: symlinks usable
		t.Skip("symlinks not usable on this platform")
	}
	// An out-of-tree directory holding a manifest with a sentinel value that
	// would otherwise be echoed verbatim into a finding.
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{
		"leak.yaml": `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: SENSITIVE-VALUE-ghp_LEAKME12345, version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`,
	})

	pack := t.TempDir()
	// Symlink templates/ (and evalsets/) at the out-of-tree dir.
	if err := os.Symlink(outside, filepath.Join(pack, "templates")); err != nil {
		t.Fatalf("symlink templates: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(pack, "evalsets")); err != nil {
		t.Fatalf("symlink evalsets: %v", err)
	}

	rep, err := ValidatePack(pack)
	// No manifests are discovered (both subdirs skipped) -> discovery error is
	// acceptable ("no packs found"); crucially the out-of-tree file must NOT be
	// read and its value must NOT appear anywhere.
	if err == nil {
		if len(rep.Findings) != 0 {
			t.Fatalf("symlinked subdir should yield no findings, got:\n%s", errorMessages(rep))
		}
		if len(rep.Templates) != 0 {
			t.Fatalf("symlinked subdir should carry no templates, got %d", len(rep.Templates))
		}
	}
	// The sentinel value must never surface, whether via error or Report.
	if err != nil && strings.Contains(err.Error(), "SENSITIVE-VALUE") {
		t.Fatalf("out-of-tree value leaked into error: %v", err)
	}
	if rep != nil {
		for _, f := range rep.Findings {
			if strings.Contains(f.Message, "SENSITIVE-VALUE") || strings.Contains(f.ID, "SENSITIVE-VALUE") {
				t.Fatalf("out-of-tree value leaked into finding: %+v", f)
			}
		}
	}
}

// --- B1 fix-pass-2 (audit HIGH-1, still-open bypass): a symlinked packs/ dir
// pointing out-of-tree must NOT be traversed. This is the intermediate-symlink
// bypass the first fix missed (os.Lstat only guarded the final component). No
// out-of-tree file may be read and the sentinel value must never leak.
func TestValidatePackSymlinkedPacksDirNotTraversed(t *testing.T) {
	if _, err := os.Lstat("/dev/null"); err != nil {
		t.Skip("symlinks not usable on this platform")
	}
	// An out-of-tree pack tree with a sentinel id.
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{
		"p1/templates/leak.yaml": `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: LEAKED-ghp_STILLLEAKING999, version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`,
	})

	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "packs")); err != nil {
		t.Fatalf("symlink packs: %v", err)
	}

	rep, err := ValidatePack(root)
	// A symlinked packs/ is ignored -> discovery falls back to root-as-pack,
	// which has no templates/evalsets -> a clean empty report (no error, no
	// findings). Crucially the sentinel must never surface.
	if err != nil && strings.Contains(err.Error(), "LEAKED") {
		t.Fatalf("out-of-tree value leaked into error: %v", err)
	}
	if rep != nil {
		for _, f := range rep.Findings {
			if strings.Contains(f.Message, "LEAKED") || strings.Contains(f.ID, "LEAKED") {
				t.Fatalf("out-of-tree value leaked into finding: %+v", f)
			}
		}
		if len(rep.Templates) != 0 {
			t.Fatalf("symlinked packs/ should carry no templates, got %d", len(rep.Templates))
		}
	}
}

// --- containedPath is the durable containment guard: a path whose resolved
// form escapes the resolved root is refused; an in-tree path is allowed. This
// locks the catch-all that defends ANY intermediate symlink component.
func TestContainedPath(t *testing.T) {
	if _, err := os.Lstat("/dev/null"); err != nil {
		t.Skip("symlinks not usable on this platform")
	}
	root := t.TempDir()
	resolvedRoot, err := resolveReal(root)
	if err != nil {
		t.Fatalf("resolveReal(root): %v", err)
	}

	// An in-tree regular file is contained.
	inTree := filepath.Join(root, "templates", "t.yaml")
	writeFiles(t, root, map[string]string{"templates/t.yaml": "x: 1\n"})
	if ok, err := containedPath(resolvedRoot, inTree); err != nil || !ok {
		t.Fatalf("in-tree path should be contained (ok=%v, err=%v)", ok, err)
	}

	// A symlink inside root pointing at an out-of-tree file is NOT contained.
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"secret.yaml": "s: 1\n"})
	escape := filepath.Join(root, "escape.yaml")
	if err := os.Symlink(filepath.Join(outside, "secret.yaml"), escape); err != nil {
		t.Fatalf("symlink escape: %v", err)
	}
	if ok, _ := containedPath(resolvedRoot, escape); ok {
		t.Fatalf("out-of-tree symlink target must NOT be contained")
	}

	// An intermediate symlinked DIRECTORY component also escapes.
	dirEscape := filepath.Join(root, "linkdir")
	if err := os.Symlink(outside, dirEscape); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if ok, _ := containedPath(resolvedRoot, filepath.Join(dirEscape, "secret.yaml")); ok {
		t.Fatalf("path through an intermediate symlinked dir must NOT be contained")
	}

	// A broken/dangling symlink fails closed (not contained).
	dangling := filepath.Join(root, "dangling.yaml")
	if err := os.Symlink(filepath.Join(outside, "does-not-exist.yaml"), dangling); err != nil {
		t.Fatalf("symlink dangling: %v", err)
	}
	if ok, err := containedPath(resolvedRoot, dangling); ok || err == nil {
		t.Fatalf("dangling symlink should fail closed (ok=%v, err=%v)", ok, err)
	}
}

// --- LOW-1 (audit): a project-scoped or ".."-bearing autorater.model is an
// ERROR at validate, mirroring the ingest guard (so the creds-free gate agrees
// with what import will reject, no false-clean).
func TestValidatePackRejectsProjectScopedAutoraterModel(t *testing.T) {
	spec := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0, description: d, license: Apache-2.0}
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "Rate {{response}}"
  autorater:
    model: projects/victim/locations/us/publishers/google/models/gemini
`
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": spec})
	if !strings.Contains(errorMessages(rep), "autorater.model must be a publisher-relative id") {
		t.Fatalf("want project-scoped autorater.model error, got:\n%s", errorMessages(rep))
	}
}

// --- MEDIUM (test): strict additionalProperties rejection at every closed
// level (metadata, top-level, nested) plus a schema id-pattern violation.
func TestValidatePackStrictSchemaRejectsUnknownKeys(t *testing.T) {
	cases := []struct {
		name      string
		spec      string
		wantError string
	}{
		{
			name: "unknown-key-at-metadata",
			spec: `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0, bogusMeta: nope}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`,
			wantError: "schema",
		},
		{
			name: "unknown-key-at-top-level",
			spec: `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
bogusTop: nope
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`,
			wantError: "schema",
		},
		{
			name: "unknown-key-nested-in-input",
			spec: `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true, bogusInput: nope}
  metricPromptTemplate: "Rate {{response}}"
`,
			wantError: "schema",
		},
		{
			name: "schema-id-pattern-violation",
			spec: `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: "Bad Id", version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`,
			wantError: "schema",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := validatePackDir(t, map[string]string{"templates/t.yaml": tc.spec})
			if !strings.Contains(errorMessages(rep), tc.wantError) {
				t.Fatalf("want a %q error, got:\n%s", tc.wantError, errorMessages(rep))
			}
		})
	}
}

// --- LOW (test): step-4 "required input never referenced" branch.
func TestValidatePackRequiredInputNeverReferenced(t *testing.T) {
	spec := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "no placeholder here"
`
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": spec})
	if !strings.Contains(errorMessages(rep), "is never referenced") {
		t.Fatalf("want required-input-never-referenced error, got:\n%s", errorMessages(rep))
	}
}

// --- LOW (test): an invalid TEMPLATE metadata.id pattern at the Go level (the
// id-shape guard, distinct from the schema-level pattern above).
func TestValidatePackInvalidTemplateID(t *testing.T) {
	spec := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: no-slash-here, version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": spec})
	if !strings.Contains(errorMessages(rep), "invalid template id") {
		t.Fatalf("want invalid-template-id error, got:\n%s", errorMessages(rep))
	}
}

// --- LOW (test): a manifest with no kind: at all.
func TestValidatePackMissingManifestKind(t *testing.T) {
	spec := `apiVersion: mizan.dev/v1alpha1
metadata: {id: acme/x, version: 1.0.0}
spec:
  kind: pointwise
  metricPromptTemplate: "hi"
`
	rep := validatePackDir(t, map[string]string{"templates/t.yaml": spec})
	if !strings.Contains(errorMessages(rep), "missing manifest kind") {
		t.Fatalf("want missing-manifest-kind error, got:\n%s", errorMessages(rep))
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

func TestValidatePackBoul(t *testing.T) {
	valid := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/boul-valid, version: 1.0.0, license: Apache-2.0}
spec:
  kind: boul
  modalities: [text]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "Is this safe? {{response}}"
`
	rep := validatePackDir(t, map[string]string{"templates/boul.yaml": valid})
	if rep.HasErrors() {
		t.Fatalf("want clean, got:\n%s", errorMessages(rep))
	}

	withChoices := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/boul-bad, version: 1.0.0, license: Apache-2.0}
spec:
  kind: boul
  modalities: [text]
  choices: [a, b]
  inputs:
    - {name: response, modality: text, required: true}
  metricPromptTemplate: "Is this safe? {{response}}"
`
	repBad := validatePackDir(t, map[string]string{"templates/boul.yaml": withChoices})
	if !strings.Contains(errorMessages(repBad), "must not set spec.choices") {
		t.Fatalf("want error forbidding choices on boul, got:\n%s", errorMessages(repBad))
	}
}

func TestValidatePackChoice(t *testing.T) {
	valid := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/choice-valid, version: 1.0.0, license: Apache-2.0}
spec:
  kind: choice
  modalities: [text]
  choices: [billing, technical, sales]
  inputs:
    - {name: message, modality: text, required: true}
  metricPromptTemplate: "Classify: {{message}}"
`
	rep := validatePackDir(t, map[string]string{"templates/choice.yaml": valid})
	if rep.HasErrors() {
		t.Fatalf("want clean, got:\n%s", errorMessages(rep))
	}

	dupChoices := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/choice-dup, version: 1.0.0, license: Apache-2.0}
spec:
  kind: choice
  modalities: [text]
  choices: [billing, technical, billing]
  inputs:
    - {name: message, modality: text, required: true}
  metricPromptTemplate: "Classify: {{message}}"
`
	repDup := validatePackDir(t, map[string]string{"templates/choice.yaml": dupChoices})
	if !strings.Contains(errorMessages(repDup), "duplicate choice") {
		t.Fatalf("want duplicate choice error, got:\n%s", errorMessages(repDup))
	}

	tooFewChoices := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/choice-few, version: 1.0.0, license: Apache-2.0}
spec:
  kind: choice
  modalities: [text]
  choices: [onlyone]
  inputs:
    - {name: message, modality: text, required: true}
  metricPromptTemplate: "Classify: {{message}}"
`
	repFew := validatePackDir(t, map[string]string{"templates/choice.yaml": tooFewChoices})
	if !strings.Contains(errorMessages(repFew), "at least 2") {
		t.Fatalf("want at least 2 choices error, got:\n%s", errorMessages(repFew))
	}
}

func TestValidatePackScore(t *testing.T) {
	valid := `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata: {id: acme/score-valid, version: 1.0.0, license: Apache-2.0}
spec:
  kind: score
  modalities: [text]
  inputs:
    - {name: text, modality: text, required: true}
  metricPromptTemplate: "Grade from 1 to 10: {{text}}"
`
	rep := validatePackDir(t, map[string]string{"templates/score.yaml": valid})
	if rep.HasErrors() {
		t.Fatalf("want clean, got:\n%s", errorMessages(rep))
	}
}

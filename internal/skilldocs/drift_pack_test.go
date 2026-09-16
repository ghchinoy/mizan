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

package skilldocs

// This is the `pack validate` contract gate for the
// author-and-validate-a-template-pack skill (plugins/mizan-authoring), the
// sibling of drift_test.go's eval.Result gate for run-eval. `pack validate` is
// TEXT/EXIT-CODE driven, not `-o json` (§8 of the skills scope: it "ignores -o
// json"), so there is NO json key-set to diff. Instead this gate proves the two
// contracts the skill documents actually match the CLI:
//
//  1. EXIT CODE: 0 = accept (no ERROR findings) / non-zero = reject (>=1 ERROR).
//     cmd/mizan/pack.go returns an error iff report.HasErrors(), so this gate
//     drives the SAME registry.ValidatePack the CLI calls over hermetic valid
//     and invalid pack fixtures and asserts HasErrors()/Errors() match the
//     accept/reject decision the skill documents.
//  2. TEXT REPORT: the findings-by-file lines ("  [ERROR] ..." / "  [warn ] ..."),
//     the clean-pack line ("OK: no defects found."), and the trailing
//     "N error(s), M warning(s)" summary. cmd/mizan.renderValidateReport lives in
//     package main (not importable), so — exactly as drift_test.go replicates the
//     json encoder — this gate replicates that renderer AND asserts its literal
//     format strings still exist verbatim in cmd/mizan/pack.go, so the replica
//     cannot silently diverge from the real CLI. It then renders the real
//     fixtures' reports and asserts every token the SKILL.md documents appears in
//     the real output.
//
// It is HERMETIC by construction: registry.ValidatePack reads only the
// filesystem (steps 1–5 are creds-free by design; --dry-run is NOT exercised),
// so no network or ADC is required. A synthetic-drift self-test proves the
// comparison actually discriminates accept from reject and would catch a wrong
// documented token.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ghchinoy/mizan/internal/registry"
)

// packSkillRelPath is the author-and-validate-a-template-pack SKILL.md relative
// to this test file. This package lives at <repo>/internal/skilldocs, so "../.."
// is the repo root.
var packSkillRelPath = filepath.Join(
	"..", "..",
	"plugins", "mizan-authoring", "skills",
	"author-and-validate-a-template-pack", "SKILL.md",
)

// packCmdRelPath is cmd/mizan/pack.go relative to this test file — the source of
// truth for the text-report renderer whose format strings this gate anchors to.
var packCmdRelPath = filepath.Join("..", "..", "cmd", "mizan", "pack.go")

// repoFile reads a repo-relative path resolved from this test file's own
// location, so the gate is independent of the working directory `go test` runs in.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate repo file")
	}
	p := filepath.Join(filepath.Dir(thisFile), rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// A valid pointwise MetricTemplate manifest (mirrors the golden fixtures under
// internal/registry/testdata) — parses cleanly, so ValidatePack finds no ERROR.
const validTemplateYAML = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
    id: acme/pointwise-quality
    name: Pointwise Quality
    description: Scores response quality.
    version: 1.0.0
    license: Apache-2.0
spec:
    kind: pointwise
    modalities:
        - text
    inputs:
        - name: response
          modality: text
          required: true
    metricPromptTemplate: 'Rate the response: {{response}}'
    autorater:
        model: gemini-2.5-pro
`

// The same VALID template with metadata.version REMOVED — a guaranteed
// ERROR-severity finding ("metadata.version is required and must be semver",
// validate.go). Every other field (description/license/autorater) is kept so the
// only finding is that single error, giving a clean "1 error(s), 0 warning(s)"
// summary the reject-case assertions anchor to.
const invalidTemplateYAML = `apiVersion: mizan.dev/v1alpha1
kind: MetricTemplate
metadata:
    id: acme/pointwise-quality
    name: Pointwise Quality
    description: Scores response quality.
    license: Apache-2.0
spec:
    kind: pointwise
    modalities:
        - text
    inputs:
        - name: response
          modality: text
          required: true
    metricPromptTemplate: 'Rate the response: {{response}}'
    autorater:
        model: gemini-2.5-pro
`

// writePackFixture creates a single-pack dir (with a templates/ subdir holding
// one manifest) under a fresh temp dir and returns its path. A dir containing
// templates/ is a valid pack root for ValidatePack.
func writePackFixture(t *testing.T, templateYAML string) string {
	t.Helper()
	root := t.TempDir()
	tmplDir := filepath.Join(root, "templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir templates: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "template.yaml"), []byte(templateYAML), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	return root
}

// renderValidateReport replicates cmd/mizan.renderValidateReport exactly. Its
// literal format strings are asserted to still exist in cmd/mizan/pack.go by
// TestPackValidateTextReportMatchesCLISource, so this replica cannot silently
// drift from the real CLI renderer (the same discipline drift_test.go uses to
// replicate the json encoder).
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

func renderReportString(t *testing.T, report *registry.Report) string {
	t.Helper()
	var buf bytes.Buffer
	renderValidateReport(&buf, report)
	return buf.String()
}

// TestPackValidateExitCodeContract proves the accept/reject exit-code semantics
// the SKILL.md documents match the CLI. cmd/mizan/pack.go returns nil (exit 0)
// iff !report.HasErrors() and returns an error (non-zero exit) otherwise, so the
// authoritative signal is registry.ValidatePack's HasErrors() over the same
// fixtures. A pack with only warnings must still be accepted (warnings never
// fail).
func TestPackValidateExitCodeContract(t *testing.T) {
	valid := writePackFixture(t, validTemplateYAML)
	invalid := writePackFixture(t, invalidTemplateYAML)

	vrep, err := registry.ValidatePack(valid)
	if err != nil {
		t.Fatalf("ValidatePack(valid): %v", err)
	}
	if vrep.HasErrors() {
		t.Errorf("valid fixture reported errors (%d); the SKILL documents exit 0 = accept for an error-free pack:\n%s",
			vrep.Errors(), renderReportString(t, vrep))
	}

	irep, err := registry.ValidatePack(invalid)
	if err != nil {
		t.Fatalf("ValidatePack(invalid): %v", err)
	}
	if !irep.HasErrors() {
		t.Errorf("invalid fixture (missing metadata.version) reported no errors; the SKILL documents non-zero = reject on ERROR:\n%s",
			renderReportString(t, irep))
	}

	// The SKILL must document BOTH sides of the exit-code contract.
	skill := repoFile(t, packSkillRelPath)
	for _, want := range []string{"Exit code `0` = accept", "non-zero", "reject"} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md does not document the exit-code contract token %q", want)
		}
	}
}

// TestPackValidateTextReportContract renders the real fixtures' reports through
// the CLI-faithful renderer and asserts every text token the SKILL.md documents
// actually appears in the real output.
func TestPackValidateTextReportContract(t *testing.T) {
	vrep, err := registry.ValidatePack(writePackFixture(t, validTemplateYAML))
	if err != nil {
		t.Fatalf("ValidatePack(valid): %v", err)
	}
	irep, err := registry.ValidatePack(writePackFixture(t, invalidTemplateYAML))
	if err != nil {
		t.Fatalf("ValidatePack(invalid): %v", err)
	}
	validOut := renderReportString(t, vrep)
	invalidOut := renderReportString(t, irep)
	skill := repoFile(t, packSkillRelPath)

	// Clean-pack line and zero-count summary must be both documented AND emitted.
	for _, tok := range []string{"OK: no defects found.", "0 error(s), 0 warning(s)"} {
		if !strings.Contains(validOut, tok) {
			t.Errorf("real clean-pack report does not contain %q:\n%s", tok, validOut)
		}
		if !strings.Contains(skill, tok) {
			t.Errorf("SKILL.md does not document the clean-pack token %q", tok)
		}
	}
	// Reject report: the [ERROR] finding line and the singular summary.
	for _, tok := range []string{"[ERROR]", "1 error(s), 0 warning(s)"} {
		if !strings.Contains(invalidOut, tok) {
			t.Errorf("real reject report does not contain %q:\n%s", tok, invalidOut)
		}
		if !strings.Contains(skill, tok) {
			t.Errorf("SKILL.md does not document the reject token %q", tok)
		}
	}
	// The [warn ] marker and the "N error(s), M warning(s)" summary shape must be
	// documented (warnings are advisory, never failing).
	for _, tok := range []string{"[warn ]", "error(s)", "warning(s)"} {
		if !strings.Contains(skill, tok) {
			t.Errorf("SKILL.md does not document the text-report token %q", tok)
		}
	}
}

// TestPackValidateTextReportMatchesCLISource anchors the replicated renderer to
// the real one: every literal format string renderValidateReport (above) relies
// on must still exist verbatim in cmd/mizan/pack.go. If the CLI renderer changes
// a token, this fails until both the replica and the SKILL.md are updated —
// closing the gap that a pure replica could pass while the CLI drifted.
func TestPackValidateTextReportMatchesCLISource(t *testing.T) {
	src := repoFile(t, packCmdRelPath)
	for _, lit := range []string{
		`"OK: no defects found."`,
		`"\n%d error(s), %d warning(s)\n"`,
		`"  [%s] %s\n"`,
		`marker := "ERROR"`,
		`marker = "warn "`,
	} {
		if !strings.Contains(src, lit) {
			t.Errorf("cmd/mizan/pack.go no longer contains the renderer literal %s; "+
				"the replicated renderer and SKILL.md text contract may have drifted", lit)
		}
	}
	// The exit-code rule itself: RunE returns an error iff report.HasErrors().
	for _, lit := range []string{"report.HasErrors()", `"pack validate: %d error(s) found"`} {
		if !strings.Contains(src, lit) {
			t.Errorf("cmd/mizan/pack.go no longer contains the exit-code rule literal %s", lit)
		}
	}
}

// TestPackValidateIgnoresJSON proves the scope invariant (§8) that `pack
// validate` ignores `-o json`: the SKILL must tell the agent to read text + exit
// code, and the CLI's validate RunE must render text unconditionally (no
// outputFormat/json branch).
func TestPackValidateIgnoresJSON(t *testing.T) {
	skill := repoFile(t, packSkillRelPath)
	if !strings.Contains(skill, "-o json") || !strings.Contains(skill, "ignored") {
		t.Error("SKILL.md must document that `pack validate` ignores `-o json` (read text + exit code)")
	}
	// The validate command body must not branch on JSON output — it always renders
	// the text report. (renderValidateReport is the only render path.)
	src := repoFile(t, packCmdRelPath)
	vi := strings.Index(src, "func newPackValidateCmd(")
	if vi < 0 {
		t.Fatal("cannot find newPackValidateCmd in cmd/mizan/pack.go")
	}
	ve := strings.Index(src[vi:], "\nfunc ")
	body := src[vi:]
	if ve > 0 {
		body = src[vi : vi+ve]
	}
	if strings.Contains(body, "outputJSON") || strings.Contains(body, "printJSON") {
		t.Error("newPackValidateCmd appears to branch on JSON output; pack validate must render text unconditionally")
	}
	if !strings.Contains(body, "renderValidateReport") {
		t.Error("newPackValidateCmd no longer calls renderValidateReport; the documented text contract may have moved")
	}
}

// TestPackValidateDriftSelfTest is the synthetic-drift self-test: it proves the
// gate above actually discriminates, so a real drift could not slip through.
// (1) accept and reject fixtures must produce different error counts; a check
// that could not tell them apart would be worthless. (2) a token that is NOT part
// of the real report must be absent from the real output — proving the token
// assertions in the other tests are not trivially satisfiable.
func TestPackValidateDriftSelfTest(t *testing.T) {
	vrep, err := registry.ValidatePack(writePackFixture(t, validTemplateYAML))
	if err != nil {
		t.Fatalf("ValidatePack(valid): %v", err)
	}
	irep, err := registry.ValidatePack(writePackFixture(t, invalidTemplateYAML))
	if err != nil {
		t.Fatalf("ValidatePack(invalid): %v", err)
	}
	if vrep.Errors() != 0 {
		t.Errorf("self-test: expected 0 errors for the valid fixture, got %d", vrep.Errors())
	}
	if irep.Errors() == 0 {
		t.Error("self-test: expected >=1 error for the invalid fixture, got 0 — the gate cannot discriminate accept from reject")
	}
	if vrep.HasErrors() == irep.HasErrors() {
		t.Error("self-test: valid and invalid fixtures share the same HasErrors() verdict — the exit-code gate is not discriminating")
	}
	// A fabricated token must NOT appear in the real clean-pack output.
	validOut := renderReportString(t, vrep)
	if strings.Contains(validOut, "no problems detected") {
		t.Error("self-test: a fabricated token unexpectedly matched the real report; token assertions would be trivially satisfiable")
	}
}

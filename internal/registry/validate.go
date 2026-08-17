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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	yaml "gopkg.in/yaml.v3"

	"github.com/ghchinoy/mizan/internal/registry/pack"
)

// validate.go is the ingest-boundary validation for UNTRUSTED pack content
// (design §3.4, trust model D5). It runs inside the codec at Unmarshal — the
// single chokepoint every import path passes through — so the guarantees hold at
// rest in the store (the shared substrate for the CLI, GUI, and future remote
// registry) regardless of any downstream runtime checks. P2.2's `pack validate`
// pipeline is intended to reuse these as its one source of truth.
//
// NOTE: internal/eval keeps an equivalent RUNTIME guard on the resolved model
// (eval.ValidateModel / bareModelID); this ingest guard is intentionally the
// earlier, preventive copy. registry must not import eval (that would be an
// import cycle — eval imports registry), so the small allowlist below is mirrored
// here deliberately; P2.2 may consolidate both behind one shared validator.

// templateIDPattern is the required shape of a template metadata.id:
// "<namespace>/<slug>" where each segment starts with a lowercase letter or
// digit and continues with lowercase letters, digits, and hyphens (the design's
// namespaced-slug discipline). Enforcing it at ingest closes terminal-injection
// on `registry list/get` (no control/escape chars land in the primary key) and
// the latent P2.4 export path-traversal seed (no '/' beyond the single
// separator, no "..", no control chars) BEFORE fan-out.
//
// This is the SINGLE source of truth for the id shape: it is kept in lockstep
// with the identical `pattern` in schema/metrictemplate.json and
// schema/evalset.json (P2.1 review flagged a slight divergence — the schema
// required a leading alphanumeric, this regex did not; they are now one
// pattern). The `pack validate` id checks and this ingest guard therefore agree.
const templateIDRegex = `^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`

var templateIDPattern = regexp.MustCompile(templateIDRegex)

// ValidateTemplateID is the exported wrapper over validateTemplateID so frontends
// (cmd/*) can validate a user-supplied id (e.g. `eval adaptive --save-as`) with
// the SAME single source of truth the ingest boundary uses — no hand-rolled id
// validation elsewhere.
func ValidateTemplateID(id string) error { return validateTemplateID(id) }

// validateTemplateID rejects a metadata.id that is empty or not of the
// "<namespace>/<slug>" shape.
func validateTemplateID(id string) error {
	if id == "" {
		return fmt.Errorf("registry: template metadata.id is required")
	}
	if !templateIDPattern.MatchString(id) {
		return fmt.Errorf("registry: invalid template id %q: must be \"<namespace>/<slug>\" of lowercase letters, digits, and hyphens", id)
	}
	return nil
}

// bareModelPattern is the conservative allowlist a bare (publisher-relative)
// autorater model id must match: letters/digits with '.', '_' and '-'
// separators (e.g. "gemini-2.5-pro"). It mirrors eval's bareModelPattern.
var bareModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// bareModelID strips any "publishers/.../<id>" (or other slash-bearing) prefix,
// returning the trailing publisher-relative id (e.g. "gemini-2.5-pro").
func bareModelID(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

// validateAutoraterModel enforces the publisher-relative-only invariant on an
// untrusted pack's spec.autorater.model (design §3.4) and returns the cleaned
// BARE id to store at rest.
//
// The eval engine TRUSTS any model beginning with "projects/" and forwards it
// verbatim as the call target; if a pack could smuggle such a value, a hostile
// template could redirect a victim's authenticated eval — carrying their
// prompt/inputs — to an attacker-controlled project (CWE-918). So a pack may
// carry only a bare id or a "publishers/.../<id>" form: a project-scoped resource
// name, or any "..", is rejected here, and only the bare id is stored.
func validateAutoraterModel(id, model string) (string, error) {
	if model == "" {
		return "", nil
	}
	if strings.HasPrefix(model, "projects/") {
		return "", fmt.Errorf("registry: template %q: autorater.model must be a publisher-relative id, not a project-scoped resource name (%q)", id, model)
	}
	if strings.Contains(model, "..") {
		return "", fmt.Errorf("registry: template %q: autorater.model %q must not contain %q", id, model, "..")
	}
	bare := bareModelID(model)
	if !bareModelPattern.MatchString(bare) {
		return "", fmt.Errorf("registry: template %q: invalid autorater.model %q: expected a bare publisher model id such as %q (letters, digits, '.', '_', '-')", id, model, "gemini-2.5-pro")
	}
	return bare, nil
}

// =============================================================================
// pack validate pipeline (P2.2, design §3.5 + §3.4a)
//
// ValidatePack is the creds-free PR gate + local pre-import check. It runs, per
// item (template or evalset), steps 1–5 fail-fast-per-item but report-ALL:
//   (1) structural, against the embedded JSON Schema (strict, additionalProperties)
//   (2) identity (namespaced-slug id, semver version, unique-in-pack)
//   (3) semantic / kind-specific
//   (4) placeholder consistency
//   (5) lint (WARNINGS, non-fatal)
// Step (6), the opt-in `--dry-run` live materialize+call, is NOT here: it needs
// creds and the eval engine, and registry must not import eval (import cycle).
// The CLI (cmd/mizan/pack.go) drives step 6 over the templates ValidatePack
// returns, keeping steps 1–5 pure and creds-free.
// =============================================================================

// Severity classifies a validation Finding. Errors fail the pack (non-zero
// exit); warnings are advisory (lint) and never fail.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one validation defect (or advisory) about one file. File is the
// path relative to the validated root (or absolute if it lies outside), ID is
// the manifest's metadata.id when known.
type Finding struct {
	File     string
	ID       string
	Severity Severity
	Message  string
}

// Report is the accumulated result of validating a pack tree. It carries every
// Finding (errors + warnings) and, separately, the templates that parsed cleanly
// enough for the optional --dry-run live probe the CLI runs on top.
type Report struct {
	Findings  []Finding
	Templates []MetricTemplate
}

func (r *Report) add(file, id string, sev Severity, format string, args ...any) {
	r.Findings = append(r.Findings, Finding{
		File:     file,
		ID:       id,
		Severity: sev,
		Message:  fmt.Sprintf(format, args...),
	})
}

// HasErrors reports whether any Finding is an error (warnings alone do not fail).
func (r *Report) HasErrors() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Errors returns the number of error-severity findings.
func (r *Report) Errors() int { return r.count(SeverityError) }

// Warnings returns the number of warning-severity findings.
func (r *Report) Warnings() int { return r.count(SeverityWarning) }

func (r *Report) count(sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

// reservedAggregationMethods is the reserved enum for an EvalSet's
// spec.aggregation.method (design §3.4a). P2 does not interpret aggregation, but
// it validates that a declared method is one of these reserved tokens so the
// carried format stays trustworthy for the future runner. Kept in lockstep with
// the same enum in schema/evalset.json.
var reservedAggregationMethods = map[string]bool{
	"mean":          true,
	"weighted-mean": true,
	"min":           true,
	"max":           true,
	"median":        true,
	"sum":           true,
}

// Compiled schemas, embedded and compiled once at init. A compile failure is a
// programming error in the embedded JSON (caught by tests), so panic is correct.
var (
	metricTemplateSchema = mustCompileSchema("metrictemplate.json", MetricTemplateSchemaJSON)
	evalSetSchema        = mustCompileSchema("evalset.json", EvalSetSchemaJSON)
)

func mustCompileSchema(name string, data []byte) *jsonschema.Schema {
	c := jsonschema.NewCompiler()
	if err := c.AddResource(name, bytes.NewReader(data)); err != nil {
		panic(fmt.Sprintf("registry: add schema %q: %v", name, err))
	}
	s, err := c.Compile(name)
	if err != nil {
		panic(fmt.Sprintf("registry: compile schema %q: %v", name, err))
	}
	return s
}

// semverPattern is the advisory semver check (design D1): P2 validates that a
// version PARSES as semver; it does NOT diff against history. Accepts
// MAJOR.MINOR.PATCH with optional -prerelease and +build (SemVer 2.0.0 shape).
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// placeholderPattern matches a {{name}} placeholder (optionally spaced).
var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// manifestHeader is the minimal shape read to dispatch a file on its kind.
type manifestHeader struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// ValidatePack validates every MetricTemplate and EvalSet manifest under root.
// root may be a repo/tree containing a top-level packs/ directory (each subdir a
// pack) OR a single pack directory (one that contains templates/ and/or
// evalsets/). It returns a Report with all findings (never nil) and an error
// only for an I/O/discovery failure that prevents validation from running at all
// (a defective manifest is a Finding, not an error).
func ValidatePack(root string) (*Report, error) {
	rep := &Report{}
	if root == "" {
		return nil, errors.New("registry: validate: empty path")
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("registry: validate %q: %w", root, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("registry: validate %q: not a directory", root)
	}

	// Resolve the validated root once, following any symlinks in the path the
	// USER supplied (that is legitimate — they chose it). Every manifest we
	// later read must, with ITS symlinks resolved, stay under this resolved
	// root; see containedPath. This is the durable containment that defends
	// against ANY intermediate symlink component (a symlinked packs/, a
	// symlinked pack dir, etc.), not just the final-component guards below.
	resolvedRoot, err := resolveReal(root)
	if err != nil {
		return nil, fmt.Errorf("registry: validate %q: %w", root, err)
	}

	packDirs, err := discoverValidatePackDirs(root)
	if err != nil {
		return nil, err
	}
	if len(packDirs) == 0 {
		return nil, fmt.Errorf("registry: validate %q: no packs found (expected a packs/ tree or a pack dir with templates/ or evalsets/)", root)
	}

	// Pass A: validate templates, collect the tree-wide id set for evalset
	// member resolution. Deferred evalset files are validated in pass B.
	treeTemplateIDs := map[string]bool{}
	var evalSetFiles []string

	for _, pd := range packDirs {
		perPackIDs := map[string]string{} // id -> first file that declared it
		files, err := listYAMLFiles(pd)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			// Containment gate: refuse to read a manifest whose resolved path
			// escapes the validated root (symlink traversal). We report the
			// IN-TREE path only (never the out-of-tree file's contents), so a
			// hostile symlink cannot leak an out-of-tree value into CI logs.
			if ok, err := containedPath(resolvedRoot, f); err != nil || !ok {
				rep.add(rel(root, f), "", SeverityError, "refusing to read manifest outside the pack root (symlink escape)")
				continue
			}
			data, kind, err := readManifest(f)
			if err != nil {
				rep.add(rel(root, f), "", SeverityError, "%v", err)
				continue
			}
			switch kind {
			case packKindMetricTemplate:
				id := validateTemplateManifest(rep, rel(root, f), data)
				if id != "" {
					if prev, dup := perPackIDs[id]; dup {
						rep.add(rel(root, f), id, SeverityError, "duplicate template id %q in pack (already declared in %s)", id, prev)
					} else {
						perPackIDs[id] = rel(root, f)
						treeTemplateIDs[id] = true
					}
				}
			case pack.KindEvalSet:
				evalSetFiles = append(evalSetFiles, f)
			case "Pack":
				// The pack manifest (mizan-pack.yaml) is not validated by P2.2.
			default:
				rep.add(rel(root, f), "", SeverityError, "unknown manifest kind %q (want MetricTemplate or EvalSet)", kind)
			}
		}
	}

	// Pass B: validate evalsets against the full template-id universe.
	sort.Strings(evalSetFiles)
	for _, f := range evalSetFiles {
		if ok, err := containedPath(resolvedRoot, f); err != nil || !ok {
			rep.add(rel(root, f), "", SeverityError, "refusing to read manifest outside the pack root (symlink escape)")
			continue
		}
		data, err := readCappedFile(f)
		if err != nil {
			rep.add(rel(root, f), "", SeverityError, "%v", err)
			continue
		}
		validateEvalSetManifest(rep, rel(root, f), data, treeTemplateIDs)
	}

	return rep, nil
}

// ValidateTemplateSchema checks that a single MetricTemplate manifest's bytes
// satisfy the strict JSON Schema (schema/metrictemplate.json) DIRECTLY — the
// structural/JSON-Schema step only, without the identity, kind-specific, or
// placeholder-consistency rules that the full ValidatePack layers on top. It is
// the write-side counterpart used to assert that Export / `pack add` output is
// schema-valid on its own, not merely re-importable, and reuses the same
// compiled schema and YAML→JSON normalization as ValidatePack's structural step.
// It returns nil when the document conforms, or an error describing the
// violation(s).
func ValidateTemplateSchema(data []byte) error {
	v, err := yamlToJSONValue(data)
	if err != nil {
		return fmt.Errorf("registry: parse template: %w", err)
	}
	if err := metricTemplateSchema.Validate(v); err != nil {
		return fmt.Errorf("registry: template violates schema/metrictemplate.json: %w", err)
	}
	return nil
}

// resolveReal returns the absolute, symlink-resolved form of p. It is the
// canonical path used by containedPath so that containment comparisons are made
// between two fully-resolved absolute paths (mixing relative/absolute or
// unresolved/resolved forms would make filepath.Rel unreliable).
func resolveReal(p string) (string, error) {
	ap, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(ap)
}

// containedPath reports whether path, with ALL of its symlink components
// resolved, stays under resolvedRoot (itself already resolved). It is the
// durable defense against out-of-tree traversal by any symlink component in the
// path — not just the final one — closing the creds-free CI threat of reading
// an attacker-committed symlink's target and leaking its value into findings.
// A path that cannot be resolved (dangling/broken symlink, missing file) is
// treated as NOT contained (fail-closed).
func containedPath(resolvedRoot, path string) (bool, error) {
	rp, err := resolveReal(path)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(resolvedRoot, rp)
	if err != nil {
		return false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false, nil
	}
	return true, nil
}

// discoverValidatePackDirs mirrors GitPackBackend.discoverPackDirs: every
// immediate subdir of <root>/packs, or <root> itself as a single pack dir. The
// result is sorted for deterministic reporting.
//
// packsRoot is stat'd with os.Lstat (not os.Stat): a symlinked packs/ dir is
// UNTRUSTED committed content in the creds-free-CI threat model, and following
// it would let an attacker point discovery at an out-of-tree tree. A symlinked
// packs/ is therefore ignored here (defense-in-depth alongside the containedPath
// gate in ValidatePack, which is the durable catch-all for any symlink).
func discoverValidatePackDirs(root string) ([]string, error) {
	packsRoot := filepath.Join(root, "packs")
	if fi, err := os.Lstat(packsRoot); err == nil && fi.IsDir() {
		entries, err := os.ReadDir(packsRoot)
		if err != nil {
			return nil, fmt.Errorf("registry: read packs dir %q: %w", packsRoot, err)
		}
		var dirs []string
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(packsRoot, e.Name()))
			}
		}
		sort.Strings(dirs)
		return dirs, nil
	}
	return []string{root}, nil
}

// listYAMLFiles returns the manifest files to validate in one pack dir: the
// templates/ and evalsets/ subtrees (globbed, one manifest per file). Files are
// dispatched on their declared kind, so a manifest placed in the "wrong" subdir
// still gets the right checks. Non-regular entries (symlinks/devices) are
// skipped for the same symlink-escape reason as the import reader (CWE-59/22).
//
// The templates/ and evalsets/ SUBDIRS are stat'd with os.Lstat (not os.Stat)
// so a symlinked directory is NOT followed: a pack running through this
// creds-free gate is UNTRUSTED PR content, and a symlink named "templates" (or
// "evalsets") pointing at an out-of-tree directory would otherwise let
// os.ReadDir enumerate + read arbitrary *.yaml on the CI runner and echo their
// values into findings (CWE-59/CWE-22). A symlink has ModeSymlink set, so
// fi.IsDir() is false and it is skipped — mirroring the per-file IsRegular
// guard below at the directory level.
func listYAMLFiles(packDir string) ([]string, error) {
	var out []string
	for _, sub := range []string{"templates", "evalsets"} {
		dir := filepath.Join(packDir, sub)
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("registry: read %q: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !isYAMLFile(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return nil, fmt.Errorf("registry: stat %q: %w", filepath.Join(dir, e.Name()), err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// readManifest reads a capped manifest file and returns its bytes + declared
// kind (for dispatch). A missing kind is reported as an error by the caller.
func readManifest(path string) ([]byte, string, error) {
	data, err := readCappedFile(path)
	if err != nil {
		return nil, "", err
	}
	var hdr manifestHeader
	if err := yamlUnmarshalStrictSize(data, &hdr); err != nil {
		return nil, "", fmt.Errorf("parse yaml: %w", err)
	}
	if hdr.Kind == "" {
		return nil, "", errors.New("missing manifest kind (want MetricTemplate or EvalSet)")
	}
	return data, hdr.Kind, nil
}

// rel renders p relative to root for stable, readable finding locations; on
// failure it falls back to p unchanged.
func rel(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil {
		return r
	}
	return p
}

// --- template validation (steps 1–5) ----------------------------------------

// validateTemplateManifest runs steps 1–5 on one MetricTemplate file and returns
// the template's id (empty if it could not be determined). It reports ALL defects
// it can find rather than stopping at the first.
func validateTemplateManifest(rep *Report, file string, data []byte) string {
	// (1) structural — strict JSON Schema.
	structuralOK := validateAgainstSchema(rep, file, "", metricTemplateSchema, data)

	// Decode into the codec's wire shape for the Go-level checks. Even when the
	// schema failed, a best-effort decode lets us surface identity/semantic
	// defects too (report-all).
	var pf packFile
	decodeErr := yamlUnmarshalStrictSize(data, &pf)
	id := pf.Metadata.ID
	if decodeErr != nil {
		rep.add(file, id, SeverityError, "parse yaml: %v", decodeErr)
		return id
	}

	// (2) identity.
	if err := validateTemplateID(id); err != nil {
		rep.add(file, id, SeverityError, "%v", err)
	}
	if pf.Metadata.Version == "" {
		rep.add(file, id, SeverityError, "metadata.version is required and must be semver")
	} else if !semverPattern.MatchString(pf.Metadata.Version) {
		rep.add(file, id, SeverityError, "metadata.version %q is not valid semver (MAJOR.MINOR.PATCH)", pf.Metadata.Version)
	}

	// spec.kind must normalize to a canonical kind (accepts vernacular).
	kind, kindErr := NormalizeKind(pf.Spec.Kind)
	if kindErr != nil {
		rep.add(file, id, SeverityError, "spec.kind: %v", kindErr)
	} else {
		// (3) semantic / kind-specific.
		validateKindSpecific(rep, file, id, kind, pf)
	}

	// (3b) autorater model security invariant — mirror the ingest guard so the
	// creds-free gate agrees with what import will reject (audit LOW-1): a
	// project-scoped or ".."-bearing autorater.model passes the strict schema
	// (any string) but is rejected at import, so without this the gate is a
	// false-clean. validateAutoraterModel is the single source of truth.
	if _, err := validateAutoraterModel(id, pf.Spec.Autorater.Model); err != nil {
		rep.add(file, id, SeverityError, "%v", err)
	}

	// (4) placeholder consistency.
	validatePlaceholders(rep, file, id, kind, pf)

	// (5) lint (warnings).
	lintTemplate(rep, file, id, pf)

	// Carry the parsed template for the optional --dry-run live probe, but only
	// when it is structurally + identity sound (no point dry-running a template
	// that already failed a hard gate).
	if structuralOK && kindErr == nil && id != "" {
		if t, err := (YAMLCodec{}).Unmarshal(data); err == nil {
			rep.Templates = append(rep.Templates, *t)
		}
	}
	return id
}

// validateKindSpecific enforces the per-kind required/forbidden data (design
// §3.5 step 3).
func validateKindSpecific(rep *Report, file, id string, kind MetricKind, pf packFile) {
	inputNames := map[string]bool{}
	for _, in := range pf.Spec.Inputs {
		inputNames[in.Name] = true
	}
	hasRubric := len(pf.Spec.RubricGroups) > 0
	hasSchema := len(pf.Spec.ResponseSchema) > 0
	hasPair := pf.Spec.CandidateFieldName != "" || pf.Spec.BaselineFieldName != ""

	switch kind {
	case KindPairwise:
		if pf.Spec.CandidateFieldName == "" {
			rep.add(file, id, SeverityError, "kind %q requires spec.candidateFieldName", kind)
		} else if !inputNames[pf.Spec.CandidateFieldName] {
			rep.add(file, id, SeverityError, "kind %q candidateFieldName %q is not declared in spec.inputs", kind, pf.Spec.CandidateFieldName)
		}
		if pf.Spec.BaselineFieldName == "" {
			rep.add(file, id, SeverityError, "kind %q requires spec.baselineFieldName", kind)
		} else if !inputNames[pf.Spec.BaselineFieldName] {
			rep.add(file, id, SeverityError, "kind %q baselineFieldName %q is not declared in spec.inputs", kind, pf.Spec.BaselineFieldName)
		}
	case KindRubric:
		if !hasRubric {
			rep.add(file, id, SeverityError, "kind %q requires a non-empty spec.rubricGroups", kind)
		}
	case KindCustomSchema:
		if !hasSchema {
			rep.add(file, id, SeverityError, "kind %q requires spec.responseSchema", kind)
		} else if err := validateResponseSchema(pf.Spec.ResponseSchema); err != nil {
			rep.add(file, id, SeverityError, "spec.responseSchema is not a valid JSON Schema: %v", err)
		}
	case KindPointwise:
		// pointwise forbids the kind-specific fields of the other kinds.
		if hasPair {
			rep.add(file, id, SeverityError, "kind %q must not set candidateFieldName/baselineFieldName (those are pairwise-only)", kind)
		}
		if hasRubric {
			rep.add(file, id, SeverityError, "kind %q must not set spec.rubricGroups (that is rubric-only)", kind)
		}
		if hasSchema {
			rep.add(file, id, SeverityError, "kind %q must not set spec.responseSchema (that is custom_schema-only)", kind)
		}
	}
}

// validateResponseSchema confirms a custom_schema's responseSchema is itself a
// compilable JSON Schema (design §3.5 step 3).
func validateResponseSchema(schema map[string]any) error {
	jb, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("responseSchema.json", bytes.NewReader(jb)); err != nil {
		return err
	}
	if _, err := c.Compile("responseSchema.json"); err != nil {
		return err
	}
	return nil
}

// validatePlaceholders enforces step 4: every {{x}} in the prompt/system text is
// declared in spec.inputs; every required input is referenced; each input's
// modality is listed in spec.modalities.
func validatePlaceholders(rep *Report, file, id string, kind MetricKind, pf packFile) {
	declared := map[string]bool{}
	for _, in := range pf.Spec.Inputs {
		declared[in.Name] = true
	}
	// Pairwise candidate/baseline field names are legitimate placeholder sources;
	// treat them as declared so a prompt may reference them even if they are only
	// wired via the field-name mechanism (they should also appear in spec.inputs,
	// which the kind-specific check enforces separately).
	if kind == KindPairwise {
		if pf.Spec.CandidateFieldName != "" {
			declared[pf.Spec.CandidateFieldName] = true
		}
		if pf.Spec.BaselineFieldName != "" {
			declared[pf.Spec.BaselineFieldName] = true
		}
	}

	referenced := map[string]bool{}
	for _, text := range []string{pf.Spec.MetricPromptTemplate, pf.Spec.SystemInstruction} {
		for _, m := range placeholderPattern.FindAllStringSubmatch(text, -1) {
			name := m[1]
			referenced[name] = true
			if !declared[name] {
				rep.add(file, id, SeverityError, "prompt references undeclared placeholder {{%s}} (add it to spec.inputs)", name)
			}
		}
	}

	// Every REQUIRED input must be referenced by the prompt/system text.
	for _, in := range pf.Spec.Inputs {
		if in.Required && !referenced[in.Name] {
			rep.add(file, id, SeverityError, "required input %q is never referenced as {{%s}} in the prompt", in.Name, in.Name)
		}
	}

	// Each input's modality must be one of spec.modalities.
	modalities := map[string]bool{}
	for _, m := range pf.Spec.Modalities {
		modalities[m] = true
	}
	for _, in := range pf.Spec.Inputs {
		if in.Modality == "" {
			continue
		}
		if !modalities[in.Modality] {
			rep.add(file, id, SeverityError, "input %q modality %q is not listed in spec.modalities %v", in.Name, in.Modality, pf.Spec.Modalities)
		}
	}
}

// lintTemplate emits the non-fatal step-5 warnings.
func lintTemplate(rep *Report, file, id string, pf packFile) {
	if strings.TrimSpace(pf.Metadata.Description) == "" {
		rep.add(file, id, SeverityWarning, "lint: missing metadata.description")
	}
	if strings.TrimSpace(pf.Metadata.License) == "" {
		rep.add(file, id, SeverityWarning, "lint: missing metadata.license")
	}
	if strings.TrimSpace(pf.Spec.Autorater.Model) == "" {
		rep.add(file, id, SeverityWarning, "lint: no autorater.model set (eval-time default will be used)")
	}
	if sc := pf.Spec.Autorater.SamplingCount; sc != 0 && (sc < 1 || sc > 32) {
		rep.add(file, id, SeverityWarning, "lint: autorater.samplingCount %d is outside the recommended 1–32 range", sc)
	}
}

// --- evalset validation (design §3.4a / §9.3a) -------------------------------

// validateEvalSetManifest runs structural + identity + member checks on one
// EvalSet file. treeTemplateIDs is the set of every template id present in the
// validated tree, used to WARN (not error) on an unresolved member reference.
func validateEvalSetManifest(rep *Report, file string, data []byte, treeTemplateIDs map[string]bool) {
	// (1) structural — strict JSON Schema.
	validateAgainstSchema(rep, file, "", evalSetSchema, data)

	doc, err := pack.ParseEvalSet(data)
	if err != nil {
		rep.add(file, "", SeverityError, "%v", err)
		return
	}
	id := doc.Metadata.ID

	// (2) identity.
	if err := validateTemplateID(id); err != nil {
		rep.add(file, id, SeverityError, "metadata.id: %v", err)
	}
	if doc.Metadata.Version == "" {
		rep.add(file, id, SeverityError, "metadata.version is required and must be semver")
	} else if !semverPattern.MatchString(doc.Metadata.Version) {
		rep.add(file, id, SeverityError, "metadata.version %q is not valid semver (MAJOR.MINOR.PATCH)", doc.Metadata.Version)
	}

	// (3) members.
	if len(doc.Spec.Members) == 0 {
		rep.add(file, id, SeverityError, "spec.members must be non-empty")
	}
	for i, m := range doc.Spec.Members {
		if m.Metric == "" {
			rep.add(file, id, SeverityError, "spec.members[%d].metric is required", i)
			continue
		}
		if !templateIDPattern.MatchString(m.Metric) {
			rep.add(file, id, SeverityError, "spec.members[%d].metric %q is not a valid template id (want \"<namespace>/<slug>\")", i, m.Metric)
			continue
		}
		// Same-tree resolution: an in-tree miss is a WARNING, since a cross-pack
		// reference cannot be resolved without the full universe (design §3.4a).
		if !treeTemplateIDs[m.Metric] {
			rep.add(file, id, SeverityWarning, "spec.members[%d].metric %q resolves to no template in this tree (ok if it lives in another pack)", i, m.Metric)
		}
	}

	// (4) aggregation.method reserved enum (when declared).
	if doc.Spec.Aggregation != nil && doc.Spec.Aggregation.Method != "" {
		if !reservedAggregationMethods[doc.Spec.Aggregation.Method] {
			rep.add(file, id, SeverityError, "spec.aggregation.method %q is not one of the reserved methods (mean|weighted-mean|min|max|median|sum)", doc.Spec.Aggregation.Method)
		}
	}
}

// --- shared helpers ----------------------------------------------------------

// validateAgainstSchema validates data against a compiled JSON Schema, appending
// one Finding per violation (report-all). It returns whether the document was
// structurally valid.
func validateAgainstSchema(rep *Report, file, id string, schema *jsonschema.Schema, data []byte) bool {
	v, err := yamlToJSONValue(data)
	if err != nil {
		rep.add(file, id, SeverityError, "parse yaml: %v", err)
		return false
	}
	if err := schema.Validate(v); err != nil {
		for _, msg := range schemaErrorMessages(err) {
			rep.add(file, id, SeverityError, "schema: %s", msg)
		}
		return false
	}
	return true
}

// schemaErrorMessages flattens a jsonschema ValidationError into one readable
// message per leaf violation (skipping the umbrella container entries that carry
// no message).
func schemaErrorMessages(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{err.Error()}
	}
	var msgs []string
	for _, e := range ve.BasicOutput().Errors {
		if strings.TrimSpace(e.Error) == "" {
			continue
		}
		loc := e.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		msgs = append(msgs, fmt.Sprintf("%s: %s", loc, e.Error))
	}
	if len(msgs) == 0 {
		msgs = append(msgs, err.Error())
	}
	sort.Strings(msgs)
	return msgs
}

// yamlToJSONValue decodes YAML into a JSON-compatible value (map[string]any /
// []any / float64 / string / bool / nil) so the jsonschema validator — which
// expects encoding/json's shapes — sees canonical types. The round-trip through
// encoding/json normalizes numeric kinds (yaml ints become float64) that the
// validator's "integer"/"number" checks accept.
func yamlToJSONValue(data []byte) (any, error) {
	var y any
	if err := yamlUnmarshalStrictSize(data, &y); err != nil {
		return nil, err
	}
	jb, err := json.Marshal(y)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(jb, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// yamlUnmarshalStrictSize decodes a single YAML document into v with the same
// input-size bound the codec/import path applies (MaxTemplateFileBytes), so a
// creds-free validate cannot be made to allocate without limit. An empty
// document (io.EOF) leaves v at its zero value, matching yaml.Unmarshal("").
func yamlUnmarshalStrictSize(data []byte, v any) error {
	dec := yaml.NewDecoder(io.LimitReader(bytes.NewReader(data), MaxTemplateFileBytes+1))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

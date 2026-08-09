package eval

import (
	"fmt"
	"regexp"
	"strings"
)

// content.go holds helpers that turn an AssetRef / template into the request
// shapes the eval backends need. In the P1 slice only the text path is wired:
//
//   - extractVars / expandAutoraterModel support the native JsonInstance path
//     (native.go).
//
// The multimodal converters below are deliberate, clearly-marked stubs for
// WI-P1-4. They are structured so that work item can add the gs://-only native
// FileData converter and the genai inline converter WITHOUT reshaping the
// engine: native EvaluateInstances accepts `FileData{gs://…}` ONLY (inline
// bytes are silently dropped — spike-core), whereas the genai custom_schema
// path DOES accept inline bytes. The two converters must stay distinct.

// varPattern matches double-brace placeholders like {{ response }}. Mizan
// standardizes on double-brace syntax (spike-core / collaboration-design §3.2),
// even though the API also parses single-brace.
var varPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// extractVars returns the unique {{var}} names referenced in s, in first-seen
// order.
func extractVars(s string) []string {
	matches := varPattern.FindAllStringSubmatch(s, -1)
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// expandAutoraterModel converts a publisher-relative model id (the portable
// form stored in a template, e.g. "gemini-2.5-flash" or
// "publishers/google/models/gemini-2.5-flash") into the FULL resource name the
// Eval Service requires:
//
//	projects/{project}/locations/{location}/publishers/google/models/{model}
//
// A bare id is rejected by the API (InvalidArgument), so this expansion is
// mandatory (spike-core). An already-fully-qualified name is passed through.
func expandAutoraterModel(model, projectID, location string) (string, error) {
	if model == "" {
		return "", fmt.Errorf("eval: template has no autorater model configured")
	}
	// Already a full resource name (project-scoped) — pass through.
	if strings.HasPrefix(model, "projects/") {
		return model, nil
	}
	if projectID == "" {
		return "", fmt.Errorf("eval: cannot expand autorater model %q: no project ID configured", model)
	}
	if location == "" {
		return "", fmt.Errorf("eval: cannot expand autorater model %q: no location configured", model)
	}
	// Accept "publishers/google/models/<model>" or a bare "<model>".
	bare := model
	if i := strings.LastIndex(model, "/"); i >= 0 {
		bare = model[i+1:]
	}
	return fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s", projectID, location, bare), nil
}

// --- WI-P1-4 stubs (multimodal). Do NOT implement in this slice. ---
//
// These are placeholders that establish the seam for the multimodal fan-out.
// The native path (aiplatformpb) and the genai path use different Go types from
// different packages, so two distinct converters are required. WI-P1-4 fills
// these in against a gs://-staged AssetRef (native) and inline bytes (genai).

// toNativeFileDataPart will convert a gs:// AssetRef into an aiplatformpb
// FileData Part for the native ContentMap path. Native EvaluateInstances
// accepts gs:// FileData ONLY; local files must be staged to GCS first
// (asset/gcs.go, WI-P1-7). Implemented in WI-P1-4.
func toNativeFileDataPart(_ AssetRef) error {
	return fmt.Errorf("%w: native multimodal FileData converter (WI-P1-4)", errNotImplemented)
}

// toGenaiInlinePart will convert an AssetRef into a genai inline Part for the
// custom_schema fallback path, which — unlike native eval — DOES accept inline
// bytes. Implemented in WI-P1-4/5.
func toGenaiInlinePart(_ AssetRef) error {
	return fmt.Errorf("%w: genai inline converter (WI-P1-4/5)", errNotImplemented)
}

package eval

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"

	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/registry"
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

// toGenaiInlinePart converts an AssetRef into a genai Part for the custom_schema
// path, which — unlike native EvaluateInstances — DOES accept inline bytes
// (spike-core / spike-custom). It reads a local file's bytes and sends them
// inline (no GCS staging needed on this path); a pre-staged gs:// URI is sent as
// FileData. Text is sent as a text Part.
func toGenaiInlinePart(ref AssetRef) (*genai.Part, error) {
	switch {
	case ref.Modality == registry.ModalityText || (ref.FilePath == "" && ref.GCSUri == "" && ref.Text != ""):
		return genai.NewPartFromText(ref.Text), nil
	case ref.GCSUri != "":
		mime := ref.MimeType
		if mime == "" {
			return nil, fmt.Errorf("eval: gs:// asset %q needs a MIME type", ref.GCSUri)
		}
		return genai.NewPartFromURI(ref.GCSUri, mime), nil
	case ref.FilePath != "":
		data, err := os.ReadFile(ref.FilePath)
		if err != nil {
			return nil, fmt.Errorf("eval: read asset %q: %w", ref.FilePath, err)
		}
		mime := ref.MimeType
		if mime == "" {
			mime = http.DetectContentType(data)
		}
		return genai.NewPartFromBytes(data, mime), nil
	default:
		return nil, fmt.Errorf("eval: asset ref has no text, file path, or gs:// URI")
	}
}

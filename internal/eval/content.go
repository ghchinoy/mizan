package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	aiplatformpb "cloud.google.com/go/aiplatform/apiv1beta1/aiplatformpb"
	"google.golang.org/genai"

	"github.com/ghchinoy/mizan/internal/asset"
	"github.com/ghchinoy/mizan/internal/registry"
)

// maxInlineBytes caps the size of a local asset the genai (custom_schema) inline
// path will read into memory before sending it as an inline Part. It is
// defense-in-depth against an oversized or crafted FilePath (a multi-GB media
// file, a sparse file, or — once FilePath is dataset-driven — a hostile entry)
// exhausting memory or hanging the process. 20 MiB is at/below the genai inline
// payload ceiling, so bytes beyond it would be rejected by the API anyway. The
// native path streams through asset.GCSStager (asset.maxAssetBytes) instead and
// never reads bytes here.
const maxInlineBytes int64 = 20 << 20

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
//
// The model reaching here has already been chosen by the centralized resolution
// chain (Engine.Run → resolveModel), so it is never empty — the former empty
// check was dead and has been removed (the built-in default is the final
// fallback). The bare id is validated before interpolation so a malformed value
// fails LOCALLY here instead of producing a garbage resource name for the remote
// API to reject.
func expandAutoraterModel(model, projectID, location string) (string, error) {
	// Already a full resource name (project-scoped) — trusted, pass through.
	if strings.HasPrefix(model, "projects/") {
		return model, nil
	}
	if projectID == "" {
		return "", fmt.Errorf("eval: cannot expand autorater model %q: no project ID configured", model)
	}
	if location == "" {
		return "", fmt.Errorf("eval: cannot expand autorater model %q: no location configured", model)
	}
	// Accept "publishers/google/models/<model>" or a bare "<model>", then
	// validate the bare id before composing the resource name.
	bare := bareModelID(model)
	if err := validateBareModelID(bare); err != nil {
		return "", err
	}
	return fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s", projectID, location, bare), nil
}

// --- Native multimodal (ContentMap) converters ---
//
// The native path (aiplatformpb) and the genai path use different Go types from
// different packages, so two DISTINCT converters are required and must stay
// distinct: native EvaluateInstances accepts `FileData{gs://…}` ONLY (inline
// bytes are silently dropped — spike-core), whereas the genai custom_schema path
// DOES accept inline bytes (toGenaiInlinePart below).

// toNativeFileDataPart converts an AssetRef into an aiplatformpb.Part for the
// native ContentMap path. Text becomes a text Part; a non-text asset becomes a
// gs:// FileData Part (with a resolved MIME type — native requires it, and a
// wrong/empty top-level type silently drops the asset). A local FilePath is
// staged to GCS through the engine's Stager first; a pre-staged gs:// URI is
// used directly (its MIME resolved via the Stager, an explicit override, or the
// object extension). When staging is required but no Stager is configured, a
// clear asset.ErrNoBucket-style error is returned.
func (e *Engine) toNativeFileDataPart(ctx context.Context, ref AssetRef) (*aiplatformpb.Part, error) {
	if isTextRef(ref) {
		return &aiplatformpb.Part{Data: &aiplatformpb.Part_Text{Text: ref.Text}}, nil
	}

	uri := ref.GCSUri
	mime := ref.MimeType

	switch {
	case ref.FilePath != "":
		if e.stager == nil {
			return nil, fmt.Errorf("eval: local asset %q requires GCS staging for native multimodal eval: %w", ref.FilePath, asset.ErrNoBucket)
		}
		res, err := e.stager.Stage(ctx, asset.StageInput{LocalPath: ref.FilePath, MIME: ref.MimeType})
		if err != nil {
			return nil, fmt.Errorf("eval: stage asset %q: %w", ref.FilePath, err)
		}
		uri, mime = res.GCSUri, res.MIME
	case ref.GCSUri != "":
		// Pre-staged: no upload needed. Resolve MIME via the Stager when present
		// (pass-through), else honor an explicit override or fall back to the
		// object extension so a bucket is not required just to read a gs:// URI.
		if e.stager != nil {
			res, err := e.stager.Stage(ctx, asset.StageInput{GCSUri: ref.GCSUri, MIME: ref.MimeType})
			if err != nil {
				return nil, fmt.Errorf("eval: resolve gs:// asset %q: %w", ref.GCSUri, err)
			}
			uri, mime = res.GCSUri, res.MIME
		} else if mime == "" {
			mime = asset.DetectMIME(ref.GCSUri, nil)
		}
	default:
		return nil, fmt.Errorf("eval: asset ref for a non-text field has no file path or gs:// URI")
	}

	if uri == "" {
		return nil, fmt.Errorf("eval: native multimodal asset has no gs:// URI")
	}
	if mime == "" || mime == "application/octet-stream" {
		return nil, fmt.Errorf("eval: native multimodal asset %q needs a MIME type (native FileData requires it; the API silently drops assets with a wrong or missing MIME)", uri)
	}
	return &aiplatformpb.Part{Data: &aiplatformpb.Part_FileData{FileData: &aiplatformpb.FileData{FileUri: uri, MimeType: mime}}}, nil
}

// isTextRef reports whether ref is a plain text value (no file path or gs:// URI
// and either an explicit text modality or a non-empty Text with no other data).
func isTextRef(ref AssetRef) bool {
	if ref.FilePath != "" || ref.GCSUri != "" {
		return false
	}
	return ref.Modality == "" || ref.Modality == registry.ModalityText
}

// isMediaRef reports whether ref must be materialized as a native FileData Part
// (i.e. it names a non-text asset by local path, gs:// URI, or non-text
// modality).
func isMediaRef(ref AssetRef) bool {
	if ref.FilePath != "" || ref.GCSUri != "" {
		return true
	}
	return ref.Modality != "" && ref.Modality != registry.ModalityText
}

// keysHaveMedia reports whether any of the referenced keys resolves to a
// non-text asset, which forces the native ContentMap path (over JsonInstance).
func keysHaveMedia(keys []string, inst Instance) bool {
	for _, k := range keys {
		if ref, ok := inst.Fields[k]; ok && isMediaRef(ref) {
			return true
		}
	}
	return false
}

// buildContentMap materializes the referenced keys into a native ContentMap
// (placeholder -> content), staging any local files to gs:// FileData first. It
// validates variable/instance-key parity (every key must have a value) before
// building, failing fast client-side.
func (e *Engine) buildContentMap(ctx context.Context, keys []string, inst Instance) (*aiplatformpb.ContentMap, error) {
	values := make(map[string]*aiplatformpb.ContentMap_Contents, len(keys))
	var missing []string
	for _, k := range keys {
		ref, ok := inst.Fields[k]
		if !ok {
			missing = append(missing, k)
			continue
		}
		part, err := e.toNativeFileDataPart(ctx, ref)
		if err != nil {
			return nil, err
		}
		values[k] = &aiplatformpb.ContentMap_Contents{
			Contents: []*aiplatformpb.Content{{
				Role:  "user",
				Parts: []*aiplatformpb.Part{part},
			}},
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("eval: instance is missing values for template variables %v", missing)
	}
	return &aiplatformpb.ContentMap{Values: values}, nil
}

// buildJSONInstanceKeys marshals the given text keys into the JSON instance
// string the API expects, validating that each key has a value.
func buildJSONInstanceKeys(keys []string, inst Instance) (string, error) {
	if len(keys) == 0 {
		// The API rejects a non-empty instance when the template has no
		// variables; an empty JSON object is the correct payload.
		return "{}", nil
	}
	fields := map[string]string{}
	var missing []string
	for _, k := range keys {
		ref, ok := inst.Fields[k]
		if !ok {
			missing = append(missing, k)
			continue
		}
		fields[k] = ref.Text
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("eval: instance is missing values for template variables %v", missing)
	}
	b, err := json.Marshal(fields)
	if err != nil {
		return "", fmt.Errorf("eval: marshal json instance: %w", err)
	}
	return string(b), nil
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
		// Resolve MIME the same way native does (asset.DetectMIME on the object
		// URI/extension) so a bare --gcs ref with no explicit MIME still attaches.
		// Guard an unresolved/octet-stream type: a wrong or empty top-level type
		// would be silently dropped downstream, so refuse loudly instead.
		mime := ref.MimeType
		if mime == "" {
			mime = asset.DetectMIME(ref.GCSUri, nil)
		}
		if mime == "" || mime == "application/octet-stream" {
			return nil, fmt.Errorf("eval: gs:// asset %q: cannot resolve a media MIME type; refusing to send (would be dropped)", ref.GCSUri)
		}
		return genai.NewPartFromURI(ref.GCSUri, mime), nil
	case ref.FilePath != "":
		data, mime, err := readInlineAsset(ref.FilePath, ref.MimeType)
		if err != nil {
			return nil, err
		}
		return genai.NewPartFromBytes(data, mime), nil
	default:
		return nil, fmt.Errorf("eval: asset ref has no text, file path, or gs:// URI")
	}
}

// readInlineAsset reads a local file's bytes for the genai inline path with the
// same hardening asset.GCSStager applies to native staging (WI-7): symlinks are
// resolved and the target must be a regular file (rejecting dirs, devices, FIFOs
// and sockets such as /dev/zero, which would otherwise read without bound), and
// the size is capped and read through a bounded reader. This closes an
// OOM/hang DoS now that FilePath is CLI-reachable (and will become dataset-
// driven). In P1 a CLI user may legitimately reference any file they can read,
// so this is defense-in-depth, not a sandbox: it does NOT confine paths to a
// base directory.
func readInlineAsset(path, mimeOverride string) ([]byte, string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", fmt.Errorf("eval: resolve asset %q: %w", path, err)
	}
	fi, err := os.Lstat(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("eval: stat asset %q: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, "", fmt.Errorf("eval: asset %q is not a regular file (mode %s); refusing to read", path, fi.Mode().Type())
	}
	if fi.Size() > maxInlineBytes {
		return nil, "", fmt.Errorf("eval: asset %q is %d bytes, exceeds the %d-byte inline cap", path, fi.Size(), maxInlineBytes)
	}

	f, err := os.Open(resolved)
	if err != nil {
		return nil, "", fmt.Errorf("eval: open asset %q: %w", path, err)
	}
	defer f.Close()

	// Bounded read (belt-and-braces against a file that grows past the stat, or
	// a special file that slipped the guard): read at most maxInlineBytes+1 and
	// reject if the cap is exceeded.
	data, err := io.ReadAll(io.LimitReader(f, maxInlineBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("eval: read asset %q: %w", path, err)
	}
	if int64(len(data)) > maxInlineBytes {
		return nil, "", fmt.Errorf("eval: asset %q exceeds the %d-byte inline cap", path, maxInlineBytes)
	}

	// Type the bytes with the project's richer detector (sniff + extension), for
	// parity with the native path. http.DetectContentType mis-detects several
	// media containers (WAV/OGG/FLAC/MOV) to a wrong top-level type — exactly the
	// silent-drop hazard this fix closes. Guard an unresolved/octet-stream type.
	mime := mimeOverride
	if mime == "" {
		mime = asset.DetectMIME(path, data)
	}
	if mime == "" || mime == "application/octet-stream" {
		return nil, "", fmt.Errorf("eval: asset %q: cannot resolve a media MIME type; refusing to read", path)
	}
	return data, mime, nil
}

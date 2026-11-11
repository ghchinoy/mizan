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

package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	return registry.ExtractPlaceholders(s)
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
	if len(ref.Items) > 0 {
		return nil, fmt.Errorf("eval: multi-asset variation lists require a structured genai kind (boul, choice, score, rubric, custom_schema) or genai engine")
	}
	if len(ref.Bytes) > 0 {
		return nil, fmt.Errorf("eval: inline byte assets are not supported by native Vertex EvaluateInstances (use a gs:// URI or run a structured genai kind)")
	}
	if ref.VideoMeta != nil {
		return nil, fmt.Errorf("eval: video metadata clip offsets are not supported by native Vertex EvaluateInstances (run a structured genai kind)")
	}

	rawURI := cmp.Or(ref.URI, ref.GCSUri)
	if rawURI != "" && !strings.HasPrefix(rawURI, "gs://") {
		return nil, fmt.Errorf("eval: native Vertex EvaluateInstances requires a gs:// URI, got %q (https:// URIs are supported only on the structured genai path)", rawURI)
	}
	if rawURI != "" && (ref.URI != "" || len(e.allowedGCSBuckets) > 0) {
		if err := ValidateMediaURI(rawURI, e.allowedURIHosts, e.allowedGCSBuckets); err != nil {
			return nil, err
		}
	}

	uri := rawURI
	mime := ref.MimeType

	switch {
	case ref.FilePath != "":
		if !e.allowLocalFiles {
			return nil, fmt.Errorf("eval: local file path %q is disabled on this evaluator (pass WithLocalFileAccess(true) or supply Bytes / URI)", ref.FilePath)
		}
		if e.stager == nil {
			return nil, fmt.Errorf("eval: local asset %q requires GCS staging for native multimodal eval: %w", ref.FilePath, asset.ErrNoBucket)
		}
		res, err := e.stager.Stage(ctx, asset.StageInput{LocalPath: ref.FilePath, MIME: ref.MimeType})
		if err != nil {
			return nil, fmt.Errorf("eval: stage asset %q: %w", ref.FilePath, err)
		}
		uri, mime = res.GCSUri, res.MIME
	case rawURI != "":
		// Pre-staged: no upload needed. Resolve MIME via the Stager when present
		// (pass-through), else honor an explicit override or fall back to the
		// object extension so a bucket is not required just to read a gs:// URI.
		if e.stager != nil {
			res, err := e.stager.Stage(ctx, asset.StageInput{GCSUri: rawURI, MIME: ref.MimeType})
			if err != nil {
				return nil, fmt.Errorf("eval: resolve gs:// asset %q: %w", rawURI, err)
			}
			uri, mime = res.GCSUri, res.MIME
		} else if mime == "" {
			mime = asset.DetectMIME(rawURI, nil)
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

// isTextRef reports whether ref is a plain text value (no file path, URI, inline
// bytes, or variation items, and either an explicit text modality or unset).
func isTextRef(ref AssetRef) bool {
	if ref.FilePath != "" || ref.GCSUri != "" || ref.URI != "" || len(ref.Bytes) > 0 || len(ref.Items) > 0 {
		return false
	}
	return ref.Modality == "" || ref.Modality == registry.ModalityText
}

// isMediaRef reports whether ref must be materialized as a media Part (non-text
// asset by local path, URI, inline bytes, variation items, or non-text modality).
func isMediaRef(ref AssetRef) bool {
	if ref.FilePath != "" || ref.GCSUri != "" || ref.URI != "" || len(ref.Bytes) > 0 || len(ref.Items) > 0 {
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

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// ValidateMediaURI validates a gs:// or https:// media URI against SSRF rules
// and optional host/bucket allowlists.
func ValidateMediaURI(rawURI string, allowedHosts, allowedBuckets []string) error {
	trimmed := strings.TrimSpace(rawURI)
	if trimmed == "" {
		return fmt.Errorf("eval: media URI is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("eval: invalid media URI %q: %w", rawURI, err)
	}
	if u.User != nil {
		return fmt.Errorf("eval: media URI %q must not contain userinfo credentials", rawURI)
	}
	switch strings.ToLower(u.Scheme) {
	case "gs":
		bucket := u.Host
		object := strings.TrimPrefix(u.Path, "/")
		if bucket == "" || object == "" {
			return fmt.Errorf("eval: gs:// media URI %q must include both bucket and object path (gs://bucket/object)", rawURI)
		}
		if len(allowedBuckets) > 0 && !slices.Contains(allowedBuckets, bucket) {
			return fmt.Errorf("eval: gs:// bucket %q in %q is not in allowed buckets %v", bucket, rawURI, allowedBuckets)
		}
		return nil
	case "https":
		host := strings.ToLower(u.Hostname())
		if host == "" {
			return fmt.Errorf("eval: https:// media URI %q has an empty host", rawURI)
		}
		explicitlyAllowed := hostInAllowlist(host, allowedHosts)
		if len(allowedHosts) > 0 && !explicitlyAllowed {
			return fmt.Errorf("eval: https:// host %q in %q is not in allowed URI hosts %v", host, rawURI, allowedHosts)
		}
		if !explicitlyAllowed {
			if host == "localhost" || strings.HasSuffix(host, ".localhost") {
				return fmt.Errorf("eval: https:// media URI %q targets loopback host %q (blocked for SSRF safety)", rawURI, host)
			}
			if ip, err := netip.ParseAddr(host); err == nil {
				unmapped := ip.Unmap()
				if unmapped.IsLoopback() || unmapped.IsPrivate() || unmapped.IsLinkLocalUnicast() ||
					unmapped.IsLinkLocalMulticast() || unmapped.IsMulticast() || unmapped.IsUnspecified() ||
					cgnatPrefix.Contains(unmapped) {
					return fmt.Errorf("eval: https:// media URI %q targets non-public IP %q (blocked for SSRF safety)", rawURI, host)
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("eval: unsupported media URI scheme %q in %q (want gs:// or https://)", u.Scheme, rawURI)
	}
}

func hostInAllowlist(host string, allowed []string) bool {
	for _, h := range allowed {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}

// ValidateVideoMetadata validates clip offsets and FPS bounds on a
// genai.VideoMetadata. Note that google.golang.org/genai serializes StartOffset
// and EndOffset as whole seconds ("%.0fs"), so sub-second clip windows that
// round to the same whole second collapse to a zero-length clip on the wire and
// are rejected here.
func ValidateVideoMetadata(vm *genai.VideoMetadata) error {
	if vm == nil {
		return nil
	}
	if vm.StartOffset < 0 {
		return fmt.Errorf("eval: video StartOffset (%v) must be >= 0", vm.StartOffset)
	}
	if vm.EndOffset < 0 {
		return fmt.Errorf("eval: video EndOffset (%v) must be >= 0", vm.EndOffset)
	}
	if vm.EndOffset > 0 {
		if vm.StartOffset >= vm.EndOffset {
			return fmt.Errorf("eval: video StartOffset (%v) must be strictly less than EndOffset (%v)", vm.StartOffset, vm.EndOffset)
		}
		if math.Round(vm.EndOffset.Seconds()) <= math.Round(vm.StartOffset.Seconds()) {
			return fmt.Errorf("eval: video clip window [%v, %v] collapses to zero whole seconds when marshaled by google.golang.org/genai (use offsets separated by at least 1 whole second)", vm.StartOffset, vm.EndOffset)
		}
	}
	if vm.FPS != nil && (*vm.FPS <= 0 || *vm.FPS > 24 || math.IsNaN(*vm.FPS) || math.IsInf(*vm.FPS, 0)) {
		return fmt.Errorf("eval: video FPS (%v) must be in (0, 24]", *vm.FPS)
	}
	return nil
}

func cloneVideoMetadata(vm *genai.VideoMetadata) *genai.VideoMetadata {
	if vm == nil {
		return nil
	}
	cp := *vm
	if vm.FPS != nil {
		fps := *vm.FPS
		cp.FPS = &fps
	}
	return &cp
}

func clonePartResolution(pr *genai.PartMediaResolution) *genai.PartMediaResolution {
	if pr == nil {
		return nil
	}
	cp := *pr
	if pr.NumTokens != nil {
		nt := *pr.NumTokens
		cp.NumTokens = &nt
	}
	return &cp
}

func attachMediaDecorators(part *genai.Part, ref AssetRef) {
	if ref.VideoMeta != nil {
		part.VideoMetadata = cloneVideoMetadata(ref.VideoMeta)
	}
	if ref.PartResolution != nil {
		part.MediaResolution = clonePartResolution(ref.PartResolution)
	}
}

// toGenaiPart converts a single non-Items AssetRef into a *genai.Part,
// returning the part, its resolved MIME type, its inline byte count (for
// aggregate 20 MiB enforcement), or a validation error.
func toGenaiPart(ref AssetRef, allowLocalFiles bool, allowedHosts, allowedBuckets []string) (*genai.Part, string, int64, error) {
	if err := ValidateVideoMetadata(ref.VideoMeta); err != nil {
		return nil, "", 0, err
	}
	switch {
	case isTextRef(ref) && (ref.Modality == registry.ModalityText || ref.Text != ""):
		return genai.NewPartFromText(ref.Text), "text/plain", 0, nil
	case len(ref.Bytes) > 0:
		if int64(len(ref.Bytes)) > maxInlineBytes {
			return nil, "", 0, fmt.Errorf("eval: inline bytes asset is %d bytes, exceeds %d-byte limit; pass gs:// or https:// via URI or pre-upload via FromGenaiPart", len(ref.Bytes), maxInlineBytes)
		}
		mime := strings.TrimSpace(ref.MimeType)
		if mime == "" {
			mime = asset.DetectMIME("", ref.Bytes)
		}
		if mime == "" || mime == "application/octet-stream" {
			return nil, "", 0, fmt.Errorf("eval: inline bytes asset: cannot resolve a media MIME type; supply an explicit MIME type")
		}
		part := genai.NewPartFromBytes(slices.Clone(ref.Bytes), mime)
		attachMediaDecorators(part, ref)
		return part, mime, int64(len(ref.Bytes)), nil
	case ref.URI != "" || ref.GCSUri != "":
		rawURI := strings.TrimSpace(cmp.Or(ref.URI, ref.GCSUri))
		if err := ValidateMediaURI(rawURI, allowedHosts, allowedBuckets); err != nil {
			return nil, "", 0, err
		}
		mime := strings.TrimSpace(ref.MimeType)
		if mime == "" {
			if u, err := url.Parse(rawURI); err == nil {
				mime = asset.DetectMIME(u.Path, nil)
			}
		}
		if mime == "" || mime == "application/octet-stream" {
			return nil, "", 0, fmt.Errorf("eval: URI asset %q: cannot resolve a media MIME type; refusing to send (would be dropped)", rawURI)
		}
		part := genai.NewPartFromURI(rawURI, mime)
		attachMediaDecorators(part, ref)
		return part, mime, 0, nil
	case ref.FilePath != "":
		if !allowLocalFiles {
			return nil, "", 0, fmt.Errorf("eval: local file path %q is disabled on this evaluator (pass WithLocalFileAccess(true) or supply Bytes / URI)", ref.FilePath)
		}
		data, mime, err := readInlineAsset(ref.FilePath, ref.MimeType)
		if err != nil {
			return nil, "", 0, err
		}
		part := genai.NewPartFromBytes(data, mime)
		attachMediaDecorators(part, ref)
		return part, mime, int64(len(data)), nil
	default:
		return nil, "", 0, fmt.Errorf("eval: asset ref has no text, bytes, file path, or media URI")
	}
}

// toGenaiInlinePart converts an AssetRef into a genai Part for the custom_schema
// path, which — unlike native EvaluateInstances — DOES accept inline bytes
// (spike-core / spike-custom). It reads a local file's bytes and sends them
// inline (no GCS staging needed on this path); a pre-staged gs:// URI is sent as
// FileData. Text is sent as a text Part.
func toGenaiInlinePart(ref AssetRef) (*genai.Part, error) {
	part, _, _, err := toGenaiPart(ref, true, nil, nil)
	return part, err
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

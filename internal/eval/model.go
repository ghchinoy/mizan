package eval

// model.go owns the SINGLE source of truth for the built-in autorater model and
// the uniform model-resolution precedence chain shared by the native and genai
// paths (WI-F3), plus the resolved-target description backing the default-on
// pre-flight echo (WI-F7).

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ghchinoy/mizan/internal/registry"
)

// BuiltinDefaultModel is the ONE built-in autorater model id, used only when no
// model is supplied anywhere else in the precedence chain
// (flag > template > config default-model > built-in). It is intentionally
// defined in exactly one place so the GA id can be changed trivially — the
// coordinator may adjust this value before merge; change it HERE and nowhere
// else.
//
// Value rationale (coordinator lock): gemini-2.5-flash is served on BOTH the
// native regional endpoints (us-central1 default) AND location=global, whereas
// gemini-3.5-flash/-lite are global-only and 404 on the regional native
// EvaluateInstances autorater path. So the built-in stays 2.5-flash for safety;
// users select a newer global-only model via the default-model config key or the
// eval-time --model override (which delivers item 3's intent without a code
// change).
const BuiltinDefaultModel = "gemini-2.5-flash"

// GenaiLocation is the fixed location the genai (custom_schema) path targets.
// The native EvaluateInstances path is regional (cfg.Location); the genai path
// is global by design (spike-custom). It is defined once so the composition root
// (wire) and the pre-flight echo (Engine.Resolve) always agree.
//
// It is an ALIAS of the single global-location source of truth (globalLocation
// in route.go): the genai path's "global" and the R-GLOBAL eval-host "global" are
// the same location string, and pinning them to one const means they cannot
// silently drift (review OPTIONAL-1).
const GenaiLocation = globalLocation

// resolveModel applies the model precedence chain UNIFORMLY across the native
// and genai paths:
//
//	flag override > template.AutoraterModel > config default-model > built-in
//
// Because the built-in is the final fallback, the returned model is never empty
// — which is why the native path (expandAutoraterModel) no longer hard-errors on
// an empty template model: it inherits the config default or the built-in
// instead.
func (e *Engine) resolveModel(tmpl registry.MetricTemplate, override string) string {
	return firstNonEmptyModel(override, tmpl.AutoraterModel, e.defaultModel, BuiltinDefaultModel)
}

func firstNonEmptyModel(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Model-source labels for AppliedAutorater.ModelSource, one per branch of the
// resolveModel precedence chain (§4.3). Kept as consts so callers and tests name
// the same string.
const (
	modelSourceFlag          = "flag"
	modelSourceTemplate      = "template"
	modelSourceConfigDefault = "config-default"
	modelSourceBuiltin       = "builtin"
)

// modelSource classifies WHY the resolved model was chosen, mirroring EXACTLY the
// precedence firstNonEmptyModel applies in resolveModel
// (flag > template > config default-model > built-in). It is a pure function so
// the "why" label stays in ONE place with the precedence it describes and can be
// unit-tested across all four branches. It takes the same first-three inputs as
// resolveModel; the fourth (BuiltinDefaultModel) is the implicit final fallback.
func modelSource(override, templateModel, configDefault string) string {
	switch {
	case override != "":
		return modelSourceFlag
	case templateModel != "":
		return modelSourceTemplate
	case configDefault != "":
		return modelSourceConfigDefault
	default:
		return modelSourceBuiltin
	}
}

// bareModelID strips any "publishers/google/models/<id>" (or "projects/.../<id>")
// prefix, returning the publisher-relative id (e.g. "gemini-2.5-flash").
func bareModelID(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

// bareModelPattern is the conservative allowlist a bare (publisher-relative)
// autorater model id must match. Publisher model ids are letters/digits with
// '.', '_' and '-' separators (e.g. "gemini-2.5-flash"); anything else —
// newlines, other control characters, whitespace, slashes, shell/format
// metacharacters — is rejected. This is deliberately stricter than the API so a
// malformed --model value fails LOCALLY with a crisp error instead of being
// echoed to stderr or interpolated into a Vertex resource name and bounced by
// the remote API.
var bareModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validateBareModelID rejects a bare model id that does not match the allowlist.
func validateBareModelID(id string) error {
	if id == "" {
		return fmt.Errorf("eval: empty autorater model id")
	}
	if !bareModelPattern.MatchString(id) {
		return fmt.Errorf("eval: invalid autorater model id %q: expected a bare publisher model id such as %q (letters, digits, '.', '_', '-')", id, BuiltinDefaultModel)
	}
	return nil
}

// ValidateModel validates a user- or template-supplied autorater model id BEFORE
// it is echoed to stderr (pre-flight, WI-F7) or composed into a Vertex resource
// name (expandAutoraterModel) or sent to the genai SDK (genaiModelID). It is the
// single local guard behind the flag/template/config inputs to the resolution
// chain:
//
//   - "" is accepted: it means "no value here, fall through the precedence chain"
//     (an empty --model flag, or an empty template/config model). The resolved
//     model is never empty because BuiltinDefaultModel is the final fallback.
//   - a fully-qualified "projects/.../models/..." resource name is trusted and
//     passed through unchanged (the native path forwards it verbatim); it is an
//     explicit, structured id, not a bare user token.
//   - a recognized "publishers/.../<id>" form validates its trailing bare id.
//   - any other value must be a TRULY bare id: no slashes at all, matching
//     bareModelPattern. Rejecting arbitrary slash-bearing tokens ("a/b",
//     "../../x") explicitly — rather than silently reducing them to their last
//     path segment — is what makes "reject slashes for the bare form" hold.
//
// This turns a clearly-invalid id (newline/control-char/slash-bearing) into a
// crisp LOCAL error instead of a remote API rejection.
func ValidateModel(model string) error {
	switch {
	case model == "":
		return nil
	case strings.HasPrefix(model, "projects/"):
		// Fully-qualified project-scoped resource name: trusted structured id.
		return nil
	case strings.HasPrefix(model, "publishers/"):
		// Recognized publisher-relative form: validate its trailing bare id.
		return validateBareModelID(bareModelID(model))
	case strings.Contains(model, "/"):
		return fmt.Errorf("eval: invalid autorater model id %q: a bare model id must not contain '/'; use a plain id such as %q, a \"publishers/google/models/<id>\" form, or a full \"projects/.../models/<id>\" resource name", model, BuiltinDefaultModel)
	default:
		return validateBareModelID(model)
	}
}

// parseFullModelResource extracts the project and location embedded in a
// fully-qualified "projects/<p>/locations/<l>/..." model resource name. The
// native path passes such an id through untouched (expandAutoraterModel), so
// those embedded values — not the engine's configured project/location — are
// what the API will actually call. The pre-flight echo (WI-F7) uses this so its
// displayed location reflects a fully-qualified template/flag model. Returns
// ok=false for any non-fully-qualified id.
func parseFullModelResource(model string) (project, location string, ok bool) {
	if !strings.HasPrefix(model, "projects/") {
		return "", "", false
	}
	parts := strings.Split(model, "/")
	// projects/<p>/locations/<l>/publishers/google/models/<id>
	if len(parts) >= 4 && parts[0] == "projects" && parts[2] == "locations" && parts[1] != "" && parts[3] != "" {
		return parts[1], parts[3], true
	}
	return "", "", false
}

// ResolvedTarget describes the project/location/model an eval WILL call, after
// the full precedence chain and per-path location selection. It backs the
// default-on pre-flight echo (WI-F7): showing it before the call makes a
// wrong-project or unexpected global-location surprise visible immediately,
// instead of only when the API rejects it.
type ResolvedTarget struct {
	Project  string
	Location string
	Model    string // bare publisher-relative id
	Path     string // "native" or "genai"
	// LocationFromModelResource records the PROVENANCE of Location on the native
	// path: true means Location was taken from a fully-qualified
	// "projects/.../locations/<loc>/..." model resource (the user pinned it
	// explicitly), false means it came from the engine's configured location or was
	// forced by global-only ROUTING. The pre-flight source attribution needs this
	// because a resource explicitly pinned to "global" and a regional model
	// auto-routed to the global host both surface Location=="global" on the native
	// path — the string alone cannot tell "src=model" (resource-derived) from
	// "src=global-route" (routing-forced). It stays false on the genai path, whose
	// global location is attributed to the path itself (src=global-path).
	LocationFromModelResource bool
}

// Resolve reports the target an eval of tmpl (with an optional --model override)
// would call, WITHOUT performing it. The location reflects the ACTUAL per-path
// value: the native path is regional (cfg.Location); the custom_schema/genai
// path is global (spike-custom). rubricDetail signals that a KindRubric run will
// take the genai structured-output path (--rubric-detail), which — like
// custom_schema — is global; the pre-flight echo must reflect that actual target
// rather than the native regional default.
func (e *Engine) Resolve(tmpl registry.MetricTemplate, override string, rubricDetail bool) ResolvedTarget {
	// resolveModel is also called in Engine.Run; this second call (for the
	// pre-flight echo) is an intentional, negligible cost — a first-non-empty
	// scan over four strings. The echo and the actual run are separate entry
	// points and each independently needs the resolved model, so a shared-state
	// cache would add more surface than it saves (review n4).
	model := e.resolveModel(tmpl, override)
	target := ResolvedTarget{
		Project:  e.projectID,
		Location: e.location,
		Model:    bareModelID(model),
		Path:     "native",
	}
	if tmpl.Kind == registry.KindCustomSchema || (rubricDetail && tmpl.Kind == registry.KindRubric) {
		// The genai path is global regardless of a fully-qualified template
		// model: genaiModelID reduces it to the bare id sent to the global
		// client, so the location shown here is always GenaiLocation. A
		// rubric-detail run takes this same genai/global path.
		target.Location = GenaiLocation
		target.Path = "genai"
		return target
	}
	// Native path: a fully-qualified "projects/.../models/..." model (from a
	// template or --model) is forwarded verbatim by expandAutoraterModel, so its
	// embedded project/location are what the API will actually use — reflect them
	// in the pre-flight line rather than the engine's configured defaults (review
	// n3).
	if p, l, ok := parseFullModelResource(model); ok {
		target.Project = p
		target.Location = l
		target.LocationFromModelResource = true
	}
	// R-GLOBAL: a KNOWN global-only judge is auto-routed to the global host at run
	// time (route.go). When we can detect that up front (the prefix fast-path),
	// the echo must show location=global so the user isn't told a regional target
	// the run will not actually honor. This overrides any embedded regional
	// location above because the HOST — not the autorater path — is decisive
	// (spike-eval-region-autorater). A global-only judge NOT in the prefix table
	// is discovered only via the retry, so its echo stays regional and the
	// run-time retry notice (noticeRetryGlobal) covers it instead.
	if isGlobalOnlyModel(model) {
		// Attribute the location to ROUTING only when routing actually CHANGES it
		// from a non-global value. A fully-qualified resource explicitly pinned to
		// "global" keeps its resource provenance (src=model, per review OPTIONAL:
		// do not collapse resource-derived-global into routing-forced-global); a
		// regional resource that routing overrides to global becomes routing-forced.
		if target.Location != globalLocation {
			target.LocationFromModelResource = false
		}
		target.Location = globalLocation
	}
	return target
}

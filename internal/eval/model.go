package eval

// model.go owns the SINGLE source of truth for the built-in autorater model and
// the uniform model-resolution precedence chain shared by the native and genai
// paths (WI-F3), plus the resolved-target description backing the default-on
// pre-flight echo (WI-F7).

import (
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
const GenaiLocation = "global"

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

// bareModelID strips any "publishers/google/models/<id>" (or "projects/.../<id>")
// prefix, returning the publisher-relative id (e.g. "gemini-3.5-flash").
func bareModelID(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
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
}

// Resolve reports the target an eval of tmpl (with an optional --model override)
// would call, WITHOUT performing it. The location reflects the ACTUAL per-path
// value: the native path is regional (cfg.Location); the custom_schema/genai
// path is global (spike-custom).
func (e *Engine) Resolve(tmpl registry.MetricTemplate, override string) ResolvedTarget {
	target := ResolvedTarget{
		Project:  e.projectID,
		Location: e.location,
		Model:    bareModelID(e.resolveModel(tmpl, override)),
		Path:     "native",
	}
	if tmpl.Kind == registry.KindCustomSchema {
		target.Location = GenaiLocation
		target.Path = "genai"
	}
	return target
}

package registry

import (
	"fmt"
	"regexp"
	"strings"
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
// "<namespace>/<slug>" where each segment is lowercase letters, digits, and
// hyphens (the design's namespaced-slug discipline). Enforcing it at ingest
// closes terminal-injection on `registry list/get` (no control/escape chars land
// in the primary key) and the latent P2.4 export path-traversal seed (no '/'
// beyond the single separator, no "..", no control chars) BEFORE fan-out.
var templateIDPattern = regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9-]+$`)

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

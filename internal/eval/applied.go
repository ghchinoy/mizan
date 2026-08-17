package eval

// applied.go carries the RESOLVED autorater-as-applied that Engine.Run records on
// every Result (eval-results-store-design §4.3). It is additive: the zero value is
// valid and callers that ignore it are unaffected. This package MUST NOT import
// internal/results — the dependency runs results→eval, never the reverse; the
// results store reads Result.Applied, it does not reach back into eval.

// AppliedAutorater is the RESOLVED autorater configuration actually used for a
// run (post model-precedence + global-routing) — not the template's declared
// value. Populated by Engine.Run so callers (the results store) can record
// what actually ran. Additive; zero value for callers that ignore it.
type AppliedAutorater struct {
	Model         string `json:"model,omitempty"`          // resolved model id actually used (post precedence chain)
	SamplingCount int32  `json:"sampling_count,omitempty"` // from tmpl.SamplingCount as applied
	FlipEnabled   bool   `json:"flip_enabled,omitempty"`   // from tmpl.FlipEnabled as applied
	// EffectiveHost is "regional" | "global" — the host predicted by the pre-flight
	// Resolve (the resolved/predicted route from the model-precedence + R-GLOBAL
	// prefix decision), NOT a post-retry observation. A global-only judge discovered
	// only via the run-time retry (not in the prefix table) is not reflected here.
	EffectiveHost string `json:"effective_host,omitempty"`
	Location      string `json:"location,omitempty"`     // region the call targeted (e.g. "us-central1" or "global")
	ModelSource   string `json:"model_source,omitempty"` // "flag" | "template" | "config-default" | "builtin" (WHY this model)
}

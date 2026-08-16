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
	Model         string // resolved model id actually used (post precedence chain)
	SamplingCount int32  // from tmpl.SamplingCount as applied
	FlipEnabled   bool   // from tmpl.FlipEnabled as applied
	EffectiveHost string // "regional" | "global" (the R-GLOBAL routing actually taken)
	Location      string // region the call targeted (e.g. "us-central1" or "global")
	ModelSource   string // "flag" | "template" | "config-default" | "builtin" (WHY this model)
}

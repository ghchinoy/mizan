package eval

// custom.go will hold the genai custom_schema fallback path (KindCustomSchema),
// a direct genai.GenerateContent call with a strict ResponseSchema and
// exponential backoff. Unlike native EvaluateInstances, the genai path DOES
// accept inline bytes (spike-core) — keep its content converter distinct from
// the native one (content.go).
//
// This path is NOT part of the P1 vertical slice; the engine returns a clear
// "not implemented in P1 slice" error for KindCustomSchema (see engine.go).
// It is implemented in WI-P1-5. Kept as an empty, clearly-marked file so the
// slice stays scoped to text pointwise and the seam is visible to WI-P1-5.

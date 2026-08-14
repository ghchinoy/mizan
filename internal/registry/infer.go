package registry

// infer.go holds the placeholder auto-inference used by the CLI's
// `registry create --infer-inputs` / `registry update --infer-inputs`
// (placeholder-inference design, decisions D1–D7). Inference is a READ-ONLY scan
// of author-written prompt text: it never mutates the template engine, the
// {{response}} handling elsewhere, or existing METADATA-FLAGS --input behavior.

// reservedInputNames is the single source of truth for template placeholder
// names that are NOT template inputs and are therefore EXCLUDED from
// --infer-inputs inference (design D3). Today it holds one token:
//
//   - "response": the MODEL OUTPUT a pointwise/single judge scores (see
//     internal/eval — {{response}} is the canonical example of the scored
//     output), not an author-provided input.
//
// If further reserved template tokens are introduced, add them HERE rather than
// hand-maintaining a second list at the call site.
var reservedInputNames = map[string]bool{
	"response": true,
}

// IsReservedInputName reports whether name is a reserved template token that
// inference must never turn into a declared input (design D3). It is the exported
// accessor over reservedInputNames so callers and tests share one source of
// truth.
func IsReservedInputName(name string) bool {
	return reservedInputNames[name]
}

// InferInputs scans the given prompt text field(s) for double-brace {{name}}
// placeholders and returns InputSpecs for every referenced name that is NOT
// already declared and NOT reserved. It powers the opt-in `--infer-inputs` flag
// (design D1).
//
// Semantics (design D2/D3/D4/D5):
//   - Placeholder grammar is the package placeholderPattern (the SAME regex
//     validation uses in validate.go), so a name inference adds is exactly a name
//     validation recognizes — no second grammar is maintained.
//   - Names are deduplicated and returned in first-seen order across texts (so a
//     placeholder repeated, or appearing in a later field, is emitted once).
//   - Reserved tokens (IsReservedInputName, e.g. "response") are excluded.
//   - A name already present in `declared` is skipped — inference is ADDITIVE and
//     NON-DESTRUCTIVE: it only ADDS placeholders the author did not declare via
//     --input, and never overrides, reorders, or drops an explicit entry (D5).
//   - Each inferred input defaults to modality=text, required=true (D4).
//
// It returns ONLY the newly inferred inputs; the caller appends them AFTER the
// explicit set so explicit entries keep their position and win on any name
// collision.
func InferInputs(declared []InputSpec, texts ...string) []InputSpec {
	have := make(map[string]bool, len(declared))
	for _, in := range declared {
		have[in.Name] = true
	}
	seen := map[string]bool{}
	var out []InputSpec
	for _, text := range texts {
		for _, m := range placeholderPattern.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if IsReservedInputName(name) || have[name] || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, InputSpec{Name: name, Modality: ModalityText, Required: true})
		}
	}
	return out
}

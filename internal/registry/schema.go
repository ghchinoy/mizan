package registry

import _ "embed"

// MetricTemplateSchemaJSON is the STRICT JSON Schema for a pack MetricTemplate
// file (design §3.5). It is embedded so the creds-free validator
// (`mizan pack validate`, P2.2) and the templates-repo CI can consume it from
// the binary without a filesystem dependency. The codec's Unmarshal performs
// only lightweight tolerant checks for import (manifest kind, required id/kind,
// kind normalization); the strict, additionalProperties:false schema enforcement
// — the six-token spec.kind enum and the kind-specific/placeholder checks — is
// the `pack validate` boundary (design §3.5, INFO-1 from the P2.1 audit).
// Exported so `pack validate` (and tests) reference the single source of truth.
//
//go:embed schema/metrictemplate.json
var MetricTemplateSchemaJSON []byte

// EvalSetSchemaJSON is the FORMAT-ONLY JSON Schema for a pack EvalSet file
// (design §3.4a, D6). P2 carries and validates the EvalSet manifest as a
// schema-governed unit; it is deliberately NOT imported into the registry store
// or run (that is the separate eval-set capability). The schema validates only
// the required metadata/spec.members core and ALLOWS the reserved-but-not-
// interpreted optional objects (spec.inputs, per-member bind/weight/required,
// aggregation/display). Exported so `pack validate` (and tests) reference the
// single source of truth.
//
//go:embed schema/evalset.json
var EvalSetSchemaJSON []byte

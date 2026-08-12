package registry

import _ "embed"

// MetricTemplateSchemaJSON is the MINIMAL JSON Schema for a pack MetricTemplate
// file (design §3.5). It is embedded so the eventual creds-free validator
// (`mizan pack validate`, P2.2) and the templates-repo CI can consume it from
// the binary without a filesystem dependency. In P2.1 the codec performs only
// lightweight Go-level structural checks (manifest kind, required id/kind, kind
// normalization); the full schema-driven enforcement — the six-token spec.kind
// enum and the kind-specific/placeholder checks — lands in P2.2. Exported so
// P2.2 (and tests) can reference the single source of truth.
//
//go:embed schema/metrictemplate.json
var MetricTemplateSchemaJSON []byte

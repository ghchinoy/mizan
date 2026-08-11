# ITEM C — Independent test-coverage report (test-itemc-1)

**PR:** #36 — `feat/vernacular-aliases`
**Base tested at:** `60f4759373531086d66b2854931675ea2a350be2` (off `main`)
**Repo:** github.com/ghchinoy/mizan
**Author of code under test:** dev-itemc-1 (I did NOT write the production code).

## Verdict

Coverage was **strong but not complete**. The dev-authored tests fully cover
`NormalizeKind`, the `--kind` value aliases on **create**, and the `eval
single/compare` subcommand aliases. Three genuine gaps remained; I added tests
to close all three. Most important: the brief's explicit ask for a check that
**the emitted Vertex spec is identical for single vs pointwise and compare vs
pairwise** was not tested at all — only the stored `Kind` value was compared. It
is now covered by an end-to-end engine test that compares the actual
`EvaluateInstancesRequest` with `proto.Equal`.

All gates green after my additions.

## Behavior-under-test → coverage matrix

| Behavior (from brief) | Pre-existing coverage | Gap? | Now covered by |
|---|---|---|---|
| `NormalizeKind` mapping table (all accepted spellings) | `internal/registry/kind_test.go:TestNormalizeKindAliases` | No | — |
| Unknown kind rejected | `kind_test.go:TestNormalizeKindUnknownErrors`; `registry_kind_alias_test.go:TestKindFlagUnknownErrors` | Partial — error text not pinned | strengthened `TestKindFlagUnknownErrors` to assert the full enumeration |
| `--kind single/compare` on **create** | `registry_kind_alias_test.go:TestKindFlagAliasesNormalize` / `TestKindFlagSingleEqualsPointwise` | No | — |
| `--kind single/compare` on **update** | *none* | **Yes** | `TestKindFlagAliasNormalizesOnUpdate`, `TestKindFlagUpdatePreservesExistingKind`, `TestKindFlagAliasEqualsCanonicalOnUpdate` |
| `--kind single/compare` on **list** filter | *none* (only unit-level `NormalizeKind`) | **Yes** | `TestRegistryListKindAliasFiltersSameAsCanonical`, `TestRegistryListKindUnknownErrors` |
| `registry create` persists **canonical** kind (alias never stored) | *none* (apply() unit test only, not through the DB) | **Yes** | `TestRegistryCreateWithKindAliasPersistsCanonical` |
| `eval single` == `eval run`; `eval compare` == `eval pairwise` | `eval_aliases_test.go` (3 tests) | No | — |
| Backward compat: pointwise/pairwise/rubric/custom_schema unchanged | `kind_test.go`, `TestValidateTemplate`, engine tests | No | — |
| **Vertex request spec identical: single vs pointwise, compare vs pairwise** | *none* (only stored `Kind` compared) | **Yes (brief's key ask)** | `TestVertexSpecIdenticalSingleVsPointwise`, `TestVertexSpecIdenticalCompareVsPairwise`, `TestUnnormalizedAliasNeverReachesSpec` |

## Tests added

### `internal/eval/kind_alias_spec_test.go` (new)
- `TestVertexSpecIdenticalSingleVsPointwise` (:40) — builds a pointwise template
  whose `Kind` is folded from `single` vs `pointwise` via `NormalizeKind`, runs
  both through the real `Engine` (fake `EvaluationClient` capturing the request),
  and asserts the two `EvaluateInstancesRequest`s are byte-identical
  (`proto.Equal`) and carry a `PointwiseMetricInput`.
- `TestVertexSpecIdenticalCompareVsPairwise` (:88) — pairwise counterpart;
  asserts identical `PairwiseMetricInput` requests.
- `TestUnnormalizedAliasNeverReachesSpec` (:123) — negative guard: if an
  un-normalized alias (`single`/`compare`) ever reached `MetricTemplate.Kind`,
  the engine errors and emits **no** request — proving normalization at the
  boundary is load-bearing, not cosmetic.

### `cmd/mizan/registry_kind_alias_e2e_test.go` (new)
- `TestRegistryCreateWithKindAliasPersistsCanonical` (:51) — `registry create
  --kind single|compare` end-to-end into a throwaway SQLite DB; asserts the
  stored `Kind` is the canonical `pointwise`/`pairwise`.
- `TestRegistryListKindAliasFiltersSameAsCanonical` (:88) — seeds one pointwise +
  one pairwise template, then asserts `list --kind single` returns exactly the
  pointwise set and equals `list --kind pointwise` (same for `compare` vs
  `pairwise`).
- `TestRegistryListKindUnknownErrors` (:123) — `list --kind bogus` errors with
  `unknown metric kind`.

### `cmd/mizan/registry_kind_alias_test.go` (extended)
- `TestKindFlagUnknownErrors` (:67) — strengthened to assert the error enumerates
  `single|pointwise`, `compare|pairwise`, `rubric`, `custom_schema` (guards the
  help/guidance text against silent regression).
- `TestKindFlagAliasNormalizesOnUpdate` (:90) — update path folds `--kind
  compare` → `KindPairwise` (the update branch uses hand-written gating, distinct
  from create).
- `TestKindFlagUpdatePreservesExistingKind` (:106) — a metadata-only update
  (no `--kind`) preserves the stored `pairwise`/`rubric`/`custom_schema` kind and
  does NOT reset it to the flag default `pointwise`.
- `TestKindFlagAliasEqualsCanonicalOnUpdate` (:123) — update via alias == update
  via canonical spelling.

## Green-gate confirmation (CGO-free, `CGO_ENABLED=0`)

| Gate | Result |
|---|---|
| `go build ./...` | OK |
| `go vet ./...` | OK |
| `gofmt -l .` | empty |
| `go test ./...` | all packages pass |
| `golangci-lint run ./...` (v2.12.2) | 0 issues |
| `govulncheck ./...` (v1.6.0) | 0 vulnerabilities called; 1 advisory in a required-but-uncalled module (pre-existing, matches dev log) |

No golden files changed — this change touches help text and the parse boundary,
not renderer output; goldens were correctly left untouched.

## Recommendations (non-blocking, for the manager)

1. **`list` normalizes after opening the DB.** In `newRegistryListCmd`,
   `registry.NormalizeKind(kind)` runs *after* `mustConfig()` +
   `wire.OpenService()`. An obviously-bad `--kind` still pays for a DB open before
   failing. Minor; a cheaper UX would validate the flag before opening the store.
   Not a correctness bug — flagged only as a small ordering nit.
2. **Future template-file codec must call `NormalizeKind`.** The dev log notes
   there is no YAML/JSON `kind:` parser yet; when one is added it becomes a new
   parse boundary and must reuse `NormalizeKind` to inherit the aliases. Worth a
   test at that point.

# ITEM C — backward-compatible single/compare vernacular aliases (dev-itemc-1)

Branch: `feat/vernacular-aliases` (off `origin/main`, which includes merged #35
`--project` + FIX-SRC work).

## Goal

Add plain-vernacular aliases to the eval CLI so `single` = pointwise (score ONE
response) and `compare` = pairwise (compare TWO responses, pick the better),
while keeping the existing `pointwise`/`pairwise` spellings working unchanged.
Code-only; prose docs (user-guide / llm-as-judge-scenarios / testing-guide) are
handled separately in the docs passes (ITEM D). This log is the precise surface
description the docs passes will transcribe.

## How `kind` is expressed today (inspection result)

- **Subcommands**: `mizan eval run` is the pointwise entry (also dispatches
  rubric / custom_schema by template kind); `mizan eval pairwise` is the pairwise
  entry. There is no `--kind` flag on the `eval` commands.
- **Kind VALUE flag**: `mizan registry create|update --kind <value>` and the
  `mizan registry list --kind <value>` filter. Default create value is
  `pointwise`.
- **Template `kind:` field**: there is currently **no** YAML/JSON template codec
  in the repo that parses a `kind:` field — templates enter the registry only via
  the `--kind` authoring flag, and the sqlite store round-trips the already-
  canonical stored string. So the only kind-VALUE parse boundaries that exist
  today are the two `--kind` flags above. When a template file codec is added
  later, it must call `registry.NormalizeKind` at its parse boundary to inherit
  the same aliases.
- **Canonical kinds** live in `internal/registry/model.go`
  (`KindPointwise`/`KindPairwise`/`KindRubric`/`KindCustomSchema`); the eval
  engine dispatches on `MetricTemplate.Kind` (`internal/eval/engine.go`) to build
  the Vertex `PointwiseMetricSpec` / `PairwiseMetricSpec`.

## Exact CLI surface landed

### Subcommand aliases (cobra `Aliases`)

- `mizan eval run` gains alias **`single`** → `mizan eval single` == `mizan eval
  run`.
- `mizan eval pairwise` gains alias **`compare`** → `mizan eval compare` ==
  `mizan eval pairwise`.

`eval --help` now lists:

```
  pairwise    compare (a.k.a. pairwise) — compare two responses (baseline vs candidate) and pick the better
  run         single (a.k.a. pointwise) — score one response (also rubric/custom_schema) against a live eval call
```

`eval run --help` shows `Aliases: run, single` and the Long text opens with:

> Also available as `mizan eval single` — score ONE response (a.k.a. pointwise).
> The compare/pairwise counterpart is `mizan eval compare`.

`eval pairwise --help` shows `Aliases: pairwise, compare` and the Long text opens
with:

> Run a pairwise metric template. Also available as `mizan eval compare` —
> compare TWO responses (a.k.a. pairwise). The single/pointwise counterpart is
> `mizan eval single`.

### Kind VALUE aliases (`--kind`)

Both `registry create`/`registry update` `--kind` and the `registry list --kind`
filter now accept `single` and `compare` in addition to
`pointwise`/`pairwise`/`rubric`/`custom_schema`.

- `registry create --help` gloss for `--kind`:

  ```
  metric kind: single (a.k.a. pointwise) — score one response | compare (a.k.a. pairwise) — compare two | rubric | custom_schema
  ```
  (default `pointwise`)

- `registry list --help` gloss for `--kind`:

  ```
  filter by metric kind (accepts single|pointwise, compare|pairwise, rubric, custom_schema)
  ```

### Accepted kind values (canonical + aliases)

| Spelling accepted | Canonical kind |
|---|---|
| `single`, `pointwise` | `pointwise` |
| `compare`, `pairwise` | `pairwise` |
| `rubric` | `rubric` |
| `custom_schema` | `custom_schema` |

Any other value → clear error: `unknown metric kind "<value>" (want one of:
single|pointwise, compare|pairwise, rubric, custom_schema)`.

## How normalization works

- New `registry.NormalizeKind(s string) (MetricKind, error)` in
  `internal/registry/model.go` is the single parse-boundary normalizer. It folds
  `single → pointwise`, `compare → pairwise`, passes canonical kinds through
  unchanged, and rejects anything else with a clear error.
- New alias constants `registry.KindAliasSingle` / `registry.KindAliasCompare`.
- Wired at the boundary only:
  - `cmd/mizan/registry.go` `templateFlags.apply` — `--kind` is normalized to a
    canonical `MetricKind` before it is stored on the template (replacing the raw
    `registry.MetricKind(f.kind)` cast). Same `!update || changed("kind")` gating
    as before.
  - `cmd/mizan/registry.go` `newRegistryListCmd` — the `--kind` filter value is
    normalized before building `ListFilter.Kinds`.
  - `cmd/mizan/eval.go` — cobra `Aliases` on the `run` and `pairwise` commands.
- The alias strings are **never** threaded deeper than the parse boundary:
  `MetricTemplate.Kind` is always one of the four canonical kinds, so the eval
  engine and the emitted Vertex specs (`PointwiseMetricSpec` /
  `PairwiseMetricSpec`) are unchanged regardless of which spelling was used.

## Tests added

- `internal/registry/kind_test.go` — `NormalizeKind` alias folding + unknown
  errors.
- `cmd/mizan/registry_kind_alias_test.go` — `--kind single`/`compare` normalize
  through the real flag→apply path; `single`==`pointwise` and
  `compare`==`pairwise`; unknown kind errors.
- `cmd/mizan/eval_aliases_test.go` — `eval single`/`eval compare` resolve to the
  same cobra command as `run`/`pairwise` (via `Command.Find`), and invoking via
  the alias hits the same RunE (fails on the shared `--metric` guard).

No golden files changed — the goldens under `cmd/mizan/testdata/` pin renderer
output, not help text, and the renderers were untouched.

## Verification (all green, cgo-free / CGO_ENABLED=0)

- `go build ./...` — OK
- `go vet ./...` — OK
- `gofmt -l .` — empty
- `go test ./...` — all packages pass
- `golangci-lint run` — 0 issues (v2.12.2)
- `govulncheck ./...` — no vulnerabilities in code; the one advisory is in a
  required module the code does not call

## Scope / DoD note

User-facing prose docs (user-guide.md / llm-as-judge-scenarios.md /
testing-guide.md) intentionally NOT touched here to avoid same-file collisions;
the docs adoption of this vernacular is ITEM D in the docs passes. The PR body
carries the doc-drift-guard override marker accordingly.

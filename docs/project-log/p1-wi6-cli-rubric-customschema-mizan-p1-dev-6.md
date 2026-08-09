# WI-P1-6 completion: CLI authoring for rubric + custom_schema

**Agent:** mizan-p1-dev-6
**Branch:** `feat/p1-cli-rubric-customschema` (from `main` @ a46ff28)
**PR:** https://github.com/ghchinoy/mizan/pull/11 (base `main`, do not merge — eng-manager gates)

## The gap
`mizan registry create --kind rubric|custom_schema` succeeded but there was **no
CLI flag to populate the required data** (`RubricGroups` / `ResponseSchema`), so a
later `eval run` failed with "has no rubric groups" / "has no response schema".
The engine already supports both kinds; only the CLI authoring path was missing.

Scope was **cmd/mizan only** — `internal/eval` and the registry model are
unchanged (the `RubricGroups map[string][]string` and `ResponseSchema *Schema`
fields already existed). The SQLite store already round-trips both fields
(`internal/registry/sqlite/sqlite.go` marshals/unmarshals them), so no storage
work was needed.

## Flag syntax chosen

### Rubric (`--kind rubric`)
- `--rubric-group "name=criterion one;criterion two"` — **repeatable**. Uses
  cobra `StringArrayVar` (not `StringSliceVar`) so a criterion may contain commas;
  criteria are split on `;`, trimmed, and empties dropped. **The same group name
  across multiple flags accumulates.** Malformed spec (no `=`, or empty name) is a
  clear error.
- `--rubric-groups-file <path>` — JSON object `{"group":["c1","c2"], ...}`.

### custom_schema (`--kind custom_schema`)
- `--response-schema '<inline json>'`
- `--response-schema-file <path>`
- Content must be **well-formed JSON** (`json.Valid`); otherwise a clear error.
- Stored as `t.ResponseSchema = &registry.Schema{JSON: <raw>}`.

### Precedence (documented in code)
- **Rubric:** `--rubric-groups-file` is parsed first to seed the map; then
  `--rubric-group` flags merge in, **overriding the file's entry per group name**.
- **custom_schema:** `--response-schema-file` takes precedence over inline
  `--response-schema` when both are given.

### Safe file reads
`readTemplateFile` mirrors the asset stager's guards
(`internal/asset/gcs.go`): resolve symlinks, require a **regular file** (rejects
dirs/devices/FIFOs/sockets), and cap size at **1 MiB** (`maxTemplateFileBytes`).
No unbounded read.

## Validation (fail at create, not at eval)
`validateTemplate` runs on the final template. On **create** it runs *before*
opening the DB (fail-fast); on **update** it runs on the merged template after
`Get`. `rubric` with no groups and `custom_schema` with no/blank schema each
produce an actionable error naming the flags to pass. Pointwise/pairwise behavior
unchanged; update is not over-constrained (existing data satisfies validation).

`apply()` returns an error now and wires the new fields through the existing
`set(...)`/`Changed()` update-safe pattern: on update the rubric/schema fields are
left untouched unless a corresponding flag was explicitly set.

## Visibility (`registry get`)
- JSON output already serialized the whole template (covered).
- Table output (`renderTemplate`) now prints `RubricGroup[<name>]:` rows (sorted)
  and a `ResponseSchema:` row.
- `eval run` table output (`renderResult`) now prints `CustomOutput[<key>]:` rows.

## Dependency direction
cmd/mizan non-test imports unchanged: only `internal/{config,eval,registry,wire}`
(new code uses stdlib `encoding/json`, `os`, `path/filepath`, `sort`). cgo-free
preserved; no import cycle.

## Acceptance / verification
- `go build ./...` — OK
- `go vet ./...` and `go vet -tags integration ./...` — clean
- `gofmt -l cmd/mizan/` — clean
- `CGO_ENABLED=0 go build ./cmd/mizan` — OK
- `CGO_ENABLED=0 go test ./...` — PASS (all packages)
- `govulncheck ./...` — **reachable = 0** ("Your code is affected by 0
  vulnerabilities"; 1 vuln in a required module but not called)

## LIVE end-to-end (PROJECT_ID=ghchinoy-genai-sa)

### CLI-level integration tests (`-tags integration`)
`PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./cmd/mizan/ -run 'TestLiveCLIRubric|TestLiveCLICustomSchema' -v`
→ **PASS** (rubric: create → eval run returned Score=5 + explanation;
custom_schema: create → eval run returned parsed CustomOutput with all required
keys). Full output in the test logs.

### Compiled binary (`/tmp/mizan`, `MIZAN_REGISTRY_DB` = temp)

**Rubric — create then eval run**
```
$ mizan registry create --id test/bin-rubric --name "Bin Rubric" --kind rubric \
    --prompt "Rate this ad on 1-5.\n\nAd:\n{{copy}}" --sampling-count 1 \
    --rubric-group "clarity=Offer is clear;No jargon" --rubric-group "tone=Professional voice"
ID:                    test/bin-rubric
Kind:                  rubric
RubricGroup[clarity]:  Offer is clear; No jargon
RubricGroup[tone]:     Professional voice

$ mizan eval run --metric test/bin-rubric \
    --field "copy=Open Settings, tap Security, follow the secure link we email you."
Score:        5
Explanation:  The ad provides clear, step-by-step instructions without jargon, and
              maintains a professional and direct tone, meeting all criteria.
```

**custom_schema — create (via --response-schema-file) then eval run**
```
$ mizan registry create --id test/bin-custom --name "Bin Custom" --kind custom_schema \
    --prompt "Audit this ad and score it. Copy:\n{{copy}}" \
    --system "You are a strict brand auditor. Respond only with the requested JSON." \
    --response-schema-file /tmp/schema.json
ID:              test/bin-custom
Kind:            custom_schema
ResponseSchema:  {"type":"object","properties":{"overall_score":...},...} …

$ mizan eval run --metric test/bin-custom \
    --field "copy=BUY NOW!!! GUARANTEED best deal EVER, act fast!!!"
Score:                        (none)
CustomOutput[compliant]:      false
CustomOutput[explanation]:    The ad copy uses excessive capitalization and exclamation
                              marks, creating an aggressive and spammy tone. ...
CustomOutput[overall_score]:  1
```

**Early validation (fails at create, before any eval)**
```
$ mizan registry create --id test/x --kind rubric
error: kind "rubric" requires rubric groups; pass --rubric-group "name=crit1;crit2" (repeatable) or --rubric-groups-file <path>   (exit 1)

$ mizan registry create --id test/y --kind custom_schema
error: kind "custom_schema" requires a response schema; pass --response-schema '<json>' or --response-schema-file <path>           (exit 1)

$ mizan registry create --id test/z --kind custom_schema --response-schema '{bad'
error: response schema is not well-formed JSON                                                                                     (exit 1)
```

## Per-criterion status
| Criterion | Status |
|---|---|
| Rubric flags (`--rubric-group` + `--rubric-groups-file`, precedence) | ✅ |
| custom_schema flags (`--response-schema` + `--response-schema-file`, JSON validation) | ✅ |
| Early validation at create/update | ✅ |
| `registry get` visibility (JSON + table) | ✅ |
| Unit tests (cgo/network-free) | ✅ PASS |
| Integration tests (build tag, PROJECT_ID) | ✅ PASS (live) |
| build/vet/gofmt/CGO_ENABLED=0 build | ✅ |
| `CGO_ENABLED=0 go test ./...` | ✅ PASS |
| govulncheck reachable=0 | ✅ |
| LIVE end-to-end both kinds via CLI | ✅ |
| Dep direction / cgo-free / no cycle | ✅ unchanged |

## Notes
- Files touched: `cmd/mizan/registry.go`, `cmd/mizan/helpers.go`,
  `cmd/mizan/eval.go`, plus `cmd/mizan/registry_authoring_test.go` and
  `cmd/mizan/registry_authoring_integration_test.go`.
- `internal/eval` and the registry model were **not** modified.

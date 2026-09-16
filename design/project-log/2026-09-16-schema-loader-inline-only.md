# Schema-loader hardening — inline-only `$ref` (fast-follow to PR #92)

- **Date:** 2026-09-16
- **Author:** dev-sec-loader (developer)
- **Branch:** `fix/schema-loader-inline-only`
- **PR:** https://github.com/ghchinoy/mizan/pull/96
- **Spec:** `sec-loader-audit-finding.md` (audit-sec-loader) — implemented §4 (fix) and §5 (tests) exactly.

## What & why

Every `jsonschema.Compiler` in the tree used the default `santhosh-tekuri/jsonschema`
`file` loader. An **untrusted pack** could embed a schema with an external
`$ref` (`file://`, `http(s)://`, or a relative path) and trigger `os.Open` on
the attacker-controlled path at `Compile` time — local-file probing plus a CI
**OOM/hang** vector via `{"$ref":"file:///dev/zero"}` (unbounded read, no size
cap). Audit §3 rated it MEDIUM (3 attacker-reachable sites) + LOW (1 embedded,
defense-in-depth).

## Fix (audit §4)

New `internal/registry/schemaloader.go`:
- `refuseExternalRefs(uri)` — loader that rejects every external ref.
- `NewInlineOnlyCompiler()` — sets the **per-instance** `Compiler.LoadURL` to it.
  No process-global mutation, no dep bump. Same-document `#/...` refs never reach
  the loader, so legitimate inline schemas are unchanged.

Replaced `jsonschema.NewCompiler()` at all 4 sites:
- `internal/registry/validate.go`: `validateHeuristicSchema`, `validateResponseSchema`, `mustCompileSchema`.
- `internal/eval/heuristic.go`: `compileHeuristicSchema` → `registry.NewInlineOnlyCompiler()`.

No other call sites exist (audit-confirmed); `evalset_test.go` fixture left as-is.

## Tests (audit §5 — all 7)

- `internal/registry/schemaloader_test.go` — reject file://, relative, http(s):// at
  `validateHeuristicSchema` + `validateResponseSchema`; DoS guard (file:///dev/zero
  returns immediately, non-Windows); allow inline `#/$defs` ref (compile + pass/fail).
- `internal/eval/heuristic_schemaloader_test.go` — same rejections + DoS + inline-allow
  through the eval-run path (`eng.Run` / `compileHeuristicSchema`).
- `internal/registry/validate_pack_schemaloader_test.go` — end-to-end `pack validate`
  over a heuristic + custom_schema pack each with a file:// $ref → validation errors.

## Folded in from #92 review (non-blocking, all addressed)

1. `Heuristic` subcase in `TestContentHashSensitivePerField`.
2. Shared `registry.CompileHeuristicRegex` helper reused by the 3 previously-
   duplicated `(?i)` sites (`cmd/mizan/registry.go`, `internal/registry/validate.go`,
   `internal/eval/heuristic.go`) — behavior identical.
3. `eval run --model` on a `kind: heuristic` metric now warns the flag is ignored.

## Docs (audit §7)

`docs/user-guide.md`: schema `$ref` is restricted to inline same-document references.

## CI gate — all green (no dep bump)

| Check | Result |
|-------|--------|
| `CGO_ENABLED=0 go build ./...` | pass |
| `go vet ./...` | pass |
| `gofmt -l .` | clean |
| `go test ./...` | pass |
| `golangci-lint run` | 0 issues |
| `govulncheck ./...` | 0 vulnerabilities affect this code (pre-existing import-only advisories unchanged) |

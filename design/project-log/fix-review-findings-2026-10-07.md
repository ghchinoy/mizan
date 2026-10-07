# Fix — `mizan mcp` review-finding remediation (#114)

**Agent:** mizan-mcp-dev (Developer) · **Date:** 2026-10-07 · **Branch:** `feat/mizan-mcp-114`
**Base HEAD:** `5207fdd` (test-engineer coverage pass) · **Reviews:** security-audit.md, code-review.md, test-report.md

Remediated the three quality-gate reviews. The implementation was strong and faithful; these
fixes are small, localized, and preserve the SDK-containment rule and every pre-existing test.

## MUST FIX (blocking)

### 1. [CRITICAL / R1] Fail-closed HTTP auth
- **Changed:** `cmd/mizan/mcp_auth.go` — DELETED the `mcpDevSigningKey` constant and the silent
  fallback + `usingDevKey()`. `newBearerAuth()` now returns `(*bearerAuth, error)` and FAILS when
  `JWT_SIGNING_KEY` is empty or `< 32` bytes (`minSigningKeyBytes`). `cmd/mizan/mcp_http.go:RunE`
  builds the gate FIRST (before opening any service) and returns the error, so the command never
  serves with a shipped/default key. `DISABLE_AUTH=true` remains the only no-auth path; the
  fail-safe (auth ON unless `DISABLE_AUTH=="true"` exactly) and the no-alg-confusion keyfunc are
  unchanged.
- **Proven by:** `TestNewBearerAuthFailsClosedWithoutKey` (unset + too-short → error),
  `TestNewBearerAuthUsesEnvKey` (real ≥32-byte key mints+validates end-to-end), existing
  `TestDisableAuthBypass` (no-auth path still works). Runtime: `go run ./cmd/mizan mcp http` with
  no key/no `DISABLE_AUTH` exits 1 with the JWT_SIGNING_KEY instruction; with `DISABLE_AUTH=true`
  it passes the gate (fails later only on project config, as expected).
- **Dev key constant:** GONE — `grep -r mcpDevSigningKey` returns nothing.

### 2. [HIGH] `file:` is a local-transport-only affordance
- **Changed:** added `AllowLocalFiles bool` to `mcpserver.Deps` (`internal/mcpserver/server.go`).
  Threaded into `assetRef`/`instanceFromFields` (`mapping.go`) and the `run`/`pairwise` handlers
  (`handlers.go`). When a field uses `{file: ...}` and `AllowLocalFiles` is false it returns
  `field %q: local file inputs are disabled on this transport; use gcs: or text:`. Wired in the cmd
  layer: `buildMcpServer(ctx, allowLocalFiles)`; `mizan mcp stdio` passes `true` (local trust),
  `mizan mcp http` passes `false` by default with an opt-in `--allow-local-files` flag (documented
  unsafe).
- **Proven by:** `TestAssetRefLocalFileGate` (reject when false, accept→FilePath when true, gcs
  unaffected), `TestEvalRunLocalFileRejectedWhenDisabled` (handler tool-error before engine runs,
  `fake.Calls()==0`), `TestMcpStdioHasNoAllowLocalFilesFlag` + `TestMcpHTTPHasAllowLocalFilesFlagDefaultFalse`.

### 3. [MEDIUM] HTTP bind defaults to loopback
- **Changed:** `cmd/mizan/mcp_http.go` — added `--address` flag defaulting to `127.0.0.1`;
  `server.Addr = fmt.Sprintf("%s:%d", address, port)`. Long-help documents that `0.0.0.0` exposes
  all interfaces and requires a real `JWT_SIGNING_KEY`.
- **Proven by:** `TestMcpHTTPAddressDefaultsToLoopback`.

## SHOULD FIX

### 4. [LOW] Drop `?token=` query-param auth
- **Changed:** `cmd/mizan/mcp_auth.go:requireBearer` now reads the bearer token from the
  `Authorization` header ONLY (removed the `r.URL.Query().Get("token")` fallback). `grep` for the
  query path returns nothing.

### 5. [LOW] Origin protection
- **Changed:** `cmd/mizan/mcp_http.go` wraps the streamable handler with
  `http.NewCrossOriginProtection().Handler(...)` (go-sdk v1.7.0 origin verification is off by
  default). Applied under the auth gate.

### 6. [LOW] Require token expiry
- **Changed:** added `jwt.WithExpirationRequired()` to the parser options, so a no-`exp` token is
  rejected. `generateToken` already always sets `exp` (confirmed).
- **Proven by:** existing `TestRequireBearerExpiredTokenRejected`; valid-token tests still pass.

### 7. Design fidelity — `responseSchema`
- **Changed:** added `ResponseSchema string` to `GetMetricOut` (`types.go`) and projected
  `MetricTemplate.ResponseSchema.JSON` in `metricDetail` (`mapping.go`) for `custom_schema` metrics
  (design §4).
- **Proven by:** `TestGetMetricProjectsResponseSchema`.

### 8. Nits
- **O2:** corrected the `generateToken` doc comment — it is unexported/test-only, not an operator
  mint path.
- **O3:** replaced the hand-rolled `joinAnd` with `strings.Join(missing, " and ")` (identical
  output; existing pairwise-missing-field tests still pass).

## DOCUMENT — confused-deputy residuals (§8 Q5)

### 9. Least-privilege service-account note + optional allowlist
- **Documented:** a prominent SECURITY block in the `mizan mcp http` long-help and a new usage note
  `docs/mcp.md` stating the per-call `project`/`location` override and `gcs:` pass-through run under
  the SERVER's ADC/service-account (confused-deputy, BY DESIGN per locked §8 Q5) and that the HTTP
  server's service account MUST be least-privilege, scoped to exactly the intended project(s)/
  bucket(s). The locked behavior was NOT silently changed.
- **Optional allowlist (INCLUDED — low cost, default-preserving):** added `AllowedProjects []string`
  to `Deps`, enforced in `engineForCall` (`handlers.go`): an EMPTY list allows all (default, current
  behavior preserved); when set, a per-call `project` override outside the list is rejected with a
  tool error. Wired via env `MIZAN_MCP_ALLOWED_PROJECTS` (CSV) in `buildMcpServer`
  (`parseAllowedProjects`). No `gcs:`-bucket allowlist was added (documentation only). This is an
  opt-in knob for the OWNER to decide at sign-off; the default is unchanged.
- **Proven by:** `TestEvalRunProjectAllowlist` (out-of-list rejected before the factory; in-list
  routed through it), `TestParseAllowedProjects`.

## Tests added
- `internal/mcpserver/remediation_test.go`: `TestAssetRefLocalFileGate`,
  `TestEvalRunLocalFileRejectedWhenDisabled`, `TestEvalRunStoreFailureWarning`
  (test-report gap — non-fatal store-failure warning path, no runId, eval result still returned),
  `TestGetMetricProjectsResponseSchema`, `TestEvalRunProjectAllowlist`.
- `cmd/mizan/mcp_auth_test.go`: reworked `TestNewBearerAuthUsesEnvKey`, replaced
  `TestNewBearerAuthFallsBackToDevKey` with `TestNewBearerAuthFailsClosedWithoutKey`.
- `cmd/mizan/mcp_cmd_test.go`: `TestMcpHTTPAddressDefaultsToLoopback`,
  `TestMcpHTTPHasAllowLocalFilesFlagDefaultFalse`, `TestMcpStdioHasNoAllowLocalFilesFlag`,
  `TestParseAllowedProjects`.
- Updated `internal/mcpserver/handlers_more_test.go` `TestAssetRefMapping` for the new `assetRef`
  signature.

## Gates
- `go build ./...` → OK.
- `go vet ./cmd/... ./internal/mcpserver/...` → clean.
- `go test ./...` → PASS (full suite, no network/ADC; `GOOGLE_APPLICATION_CREDENTIALS` unset).
- SDK containment intact: go-sdk imported only by `internal/mcpserver/server.go`,
  `cmd/mizan/mcp_stdio.go`, `cmd/mizan/mcp_http.go` (+ the two smoke tests); `mcp_auth.go` is
  SDK-free.

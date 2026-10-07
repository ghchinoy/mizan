# Phase B — mizan mcp cmd-layer transports + wiring + smoke test (#114)

**Agent:** mizan-mcp-dev-2 · **Date:** 2026-10-07 · **Branch:** `feat/mizan-mcp-114`
**Commit:** `140bf49` (on top of Phase A HEAD `e91fe71`)

## What was built

The cmd-layer transport wrapper over the Phase A `internal/mcpserver` core. The
`internal/mcpserver` package contract was NOT changed (its `Deps`, `NewServer`,
and `EngineFor` factory are used exactly as finalized by Phase A).

### Files added
- `cmd/mizan/mcp.go` — `newMcpCmd()` parent cobra group (`mizan mcp`) +
  `buildMcpServer(ctx)`: loads config via `mustConfig()`, requires a project
  (mirrors `openEngine`), opens `wire.OpenService` / `wire.OpenResultService` /
  `wire.NewEngine` ONCE, and assembles `mcpserver.Deps`. `EngineFor` copies the
  config (detaches `Sources`, applies `applyProjectOverride` for project, sets
  `Location` directly) so the server default is untouched; the staging bucket
  stays fixed at server start. Returns a cleanup that closes every opened service
  in reverse order (with rollback if a later open fails).
- `cmd/mizan/mcp_stdio.go` — `mizan mcp stdio`: `srv.Run(ctx, &mcp.StdioTransport{})`.
  No auth, no network flags.
- `cmd/mizan/mcp_http.go` — `mizan mcp http`: `mcp.NewStreamableHTTPHandler(getServer,
  &mcp.StreamableHTTPOptions{Stateless:true})` mounted on an `http.Server`
  (`/` = MCP, `/healthz` = liveness) with a `--port` flag (default 8080) and
  graceful shutdown on SIGINT/SIGTERM or context cancel.
- `cmd/mizan/mcp_auth.go` — bearer HS256 JWT ingress gate (`bearerAuth.requireBearer`),
  ported from the template's `RequireBearer`/`GenerateAccessToken`. Signing key
  from `JWT_SIGNING_KEY` (dev-key fallback with a warning). The full OAuth 2.1 /
  DCR / PKCE authorization-server surface from the template was intentionally NOT
  ported — #114 asks for a bearer gate + `DISABLE_AUTH` toggle, not a token server.
- `cmd/mizan/root.go` (modified) — new `groupMCP` group; `newMcpCmd()` registered
  in the `newRootCmd` AddCommand list.
- `go.mod` (modified) — `github.com/golang-jwt/jwt/v5 v5.3.1` promoted to a direct
  require (already present transitively via the go-sdk; `go.sum` unchanged).

### Tests added (no network / no ADC)
- `cmd/mizan/mcp_smoke_test.go` — in-process SDK transport smoke test: builds the
  server via `mcpserver.NewServer` with FAKE deps (fake `evaltest.FakeEvaluationClient`
  + temp-file SQLite registry/results), connects the SERVER first over
  `mcp.NewInMemoryTransports()`, then a `mcp.NewClient`, and exercises a READ tool
  (`mizan_list_metrics` + `ListTools`) and an EVAL tool (`mizan_eval_run`)
  end-to-end through the transport, asserting the re-marshalled `StructuredContent`.
- `cmd/mizan/mcp_auth_test.go` — bearer middleware unit tests against an
  `httptest.Server`: valid locally-signed HS256 token passes; missing / malformed /
  wrong-key / expired tokens all 401 (with a `WWW-Authenticate` challenge); the
  `DISABLE_AUTH` bypass (no middleware) passes; env-key vs dev-key selection.
- `cmd/mizan/mcp_cmd_test.go` — command wiring: `mizan mcp` is on the root and
  exposes exactly `stdio` + `http`; `http` has `--port` (default 8080); `stdio`
  has no `--port` (disjoint flag sets).

## How stdio / http / auth are wired
Both transports build the SAME server via `mcpserver.NewServer(*deps)` from one
`buildMcpServer` call (one tool registration). stdio runs it over the SDK stdio
transport with no auth. http mounts it as a Stateless streamable-HTTP handler; by
default it is wrapped in `bearerAuth.requireBearer` (HS256, key from
`JWT_SIGNING_KEY`), and `DISABLE_AUTH=true` mounts the handler without the gate
(with a stderr warning). Config (project/location/staging bucket) is read from the
process env via `config.LoadConfig()` by both transports; project/location are
additionally overridable per call via `Deps.EngineFor`.

## Verification (gates run)
- `go build ./...` — PASS.
- `go vet ./cmd/... ./internal/mcpserver/...` — PASS (clean).
- `go test ./...` — PASS. Summary:
  ```
  ok  github.com/ghchinoy/mizan/cmd/mizan            67.994s
  ok  github.com/ghchinoy/mizan/internal/mcpserver   0.539s
  ... (all other internal packages ok)
  ```
- `go run ./cmd/mizan mcp --help` — lists both `stdio` and `http` sub-subcommands.
- Containment: `grep` confirms the go-sdk `mcp` package is imported only by
  `internal/mcpserver/server.go`, `cmd/mizan/mcp_stdio.go`, `cmd/mizan/mcp_http.go`
  (non-test) plus the smoke test; `mcp_auth.go` uses only `net/http` + jwt.

## Residual notes
- Auth is a focused bearer-JWT gate, not the template's full OAuth2.1/DCR/PKCE
  authorization server (out of scope for #114). The `bearerAuth.generateToken`
  helper exists for local token minting / tests; no CLI `token` subcommand was
  added (can be a follow-up if the owner wants one).
- SSE handler was NOT added (streamable HTTP is the required transport; SSE was
  optional). Easy to add later via `mcp.NewSSEHandler` if parity is desired.
- `applyProjectOverride` governs only the project; the per-call location override
  is applied by setting `cfg.Location` directly in `EngineFor` (the brief's
  pseudocode assumed a 2-arg variant; the real signature is project-only).

# `mizan mcp` — serve the eval engine over the Model Context Protocol

`mizan mcp` exposes the mizan eval engine to MCP clients as a thin transport
wrapper. It registers four tools — `mizan_list_metrics`, `mizan_get_metric`,
`mizan_eval_run`, and `mizan_eval_pairwise` — each mapping 1:1 onto an internal
method the CLI already calls. No new eval logic lives in the MCP layer.

Both transports read the SAME configuration from the process environment
(project / location / staging bucket). `project` and `location` are additionally
overridable per call; the staging bucket is fixed at server start.

## Transports

### `mizan mcp stdio`

Serves over the stdio transport for a local client that spawns `mizan` as a child
process. stdio is a local pipe (the caller is the local user, the CLI trust
model), so it carries **no authentication** and **allows local `file:` inputs**.

### `mizan mcp http`

Serves over the streamable-HTTP transport. Intended for networked callers, so its
defaults are hardened:

| Concern | Default | Override |
|---------|---------|----------|
| Authentication | ON, **fail-closed** | `DISABLE_AUTH=true` (local dev only) |
| Signing key | `JWT_SIGNING_KEY` **required, ≥32 bytes** | — |
| Bind address | `127.0.0.1` (loopback) | `--address 0.0.0.0` (all interfaces) |
| Local `file:` inputs | **disabled** | `--allow-local-files` (unsafe) |
| Token expiry | **required** (`exp` claim) | — |
| Origin check | ON (cross-origin protection) | — |

- **Fail-closed auth.** With auth enabled and no valid `JWT_SIGNING_KEY`, the
  command refuses to start. There is no shipped/default signing key. The bearer
  token is read from the `Authorization: Bearer <jwt>` header only (never a URL
  query parameter). `DISABLE_AUTH=true` is the only no-auth path and is for
  loopback-bound local development.
- **`file:` is a local-only affordance.** Over HTTP a caller may be remote and
  untrusted; a local `file:` input would let it read any file the server process
  can access. Use `gcs:` or `text:` instead. `--allow-local-files` re-enables
  `file:` and is **unsafe for untrusted callers**.
- **Loopback by default.** `--address 0.0.0.0` exposes the server on all
  interfaces and requires a real `JWT_SIGNING_KEY`.

## Security — least-privilege service account (confused-deputy)

A caller may set `project`/`location` per call and pass `gs://` URIs. These run
under the **server's** ADC / service-account identity, not the caller's. This is
intentional (matches the CLI behavior the engine already implements), but over a
network it is a confused-deputy exposure: a caller can steer the server identity
at other projects or GCS objects the server can reach.

**When exposing `mizan mcp http`, its service account MUST be least-privilege,
scoped to exactly the intended project(s) and bucket(s).** Treat the server
identity as the trust boundary.

### Optional project allowlist

Set `MIZAN_MCP_ALLOWED_PROJECTS` to a comma-separated list of project IDs to
reject per-call `project` overrides outside that list with a tool error. An empty
or unset value preserves the default behavior (any project the server identity can
reach). There is no bucket allowlist; restrict `gs://` access through the service
account's IAM instead.

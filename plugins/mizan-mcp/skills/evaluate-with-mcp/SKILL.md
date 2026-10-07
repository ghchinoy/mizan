---
name: evaluate-with-mcp
description: Run an agent-in-the-loop Mizan evaluation over the Model Context Protocol (MCP) — discover a judge (mizan_list_metrics -> mizan_get_metric), run it (mizan_eval_run / mizan_eval_pairwise), and read the persisted runId returned INLINE in the tool result. Use when an agent has the mizan MCP server connected (via the mizan-mcp plugin's stdio registration) and should score or A/B-compare a response/asset against a metric using MCP tools rather than shelling out to the `mizan` CLI.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest); the plugin launches it as `mizan mcp stdio`. The two read-only tools (mizan_list_metrics, mizan_get_metric) need no credentials. A live eval (mizan_eval_run / mizan_eval_pairwise) calls Vertex AI and needs Google Application Default Credentials (ADC) plus a project/location, and a staging bucket when using file:/local inputs. This skill never takes or stores credentials — it relies on the server's existing ADC exactly as the CLI does.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Evaluate with Mizan over MCP (`evaluate-with-mcp`)

Score one response against a metric (**pointwise**), or compare two responses and
pick the better (**pairwise**), by calling the Mizan **MCP tools** the `mizan-mcp`
plugin registers — not by shelling out to the CLI. The plugin registers a local
**stdio** MCP server (`mizan mcp stdio`, the default transport); your MCP client
spawns it and the four tools below become callable.

The server is a thin transport wrapper over the same eval engine the CLI uses; it
adds no new eval logic. Four tools are exposed:

| Tool | Read-only | Purpose |
| --- | --- | --- |
| `mizan_list_metrics` | yes | List available metrics (judges), optionally filtered. |
| `mizan_get_metric` | yes | Full schema of one metric, incl. its declared input fields. |
| `mizan_eval_run` | no | Run a metric against named fields; returns score/explanation + `runId`. |
| `mizan_eval_pairwise` | no | Compare a baseline vs a candidate; returns the winner + `runId`. |

## When to use this skill

- "Evaluate / score / grade this response against metric X" (over MCP).
- "Compare response A vs response B — which is better and why?"
- "Find a judge for <modality/criterion> and run it."
- Any agent-in-the-loop flow where the Mizan MCP server is connected.

## Prerequisites (check first)

1. **The mizan MCP server is reachable.** The `mizan-mcp` plugin registers it via
   `.mcp.json` as a stdio server (`command: mizan`, `args: ["mcp", "stdio"]`), so the
   `mizan` binary must be on the PATH of the process your MCP client launches
   (`go install github.com/ghchinoy/mizan/cmd/mizan@latest`). If tool calls fail to
   connect, confirm `mizan` resolves on PATH and that `mizan mcp stdio` starts.
2. **Credentials — only for the eval tools.** `mizan_list_metrics` and
   `mizan_get_metric` read the local registry and need **no** ADC. A live
   `mizan_eval_run` / `mizan_eval_pairwise` calls Vertex AI and needs ADC
   (`gcloud auth application-default login`) plus a project/location configured on
   the **server** process (`MIZAN_PROJECT_ID`/`PROJECT_ID`, `MIZAN_LOCATION`). Do
   **not** collect or store keys — rely on the server's existing ADC. If an eval
   returns a credentials/project error, surface it verbatim and ask the operator to
   configure ADC/project on the server; never attempt to supply credentials yourself.
3. **A staging bucket — only for `file:` inputs.** If you pass a local `file` field,
   the engine stages it to GCS and needs a bucket (`MIZAN_STAGING_BUCKET` or
   `GENMEDIA_BUCKET`) set on the server. Prefer `gcs:` or `text:` values to avoid
   this (see portability note below).

## Step 1 — Discover a judge

List metrics, then fetch the one you want to learn its exact input fields:

```jsonc
// tool: mizan_list_metrics   (all args optional)
{ "modalities": ["text"], "kinds": ["score"], "tags": ["brand"], "namespace": "demo" }
// -> { "metrics": [ { "id": "demo/clarity", "kind": "score", "name": "Clarity", ... }, ... ] }
```

```jsonc
// tool: mizan_get_metric
{ "id": "demo/clarity" }
// -> { "id", "kind", "modalities", "inputs": [ { "name", "modality", "required" } ],
//      "baselineFieldName", "candidateFieldName", "choices", "autoraterModel", ... }
```

Use `mizan_get_metric` to read the template's **`inputs`** (the placeholder names you
must fill) and, for a pairwise metric, its **`baselineFieldName` / `candidateFieldName`**.
A pairwise metric is one that sets both of those; a pointwise/score/rubric metric does not.

## Step 2 — Build field values

Every instance field is an object that sets **exactly one** of `text`, `file`, or `gcs`:

- `{ "text": "..." }` — a literal text value.
- `{ "gcs": "gs://bucket/obj" }` — a pre-staged media asset (portable across transports).
- `{ "file": "/path/to/asset" }` — a **local** file; the engine stages it to GCS
  (needs a staging bucket). **Local `file:` inputs are allowed only on the stdio
  transport.** On the HTTP transport they are **disabled by default** (the caller may
  be remote/untrusted), so for portability across transports **prefer `gcs:` or
  `text:`** field values. A `file:` value sent to an HTTP server returns a tool error.

Keys in `fields` must match the template's placeholder names from Step 1. Do not route
a `gs://` URI or a media path through a `text` value — media must travel via `gcs`/`file`.

## Step 3 — Run the eval

Pointwise:

```jsonc
// tool: mizan_eval_run
{
  "metric": "demo/clarity",
  "fields": { "response": { "text": "The quarterly report is attached." } },
  "model": "",          // optional autorater override (highest precedence)
  "store": true          // default true: persist the run and return its runId
}
// -> { "score": 4, "passed": null, "confidence": null, "explanation": "...",
//      "warnings": [ ... ], "runId": "01J..." }
```

Pairwise (baseline/candidate are folded into the template's baseline/candidate fields):

```jsonc
// tool: mizan_eval_pairwise
{
  "metric": "demo/better-answer",
  "baseline":  { "text": "Answer A ..." },
  "candidate": { "text": "Answer B ..." },
  "fields": {},          // any extra placeholders the prompt references
  "store": true
}
// -> { "pairwiseChoice": "CANDIDATE", "explanation": "...", "warnings": [ ... ], "runId": "01J..." }
```

Per-call `project` / `location` overrides are accepted (subject to the server's
`MIZAN_MCP_ALLOWED_PROJECTS` allowlist, if set). Omit them to use the server's default.

## Step 4 — Read the result (the runId is INLINE)

Unlike the CLI (where you fetch the RunID from `results list` afterward), the MCP
tools return the persisted **`runId` directly in the tool result**. Read it from the
result — no follow-up call is needed. Surface to the user: the metric, the `score`
(or `pairwiseChoice`), the `explanation`, any `warnings`, and the `runId` for follow-up.

## Edge cases to handle

- **"result not persisted" warning with an empty `runId`.** Persistence is best-effort
  and **non-fatal**: if the server's results store is unconfigured (or a store write
  fails), the eval still succeeds but `runId` comes back empty and `warnings` contains
  a `result not persisted: ...` note. Treat this as a **warning, not a failure** — use
  the returned score/choice/explanation, and tell the user the run was not stored (and
  why) so they can configure a results store if they want durable history.
- **Local `file:` inputs are disabled on the HTTP transport.** Prefer `gcs:` or `text:`
  values so the same call works on both stdio and HTTP. If you must use a local file,
  it will only work when the server is the stdio transport this plugin registers.
- **Eval tools need ADC + a project.** A missing/misconfigured project or credentials
  makes `mizan_eval_run` / `mizan_eval_pairwise` return a tool error (the read-only
  tools are unaffected). Surface the message verbatim and ask the operator to fix the
  server's ADC/project; never supply credentials from the agent side.
- **Wrong metric kind.** Calling `mizan_eval_pairwise` on a non-pairwise metric (one
  that doesn't set baseline/candidate field names) returns a tool error; pick a
  `compare`/pairwise metric, or use `mizan_eval_run` for pointwise/score/rubric.

---

## Appendix — Hardened HTTP deployment (advanced; default stays stdio)

The default and recommended setup is **stdio** (what this plugin registers): a local
process pipe with CLI trust and no network exposure. Only deploy the **HTTP**
transport (`mizan mcp http`) when you deliberately need a remote/shared server, and
harden it:

- **Auth is ON and fail-closed by default.** HTTP uses HS256 bearer-JWT auth. Set a
  real signing key: `JWT_SIGNING_KEY` **must be >= 32 bytes**. (The `DISABLE_AUTH=true`
  escape hatch opens the server with no auth — use only for isolated local testing,
  never in a shared/remote deployment.)
- **Bind narrowly.** It binds `127.0.0.1` by default (`--address`, `--port` default
  8080). Set `--address 0.0.0.0` **only when you intend** to accept non-loopback
  traffic, and put it behind TLS/an authenticating proxy.
- **Keep local files off.** `--allow-local-files` is **false** by default on HTTP;
  leave it off so remote callers cannot read server-local paths. Use `gcs:`/`text:`.
- **Constrain per-call project overrides.** Set `MIZAN_MCP_ALLOWED_PROJECTS` (CSV) to
  an allowlist so callers cannot redirect runs to arbitrary projects.
- **Confused-deputy caveat.** A per-call `project` / `gs://` input runs under the
  **server's** service account, not the caller's identity. Combined with the
  allowlist above, treat every authenticated caller as able to act as the server's SA
  within the allowed projects/buckets, and scope that SA's IAM to the minimum needed.

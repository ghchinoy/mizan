---
name: configure-mizan
description: Configure Mizan for first use — inspect the resolved configuration with `mizan config show -o json`, persist GCP project/location, default autorater model, and results-store settings with `mizan config set`, confirm the build with `mizan version`, and check (never store) Application Default Credentials. Use when a user is setting up Mizan, hits a "project ID not set"/config error, or asks to verify their environment before running live evaluations.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest). Reads and writes only the local Mizan config file (<UserConfigDir>/mizan/.env) and reads the resolved config over -o json — no network. This skill CHECKS for Application Default Credentials but NEVER takes, prints, or stores a credential; live evals use the user's existing ADC exactly as the CLI does.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Configure Mizan for first use (`configure-mizan`)

Get a Mizan environment ready to run evaluations: read the **resolved**
configuration, set the handful of values a live eval needs (GCP project and
location, an optional default autorater model, and the results-store settings),
confirm the installed build, and verify that Application Default Credentials
(ADC) are present. This skill drives the local `mizan` CLI over its
machine-readable `-o json` output and its `config set` writer; it **starts no
server**, makes **no network request**, and **re-implements no CLI logic**.

> **Credentials (N5).** This skill **checks** whether ADC exist; it **never
> takes, prints, or stores a credential**. `mizan` uses the user's existing ADC
> exactly as the CLI does — `config set` persists only non-secret settings (project
> id, region, model name, store paths) to the local config file. Do not paste,
> capture, or write a token, key file contents, or password anywhere.

## When to use this skill

- "Set up / configure Mizan for me."
- "`mizan eval run` says *project ID not set* — fix my config."
- "What project / region / model will Mizan use?"
- "Point Mizan's results store somewhere else."
- "Check that my Mizan install and credentials are ready for a live eval."

## Prerequisites (check first)

1. **`mizan` is installed.** Run the precheck and stop with an install hint if it fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```

## Step 1 — confirm the build (`mizan version`)

```bash
mizan version              # text: "mizan <version> (commit <commit>, built <date>)"
mizan version -o json      # structured version.Info
```

`-o json` prints the `version.Info` object (three fields):

<!-- drift:version.Info -->
```json
{
  "version": "v0.1.0",
  "commit": "abcdef0",
  "date": "2026-09-16"
}
```

- **`version`** — the build's semantic version (`dev` for an unstamped local
  build). **`commit`** — the git commit. **`date`** — the build date. Report these
  so an install can be pinned/upgraded when needed. There is no `--format`; the
  switch is `-o json`.

## Step 2 — read the resolved configuration (`mizan config show -o json`)

`config show` (alias: `config list`) prints the **resolved** configuration — the
effective value of every setting **and where each one came from** (a real
exported env var, the loaded env file, or a built-in default). Read the JSON:

```bash
mizan config show -o json          # resolved config as a single JSON object
mizan config list -o json          # `list` is an alias for `show`
mizan config show                  # human table: KEY / VALUE / SOURCE
```

Reason over the JSON to decide what still needs setting — in particular an empty
`ProjectID` blocks live evals. The `Sources` map (keyed by the `config set` key)
tells you whether a value is a real setting or just the built-in default.

<!-- drift:config.Config -->
```json
{
  "ProjectID": "my-project",
  "Location": "us-central1",
  "StagingBucket": "my-bucket",
  "APIEndpoint": "",
  "RegistryDBPath": "/home/you/.config/mizan/registry.db",
  "ResultsBackend": "sqlite",
  "ResultsDBPath": "/home/you/.config/mizan/results.db",
  "ResultsRetention": "hybrid",
  "PackCacheDir": "/home/you/.cache/mizan/packs",
  "DefaultTemplatesRepo": "github.com/ghchinoy/mizan-templates",
  "DefaultModel": "gemini-2.5-pro",
  "AuthorName": "Alice Example",
  "DefaultLicense": "Apache-2.0",
  "Sources": {
    "project-id": "env",
    "location": "default"
  }
}
```

Field semantics (from `internal/config/config.go`; top-level keys are Go's
default capitalized names — read `ProjectID`, `Location`, etc. exactly as shown):

- **`ProjectID`** — GCP project for eval (empty blocks live evals). **`Location`**
  — eval-service region (default `us-central1`). **`StagingBucket`** — `gs://`
  prefix for multimodal inputs (only needed for media evals). **`APIEndpoint`** —
  optional Vertex endpoint override.
- **`RegistryDBPath`** / **`ResultsDBPath`** — local SQLite paths for the template
  registry and the results store. **`ResultsBackend`** — results store backend
  (default `sqlite`). **`ResultsRetention`** — input retention policy
  (`inline`|`reference`|`hybrid`, default `hybrid`). **`PackCacheDir`** — cache dir
  for `registry import <git-url>`.
- **`DefaultTemplatesRepo`** — default source for `registry import` with no
  `<src>`. **`DefaultModel`** — default autorater model; empty means the built-in
  default is used at eval time. **`AuthorName`** / **`DefaultLicense`** — authoring
  conveniences `registry create` falls back to.
- **`Sources`** — a map from each `config set` key to where its resolved value
  came from (`env`, `env-file`, or `default`). Its keys are the `config set` keys
  (see Step 3), not part of a fixed schema — treat the map itself as opaque data
  (it is *omitempty*: absent only when nothing has been resolved).

## Step 3 — set what is missing (`mizan config set <key> <value>`)

`config set` persists a value to `<UserConfigDir>/mizan/.env` (mode `0600`) — the
same trusted file `mizan` reads; it never writes the current working directory.
Use the friendly **keys** (the same keys shown in `Sources`), not the env-var
names. The full valid key set (run `mizan config set` with a bad key to see it):

| key | sets | typical value |
|---|---|---|
| `project-id` | GCP project (**required for live eval**) | `my-gcp-project` |
| `location` | eval-service region | `us-central1` |
| `default-model` | default autorater model | `gemini-2.5-pro` |
| `staging-bucket` | `gs://` prefix for multimodal inputs | `my-bucket` |
| `api-endpoint` | Vertex endpoint override | *(usually unset)* |
| `results-backend` | results store backend | `sqlite` |
| `results-db` | results store path | `/path/results.db` |
| `results-retention` | input retention (`inline`\|`reference`\|`hybrid`) | `hybrid` |
| `registry-db` | template registry path | `/path/registry.db` |
| `pack-cache` | import cache dir | `/path/cache` |
| `templates-repo` | default `registry import` source | `github.com/ghchinoy/mizan-templates` |
| `author-name` | default template author | `Alice Example` |
| `default-license` | default template license | `Apache-2.0` |

First-run essentials:

```bash
mizan config set project-id my-gcp-project      # required for live eval
mizan config set location us-central1           # region
mizan config set default-model gemini-2.5-pro   # optional; else built-in default
```

Then re-run `mizan config show -o json` and confirm the `Sources` for the keys you
set now read `env-file` (not `default`).

## Step 4 — check Application Default Credentials (check only — never store)

Live evals call Vertex AI and need ADC. **Verify presence only; do not capture,
print, or store any credential.**

```bash
# Non-secret presence checks (no token is captured):
[ -n "${GOOGLE_APPLICATION_CREDENTIALS:-}" ] && echo "ADC via GOOGLE_APPLICATION_CREDENTIALS"
[ -f "$HOME/.config/gcloud/application_default_credentials.json" ] && echo "ADC file present"

# Optional liveness check — discard the token; never save it:
gcloud auth application-default print-access-token >/dev/null 2>&1 \
  && echo "ADC valid" \
  || echo "ADC missing/expired — run: gcloud auth application-default login"
```

If ADC are missing, tell the user to run
`gcloud auth application-default login` themselves. This skill never runs a login
that stores keys on their behalf beyond that standard, user-owned flow, and never
records the token.

## Reporting back to the user

Summarize: the installed `mizan version`; the resolved `ProjectID`/`Location`/
`DefaultModel` and whether each is a real setting or a default (from `Sources`);
which keys you set and the config-file path; the results-store settings; and the
ADC check result (present / missing, with the exact `gcloud` command to fix it).
Note that live evals still require the user's own ADC and that no credential was
taken or stored. Surface any CLI stderr verbatim with the concrete fix.

## Out of scope / deferred (do not use as if shipping)

- No `mizan mcp` server exists; this is CLI configuration only.
- `config set` writes **only non-secret** settings; it is not a credential store.

# Mizan User Guide

This guide covers what Mizan can actually do today: manage a local metric
registry, configure credentials/project settings, and run a text pointwise
evaluation against the live Vertex AI Gen AI Evaluation Service. Every command
and output shown below was run against the built CLI; where a capability isn't
implemented yet, this guide says so explicitly rather than implying it works.

For the underlying design, see [`architecture-final.md`](architecture-final.md).
For phase-by-phase roadmap detail, see
[`implementation-plan.md`](implementation-plan.md).

## Prerequisites

- **Go 1.26+** — only required if you're building from source. `go install`
  (below) handles the toolchain for you.
- **A GCP project with the Vertex AI API enabled.**
- **Application Default Credentials (ADC)** configured, e.g.:

  ```sh
  gcloud auth application-default login
  ```

  (or a service account / workload identity if running in a deployed
  context — there is no API-key auth path.)
- **IAM permission to call Vertex AI** — the `Vertex AI User` role (or
  equivalent) on the project you configure.

## Install

```sh
go install github.com/ghchinoy/mizan/cmd/mizan@main
```

Mizan uses the pure-Go `modernc.org/sqlite` driver for its local registry
store, so this install is **cgo-free** — no C toolchain required, even though
the registry is backed by SQLite.

## Configure

Set your project:

```sh
mizan config set project-id <your-project-id>
```

Optionally set the region (default is `us-central1`):

```sh
mizan config set location us-central1
```

> The multi-region value `us` is **not** valid for the native eval path and
> will 404 — always use a concrete region such as `us-central1`.

Check the resolved configuration:

```sh
$ mizan config show
ProjectID:             my-project
Location:              us-central1
StagingBucket:         (unset)
APIEndpoint:           (unset)
RegistryDBPath:        /home/you/.config/mizan/registry.db
PackCacheDir:          /home/you/.cache/mizan/packs
DefaultTemplatesRepo:  github.com/ghchinoy/mizan-templates
```

`config show` works fine with no project ID set (it prints `(unset)`) — only
`eval run` hard-requires a project ID. `registry` and `config` commands work
without one.

`mizan config set` persists values to `<UserConfigDir>/mizan/.env` (e.g.
`~/.config/mizan/.env` on Linux), created with restrictive permissions
(`chmod 0600` on the file, `0700` on the directory). Valid keys: `api-endpoint`,
`location`, `pack-cache`, `project-id`, `registry-db`, `staging-bucket`,
`templates-repo`.

You can also configure via environment variables instead of (or in addition
to) the persisted file — real environment variables always win over the
`.env` file:

| Config value | Env var(s) |
|---|---|
| Project ID | `MIZAN_PROJECT_ID` or `PROJECT_ID` |
| Location | `MIZAN_LOCATION` |
| Staging bucket | `MIZAN_STAGING_BUCKET` |
| API endpoint | `MIZAN_API_ENDPOINT` |
| Registry DB path | `MIZAN_REGISTRY_DB` |
| Pack cache dir | `MIZAN_PACK_CACHE` |
| Templates repo | `MIZAN_TEMPLATES_REPO` |

You can also point Mizan at an explicit env file with `MIZAN_ENV_FILE`.

**Security note:** Mizan refuses a custom `--api-endpoint` / `MIZAN_API_ENDPOINT`
whose host isn't `*.googleapis.com`, because the Vertex client attaches your
ADC bearer token to every request — an arbitrary endpoint could exfiltrate it.
If you genuinely need a non-Google endpoint (e.g. a test proxy), set
`MIZAN_ALLOW_CUSTOM_ENDPOINT=1` to override.

## Registry walkthrough

The registry holds your metric templates locally (SQLite file at
`RegistryDBPath`). Every template has a stable id of the form
`<namespace>/<slug>` (e.g. `demo/conciseness`) — you choose both parts.

### Create

```sh
$ mizan registry create --id demo/conciseness --name "Conciseness" \
    --description "Scores how concise a response is" \
    --kind pointwise \
    --prompt "Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}" \
    --model gemini-2.5-flash
ID:             demo/conciseness
Name:           Conciseness
Kind:           pointwise
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Description:    Scores how concise a response is
Prompt:         Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}
```

Prompt templates use double-brace `{{var}}` placeholders. Every placeholder in
the prompt must be supplied as a `--field` when you run the metric (see
below), or the run fails before it ever calls the API.

`--kind` accepts `pointwise`, `pairwise`, `rubric`, or `custom_schema`, but
**only `pointwise` can currently be run** with `eval run` (see
[Running an evaluation](#running-a-text-pointwise-evaluation-end-to-end)
below and [Coming soon](#coming-soon--roadmap)). You can create templates of
the other kinds today, but there's no way to execute them yet — the `--kind`
value is only validated structurally at creation time.

Other useful create flags: `--system` (system instruction), `--sampling-count`
(autorater sampling count, default 4), `--modality` (repeatable; default
`text`), `--tag` (repeatable).

### List

```sh
$ mizan registry list
ID                NAME         KIND       MODEL
demo/conciseness  Conciseness  pointwise  gemini-2.5-flash
```

Filter with `--namespace <prefix>` or `--kind <kind>`.

### Get

```sh
$ mizan registry get demo/conciseness
ID:             demo/conciseness
Name:           Conciseness
Kind:           pointwise
Modalities:     text
Model:          gemini-2.5-flash
SamplingCount:  4
Description:    Scores how concise a response is
Prompt:         Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}
```

### Update

Only the flags you pass are changed; everything else is left as-is:

```sh
$ mizan registry update demo/conciseness --description "Updated description"
```

(Verified: updating just `--description` leaves the prompt, model, and every
other field untouched.)

### Delete

```sh
$ mizan registry delete demo/conciseness
deleted demo/conciseness
```

### Output format

Every registry (and config) command supports `-o/--output json|table`
(default `table`) for scripting.

## Running a text-pointwise evaluation end-to-end

Create the metric (as above), then run it:

```sh
$ mizan eval run --metric demo/conciseness --field response="The cat sat on the mat."
Score:        1
Explanation:  The response 'The cat sat on the mat.' is a very short, direct, and grammatically complete sentence that conveys its meaning with no superfluous words, making it maximally concise.
```

This is a **live call** to Vertex AI's `EvaluateInstances` API in the
configured region (`us-central1` by default). Every `--field key=value` is
treated as plain text — there is currently no way to pass a file, image, or
other asset via the CLI (multimodal isn't wired up at the CLI level at all in
this slice, independent of the engine-level GCS-staging gap noted in the
roadmap).

### Interpreting the result

- **`Score`** is a float. Its range and meaning are whatever your prompt's
  rubric implies — Mizan does not impose a fixed scale (in the example above,
  the prompt asked for 0–1, so 1 means "very concise"). Read your own prompt
  template to know how to interpret the number it produces.
- **`Explanation`** is free-text rationale generated by the autorater model,
  not a fixed-format field — treat it as a qualitative aid, not something to
  parse programmatically.

## Troubleshooting

**`Error: config: project ID not set (set MIZAN_PROJECT_ID or PROJECT_ID)`**
`eval run` requires a project id. Run `mizan config set project-id <id>` or
export `MIZAN_PROJECT_ID`/`PROJECT_ID`. (Registry and `config show` work
without one.)

**`Error: eval: instance is missing values for template variables [...]`**
Your prompt template has a `{{var}}` placeholder with no matching `--field
var=value` on the command line. Add the missing `--field`.

**`Error: eval: not implemented in P1 slice: pairwise (WI-P1-4)`** (or
`rubric`/`custom_schema`) You created a template with a `--kind` other than
`pointwise` and tried to run it. This is expected — those kinds are
creatable but not yet runnable. See [Coming soon](#coming-soon--roadmap).

**`Error: registry: template not found`**
The `<id>` you passed to `get`/`update`/`delete`/`eval run --metric` doesn't
exist in the registry. Check `mizan registry list`.

**`Error: config: refusing custom API endpoint "...": host is not
*.googleapis.com (set MIZAN_ALLOW_CUSTOM_ENDPOINT=1 to override)`**
You set `--api-endpoint`/`MIZAN_API_ENDPOINT` to a non-Google host. This is a
deliberate safeguard against exfiltrating your ADC token to an untrusted
endpoint. If you really need a custom endpoint (e.g. a local test proxy), set
`MIZAN_ALLOW_CUSTOM_ENDPOINT=1`.

## Coming soon / roadmap

These are **not implemented** in the current build — don't expect them to
work, and treat any resemblance in `--help` output (e.g. accepted `--kind`
values) as forward-looking scaffolding, not a working feature:

- **Multimodal evaluation** (image/audio/video/music) — the native
  `EvaluateInstances` path requires staging non-text assets to GCS
  (`gs://` URIs) first; that staging step (`internal/asset` MIME detection +
  upload) isn't wired into the CLI or engine yet. A separate `genai`-based
  custom-schema fallback that could accept inline bytes also isn't wired in.
- **Pairwise evaluation** (`eval pairwise`, flip-bias mitigation) — roadmap
  work item WI-P1-4.
- **Rubric-based metrics** — roadmap work item WI-P1-5.
- **`custom_schema` metric execution** via the `genai` `GenerateContent`
  fallback — roadmap work item WI-P1-5.
- **Template packs and sharing** (`mizan pack init|validate|add`, `registry
  import|export`) — no `pack` command exists in the built binary yet
  (roadmap phase P2, the collaboration layer). The companion
  [`github.com/ghchinoy/mizan-templates`](https://github.com/ghchinoy/mizan-templates)
  repo is scaffolded for this but nothing in this repo talks to it yet.
- **Batch evaluation** (`EvaluateDataset` over GCS-hosted datasets) —
  roadmap phase P3.
- **The Wails desktop app** (`cmd/mizan-desktop`) — design-stage scaffolding
  only (`internal/app/app.go`, `cmd/mizan-desktop/main.go`); no built or
  runnable desktop app exists. Roadmap phase P4.

See [`implementation-plan.md`](implementation-plan.md) for phase-by-phase
detail and acceptance criteria.

## How it fits together

For a text-pointwise `eval run`, the request flows entirely through
implemented code paths — see the sequence below (and
[`architecture-final.md`](architecture-final.md) §6 for the domain model):

![Sequence diagram: mizan eval run flows from the CLI through config, registry.Service, and eval.Engine to a live Vertex AI EvaluateInstances call and back](diagrams/eval-sequence.webp)

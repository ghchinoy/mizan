# Mizan User Guide

This guide covers what Mizan can actually do today: manage a local metric
registry, configure credentials/project settings, and run evaluations —
pointwise, rubric, custom_schema, and pairwise, including multimodal
(image/audio/video/music) assets — against the live Vertex AI Gen AI
Evaluation Service. Phase 1 is complete: all four metric kinds and multimodal
are implemented and CLI-runnable end-to-end. Every command and output shown
below was run against the built CLI; where a capability isn't implemented
yet, this guide says so explicitly rather than implying it works. For
deeper, copy-pasteable recipes for every metric kind (including live error
output for the pairwise placeholder contract and the create-time rubric/
schema validation), see [`docs/testing-guide.md`](testing-guide.md).

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

`--kind` accepts `pointwise`, `pairwise`, `rubric`, or `custom_schema`, and
all four are runnable end-to-end today. Two kinds need extra authoring flags,
and one has an extra structural requirement on its prompt:

- **`pointwise`** — no extra flags needed beyond `--prompt`; see
  [Running an evaluation](#running-a-text-pointwise-evaluation-end-to-end)
  below.
- **`rubric`** — also requires `--rubric-group "name=criterion
  one;criterion two"` (repeatable) or `--rubric-groups-file <path>`; `create`
  rejects the template immediately if neither is given.
- **`custom_schema`** — also requires `--response-schema '<json>'` or
  `--response-schema-file <path>`; `create` rejects the template immediately
  if neither is given.
- **`pairwise`** — also requires `--baseline-field`/`--candidate-field`, and
  the `--prompt` text must reference those field names as `{{name}}`
  placeholders (the run fails otherwise, since the API rejects instance keys
  the template doesn't reference). Use the dedicated `mizan eval pairwise
  --baseline key=value --candidate key=value` command, which makes the
  baseline/candidate roles explicit (generic `eval run --field` also
  technically works, since pairwise fields are ordinary placeholders, but
  `eval pairwise` is the documented, less error-prone path).

See [`docs/testing-guide.md`](testing-guide.md) for full recipes and live
output for every kind.

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
configured region (`us-central1` by default). `--field key=value` is treated
as plain text; for image/audio/video/music assets, use `--file key=/path`
(local file, auto-staged to your configured GCS staging bucket) or `--gcs
key=gs://...` (a pre-staged asset) instead — see
[`docs/testing-guide.md`](testing-guide.md#multimodal) for a full multimodal
walkthrough.

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

**`Error: kind "rubric" requires rubric groups; pass --rubric-group
"name=crit1;crit2" (repeatable) or --rubric-groups-file <path>`**
You ran `registry create --kind rubric` without either rubric-authoring flag.
This is caught immediately at create time (as of PR #11) — add
`--rubric-group "name=criterion one;criterion two"` (repeatable) or
`--rubric-groups-file <path-to-json-object>`. See
[`docs/testing-guide.md`](testing-guide.md#rubric) for a full recipe.

**`Error: kind "custom_schema" requires a response schema; pass
--response-schema '<json>' or --response-schema-file <path>`**
Same as above, for `--kind custom_schema`: add `--response-schema '<json>'`
or `--response-schema-file <path>`. See
[`docs/testing-guide.md`](testing-guide.md#custom-schema) for a full recipe.

**`Error: eval: pairwise template "<id>" metric prompt must reference the
baseline {{<name>}} and candidate {{<name>}} placeholder(s); the API rejects
instance keys not present in the template`**
Your pairwise template's `--prompt` text doesn't contain `{{<baseline-field>}}`
and `{{<candidate-field>}}` placeholders matching the field names you passed
at `create` time via `--baseline-field`/`--candidate-field`. Update the prompt
to reference both. See
[`docs/testing-guide.md`](testing-guide.md#placeholder-contract-real-verified).

**`--flip-enabled=false` doesn't disable flipping**
This is a known P1 limitation, not a bug: the registry's `FlipEnabled` field
is a plain `bool` that can't distinguish an explicit "false" from "unset," so
P1 always runs pairwise with flip enabled regardless of what you pass. See
[`docs/testing-guide.md`](testing-guide.md#flip-enabled-known-p1-limitation).

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

Phase 1 (all four metric kinds + multimodal) is complete. What's **not usable
end-to-end via the CLI** in the current build is the P2/P3/P4 work below —
don't expect these to work:

- **Template packs and sharing** (`mizan pack init|validate|add`, `registry
  import|export`) — no `pack` command and no `registry import|export` exist
  in the built binary yet (roadmap phase P2, the collaboration layer). The
  intended model: packs are contributed via pull requests to the dedicated
  [`github.com/ghchinoy/mizan-templates`](https://github.com/ghchinoy/mizan-templates)
  repo (data + CI only, no Mizan application code); its `validate-packs` CI
  workflow is already wired up and runs on every PR there, but it currently
  fails for the same reason — the validation step invokes `mizan pack
  validate`, which doesn't exist yet — so that gate goes green once the
  command ships. Packs are then pulled in with `mizan registry import`
  (that repo is the configured default source) once the command exists.
  `mizan-templates` is not just a stub — it already holds a real worked
  example pack (`packs/google-brand/`), its own pack-format docs, and an
  active CI gate — but this repo's binary has no code path that talks to it
  yet.
- **Batch evaluation** (`EvaluateDataset` over GCS-hosted datasets) —
  roadmap phase P3.
- **The Wails desktop app** (`cmd/mizan-desktop`) — design-stage scaffolding
  only (`internal/app/app.go`, `cmd/mizan-desktop/main.go`); no built or
  runnable desktop app exists. Roadmap phase P4.

See [`implementation-plan.md`](implementation-plan.md) for phase-by-phase
detail and acceptance criteria.

## How it fits together

For a text-pointwise `eval run`, the request flows entirely through
implemented code paths — see the sequence below. This is one example of a
fully implemented path; rubric, custom_schema, pairwise, and multimodal are
also implemented end-to-end (see
[`architecture-final.md`](architecture-final.md) §6 for the domain model and
the component diagram):

![Sequence diagram: mizan eval run flows from the CLI through config, registry.Service, and eval.Engine to a live Vertex AI EvaluateInstances call and back](diagrams/eval-sequence.webp)

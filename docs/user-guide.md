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

Prompt templates use double-brace `{{var}}` placeholders. The check runs in
**both directions** before any API call, so a mis-authored template or a stray
`--field` fails fast with a clear error instead of silently producing a wrong
score:

- Every placeholder in the prompt must be supplied as a `--field` when you run
  the metric (see below) — a missing value fails the run.
- Conversely, every `--field` you supply must match a placeholder. A `--field`
  whose key matches no `{{placeholder}}` (for example a typo, or an extra key)
  is rejected — otherwise its value would be silently dropped and never reach
  the judge. Likewise, running a template that contains **no** `{{placeholder}}`
  at all while supplying `--field` values is an error: the values cannot reach
  the judge, so Mizan tells you to add a `{{...}}` placeholder (single-brace
  `{var}` is **not** recognized — use `{{var}}`).

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
(autorater sampling count, default 4 — lowering it trades self-consistency for
latency), `--modality` (repeatable; default `text`), `--tag` (repeatable),
`--flip-enabled` (pairwise position-bias mitigation, default `true`; see the
pairwise flip note below).

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

### Choosing the judge model — and global-only judges

The autorater model is resolved per run: `--model` flag > template model >
`default-model` config (`MIZAN_DEFAULT_MODEL`) > built-in `gemini-2.5-flash`.

Some newer judges — the `gemini-3.5` family (`gemini-3.5-flash` /
`-flash-lite`) — are **global-only**: they exist only on Vertex's global eval
host and return `NOT_FOUND` on a regional endpoint. You do **not** need to change
`--location` to use one. When the resolved judge is global-only, Mizan
automatically runs the whole eval call against the global host
(`aiplatform.googleapis.com` / `locations/global`) — either up front for a known
global-only model, or by transparently retrying on the global host after the
regional call reports the autorater is not found.

Because a global-only judge cannot run in your region, this routing is **forced**:
your configured `--location` / `MIZAN_LOCATION` is kept for output labeling but
is not honored as a residency region for that run. Mizan says so on stderr, e.g.:

```
mizan: autorater gemini-3.5-flash is global-only (…); routing this eval to the GLOBAL host (location=global). Your configured --location is kept for labeling only.
```

The pre-flight echo Mizan prints to stderr before each call also shows
`location=global` for a known global-only judge. The built-in default
(`gemini-2.5-flash`) is served on both
regional and global endpoints, so a default run is never re-routed. For the full
detection details see
[`docs/llm-as-judge-scenarios.md`](llm-as-judge-scenarios.md) Scenario 7.

### Interpreting the result

- **`Score`** is a float. Its range and meaning are whatever your prompt's
  rubric implies — Mizan does not impose a fixed scale (in the example above,
  the prompt asked for 0–1, so 1 means "very concise"). Read your own prompt
  template to know how to interpret the number it produces.
- **`Explanation`** is free-text rationale generated by the autorater model,
  not a fixed-format field — treat it as a qualitative aid, not something to
  parse programmatically.

## Per-criterion rubric detail (`--rubric-detail`)

For a `rubric` template, add `--rubric-detail` to `eval run` to get a score and
rationale for **each authored criterion** (plus an overall roll-up), instead of a
single score. Set the Likert scale with `--rubric-scale "<min>-<max>"` (default
`1-5`; non-negative, `min < max`). See
[`docs/llm-as-judge-scenarios.md`](llm-as-judge-scenarios.md) Scenario 4 for the
full walkthrough and output shape.

The judge's returned criteria are **strictly reconciled** against your authored
set, matched by the exact **(group, criterion)** pair:

- a **missing** authored criterion (authored but not returned by the judge) fails
  the run with an `eval:` error naming the missing pair(s) — a partial scorecard
  is never surfaced;
- a **duplicated** authored criterion (the same pair returned more than once)
  fails the run with an `eval:` error naming the duplicated pair(s);
- an **extra** criterion (returned but not authored) is **kept in the output** and
  a warning is printed to **stderr** — extras are informative, not corrupting, so
  they do not fail the run.

## Version and releases

Check which build you're running:

```sh
$ mizan version
mizan v1.2.3 (commit a1b2c3d, built 2026-08-10T00:00:00Z)
```

Like every other command, `version` honors `-o/--output json|table` (default a
plain line) for scripting:

```sh
$ mizan version -o json
{
  "version": "v1.2.3",
  "commit": "a1b2c3d",
  "date": "2026-08-10T00:00:00Z"
}
```

The three values are injected at build time via `-ldflags`. A plain
`go build ./cmd/mizan` (no ldflags) reports the placeholders
`dev`/`none`/`unknown`; `make build` and `make install` populate them from
`git describe --tags --always --dirty`, the short commit, and a UTC timestamp.

### Cutting a release

Releases are tag-driven. Push a `vX.Y.Z` tag and the
[`release` workflow](../.github/workflows/release.yml) cross-compiles CGO-free
binaries (linux/darwin × amd64/arm64), stamps them with the tag via the same
ldflags, and attaches the tarballs plus `.sha256` sums to the GitHub Release:

```sh
git tag v1.2.3
git push origin v1.2.3
```

Once a `vX.Y.Z` tag exists, downstreams (e.g. the `mizan-templates`
`validate-packs` CI) can pin `MIZAN_VERSION=vX.Y.Z` instead of tracking
`@main`.

## Troubleshooting

**`Error: config: project ID not set (set MIZAN_PROJECT_ID or PROJECT_ID)`**
`eval run` requires a project id. Run `mizan config set project-id <id>` or
export `MIZAN_PROJECT_ID`/`PROJECT_ID`. (Registry and `config show` work
without one.)

**`Error: eval: instance is missing values for template variables [...]`**
Your prompt template has a `{{var}}` placeholder with no matching `--field
var=value` on the command line. Add the missing `--field`.

**`Error: eval: unknown instance field(s) [...] for template "<id>"; it
references placeholders [...]`**
The reverse of the above: you supplied a `--field` whose key matches no
`{{placeholder}}` in the template — usually a typo (`--field respons=...` for a
`{{response}}` placeholder) or an extra key. Such a field would be silently
dropped and never reach the judge, so Mizan rejects it. Fix the field name to
match a placeholder, or add the placeholder to the prompt.

**`Error: eval: template "<id>" references no {{placeholders}} but N field(s)
were supplied [...]; the value(s) will NOT reach the judge`**
The template's prompt has no `{{...}}` placeholders at all, yet you passed
`--field` values. Because substitution is placeholder-driven, those values
cannot reach the judge — which previously yielded a confidently-wrong score
against an empty input. Add a `{{...}}` placeholder to the prompt (for example
`Response: {{response}}`), or check that you referenced the right template.
Note that single-brace `{var}` is not recognized — use double braces `{{var}}`.

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

**Pairwise flip and the `Choice` is authoritative**
Pairwise runs with position-bias mitigation ("flip") controlled by the template's
`--flip-enabled` flag at `create` time (**default `true`**). With flip on, the
judge evaluates both position orderings and returns a de-biased, aggregated
`Choice` — **the `Choice` is the authoritative verdict**. The `Explanation`,
however, is a single sampled artifact whose "baseline"/"candidate" wording may
reflect a flipped ordering, so it can read as though it praises the *other*
response. `mizan eval pairwise` prints a one-line warning to stderr when flip is
in effect (and serializes it under `warnings` in `--output json`). If you need
the explanation's wording to match the order you presented, create the template
with `--flip-enabled=false`; the trade-off is losing position-bias mitigation.
Because the P1 registry stores `FlipEnabled` as a plain `bool` (no tri-state),
`false` is only distinguishable from the default at `create` time — set it
explicitly on the template.

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

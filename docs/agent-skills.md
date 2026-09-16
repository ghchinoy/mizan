# Mizan Agent Skills

Mizan ships **agent skills** so people (and their agents) can operate Mizan by
invoking a skill instead of memorizing CLI flags. The skills follow the
[Agent Plugins Specification v1.0.0](https://agent-plugins.org) and the
[Agent Skills specification](https://agentskills.io), the same packaging model as
[`ghchinoy/agent-skills`](https://github.com/ghchinoy/agent-skills).

This is the authoritative documentation for the pattern; it is synced into the
Starlight docs site from `docs/`.

## The pattern

A **plugin** is a directory under `plugins/` with a `plugin.json` manifest and one
or more **skills**. A **skill** is a `SKILL.md` (Markdown with YAML frontmatter)
plus optional `scripts/`, `references/`, and `assets/`. Skills are
**model-invoked**: there is no argument schema — the frontmatter `description`'s
"Use when …" clause is what makes an agent load the skill, and the body gives the
literal CLI commands to run.

```
mizan/
├── .claude-plugin/
│   └── marketplace.json          # discovery index (Claude plugin marketplace)
├── plugins/
│   ├── mizan-eval/
│   │   ├── plugin.json           # Agent Plugins v1.0.0 manifest
│   │   └── skills/
│   │       └── run-eval/
│   │           └── SKILL.md      # the skill
│   └── mizan-results/
│       ├── plugin.json
│       └── skills/
│           └── report-to-html/
│               ├── SKILL.md      # the skill
│               ├── scripts/      # render_report.py (HTML renderer)
│               └── assets/       # report.template.html (neutral template)
├── scripts/
│   └── validate-plugins.sh       # structural + frontmatter conformance gate
└── internal/
    └── skilldocs/
        ├── drift_test.go         # eval.Result -o json contract drift gate
        └── drift_results_test.go # results.Result -o json contract drift gate
```

The plugin manifest (`plugin.json`) uses the closed Agent Plugins schema — its
`$schema` is exactly `https://agent-plugins.org/schemas/1.0.0/plugin.schema.json`
and its `name` must equal the plugin directory name. `SKILL.md` frontmatter is a
closed vocabulary (`name`, `description`, `license`, `compatibility`, `metadata`,
`allowed-tools`); `name` must equal the skill directory name and all `metadata`
values must be strings.

## How a Mizan skill talks to Mizan (the binder model)

Mizan's CLI emits struct-backed JSON on a single persistent switch — `-o json`
(there is no `--format`) — for every consuming command, writing the result object
to **stdout** while pre-flight echo, warnings, and store notices go to **stderr**.
So a Mizan skill:

1. checks `command -v mizan`,
2. runs the relevant `mizan … -o json`,
3. parses stdout and reasons over the struct,
4. relies on the user's existing ADC — it never takes or stores credentials.

This mirrors [`ghchinoy/binder`](https://github.com/ghchinoy/binder): the skill
drives the CLI and reasons over `--json`, and an in-process **drift test** proves
the JSON shapes the skill documents against the live CLI so the skill cannot
silently rot as the CLI moves.

### Keeping skills in sync — two gates

- **`scripts/validate-plugins.sh`** — structural + frontmatter conformance
  (Agent Plugins v1.0.0 + Agent Skills): valid `plugin.json`, `name` == dir,
  closed frontmatter vocabulary, `metadata` string values, executable `scripts/`,
  and every `marketplace.json` path resolving in-tree. Runs in CI and under
  `make check`.
- **`internal/skilldocs/`** — binder-style, **hermetic** drift tests, one per
  documented `-o json` contract. For `run-eval`, `drift_test.go` constructs a
  representative `eval.Result`; for `report-to-html`, `drift_results_test.go`
  constructs a representative `results.Result`. Each renders it through the same
  `encoding/json` path the CLI's `-o json` uses and asserts **key-set equality**
  (path-anchored) against the JSON block documented in the corresponding
  `SKILL.md`, plus a reflection pass proving every declared field — including
  omitempty and slice-element fields — is documented. They make **no network or
  ADC calls**, so they pass in CI without credentials, and they fail the moment a
  field is added, renamed, or retagged on the underlying struct until the skill
  is updated.

Both are aggregated by `make check` (which also runs build/vet/fmt/lint/vuln and
the full `go test ./...`).

## Installing the skills

The primary channel is the **Claude plugin marketplace** (the root
`.claude-plugin/marketplace.json`):

```text
# In Claude Code:
/plugin marketplace add ghchinoy/mizan
/plugin install mizan-eval@mizan
/plugin install mizan-results@mizan
/plugin install mizan-authoring@mizan
/plugin install mizan-setup@mizan
```

The Claude CLI resolves each `skills` entry in `marketplace.json`
**relative to the plugin's `source` directory**, so those paths are written
source-relative (e.g. `./skills/run-eval` under `source: ./plugins/mizan-eval`),
not repository-root-relative.

Because the repository also follows the `agent-skills` marketplace schema, the
same skills install through the other standard channels:

```bash
# npx skills
npx skills add ghchinoy/mizan --skill run-eval

# Gemini CLI
gemini skills install ghchinoy/mizan --path plugins/mizan-eval/skills/run-eval
gemini skills install ghchinoy/mizan --path plugins/mizan-results/skills/report-to-html

# Or copy the skill directory into your agent's skills dir
cp -r plugins/mizan-eval/skills/run-eval ~/.claude/skills/
cp -r plugins/mizan-results/skills/report-to-html ~/.claude/skills/
```

All channels require the `mizan` CLI on PATH; live evaluations additionally need
Google Application Default Credentials (`gcloud auth application-default login`)
and a configured project — exactly what the CLI itself needs.

## Plugins & skills

### `mizan-eval` — evaluation

#### `run-eval`

Run a **single (pointwise)** evaluation, or a **pairwise (compare)** evaluation,
against a metric template, and explain the verdict.

```bash
# discover a metric (no --tag filter exists; use --namespace / --kind)
mizan registry list -o json
mizan registry get <namespace>/<slug> -o json

# single / pointwise
mizan eval run --metric <namespace>/<slug> --field <key>=<value> -o json

# pairwise / compare
mizan eval pairwise --metric <namespace>/<slug> \
  --baseline <baseKey>=<text> --candidate <candKey>=<text> -o json
```

Fill instance inputs by modality: `--field key=value` for text, `--file
key=/path` for a local media asset (staged to GCS by the engine), `--gcs
key=gs://…` for a pre-staged asset. Mizan **hard-errors** if a `gs://` URI or a
local media path is passed to a text slot (`--field`/`--baseline`/`--candidate`) —
its `guardTextSlot` protection — so always use `--file`/`--gcs` for media.

`-o json` returns the `eval.Result` object: `Score` (pointwise) or
`PairwiseChoice` (`BASELINE`/`CANDIDATE`/`TIE`), `Explanation`, optional
`CustomOutput`/`rubric_detail` (with `--rubric-detail`), any `warnings`, and
`Stats` (`duration_ns`, and `token_usage` on the genai path). The resolved
autorater (`Applied`) is deliberately not serialized.

A successful run is persisted by default (opt out with `--no-store`), but the run
JSON does not carry the RunID; retrieve it afterward:

```bash
mizan results list --metric <namespace>/<slug> --limit 1 -o json   # array, newest first; RunID is .[0].RunID
mizan results show <run-id> -o json
```

See the skill source at
[`plugins/mizan-eval/skills/run-eval/SKILL.md`](../plugins/mizan-eval/skills/run-eval/SKILL.md).

#### `run-eval-set`

Run a **curated multi-concern eval-set** — several weighted metric templates —
against one shared asset/response, then interpret the **weighted scorecard**:
per-member verdicts, the aggregate, the overall PASS/FAIL, and (for CI) the gate
exit code. Use this instead of `run-eval` when the question is "how does this asset
do across *all* our concerns at once, and does it pass the bar?"

In this phase `--set` is a **filesystem path** to an `EvalSet` manifest (a
`kind: EvalSet` YAML), not a registry id. Locate an existing manifest (commonly
under a pack's `evalsets/`, e.g. the shipped
[`docs/examples/evalset-quickstart/evalsets/answer-quality.yaml`](examples/evalset-quickstart/evalsets/answer-quality.yaml))
or scaffold one from the concerns the user describes, then run it:

```bash
# discover / confirm member metrics resolve
mizan registry get <namespace>/<slug> -o json

# run the set against shared inputs (same modality flags as run-eval)
mizan eval run --set ./evalsets/answer-quality.yaml \
  --field prompt="$PROMPT" --field response="$RESPONSE" -o json
```

Fill the manifest's shared `spec.inputs` with `--field key=value` (text),
`--file key=/path` (local media, staged to GCS), or `--gcs key=gs://…`
(pre-staged); the same `guardTextSlot` protection applies, so use `--file`/`--gcs`
for media. `--fail-fast` aborts at the first errored/missing member; `--model`
overrides the autorater for every member. `--set` and `--metric` are mutually
exclusive — exactly one is required.

`-o json` returns the `evalset.EvalSetResult` object. These structs carry **no json
tags**, so top-level and nested `evalset` fields serialize in **PascalCase**; only
the embedded `eval.Result` (the `Result` field) keeps the `run-eval` snake_case
tags (`rubric_detail`, `warnings`, `duration_ns`, `token_usage`):

- `SetID`/`SetName`/`Version`/`AssetClass` — the manifest metadata.
- `Members[]` — one row per member: `MetricID`, `Status` (`OK`/`Errored`/`Missing`/
  `Skipped`), `Weight`, `Required`, `Score` (mirrored; `null` for non-scalar/non-OK
  members), the full embedded `Result` (`eval.Result`), and `Error`.
- `Aggregate` — `Method` (`mean`/`weighted-mean`/`min`), `Score`, `Threshold`,
  `Passed`, `Scored`, `Failed`.
- `Verdict` — the overall `PASSED`/`FAILED`, **always computed** regardless of the
  gate. `Gate` — the manifest's opt-in gate flag. `StartedAt` (RFC3339) and
  `Duration` (nanoseconds).

**Gate exit code (for CI).** The `Verdict` is always shown on stdout; whether a
`FAILED` verdict also fails the process is the opt-in gate (`aggregation.gate`). A
non-zero exit happens **only** when the set is a gate **and** the verdict is
`FAILED`; every other combination exits `0`:

| gate | verdict | exit |
|------|---------|------|
| false | PASSED | 0 |
| false | FAILED | 0 |
| true  | PASSED | 0 |
| true  | FAILED | 1 (fails the CI step) |

Wire an eval-set into CI with a gated manifest (e.g.
[`answer-quality-strict-gate.yaml`](examples/evalset-quickstart/evalsets/answer-quality-strict-gate.yaml))
and let the exit code fail the build. The `evalset.EvalSetResult` `-o json` contract
and this exit-code table are both covered by hermetic gates (the drift test in
`internal/skilldocs` and the exit-code test in `cmd/mizan`).

**Out of scope:** eval-set *results* are **not** persisted, so there is **no**
per-eval-set history/trend. The `results summary`/`trend` commands **do exist**, but
they aggregate persisted single-eval runs — not eval-sets — so do not point the user
at `mizan results` for a set's history. Per-eval-set persistence/trend is a
documented follow-up, not a shipping capability.

See the skill source at
[`plugins/mizan-eval/skills/run-eval-set/SKILL.md`](../plugins/mizan-eval/skills/run-eval-set/SKILL.md).

### `mizan-results` — results reporting

#### `report-to-html` (flagship)

Turn eval results already persisted in the local store into **one self-contained,
standalone HTML report** — a summary table, a score distribution, and a per-metric
trend over time. The report embeds its CSS, JavaScript, and data inline, so it
opens directly from the filesystem with **no server and no external request**; it
is deliberately **not** coupled to the docs-site styling.

The data source is the real, ships-today command `mizan results list -o json` (a
JSON array of `results.Result`, newest first). Filtering uses the real flags below,
and all summary/trend numbers are computed **client-side** by the bundled renderer
— this is the **default** path and always runs. `results list --tag` is **deferred
/ not used** (that flag exists but tag-filtered discovery is out of scope, and
`--tag` — including `results summary --tag` — is never invoked):

```bash
# query + filter with the real flags (metric / namespace / since / limit)
mizan results list --namespace <ns> --since 2026-08-01 -o json > results.json

# render one self-contained report.html (paths passed as argv, not interpolated)
python3 plugins/mizan-results/skills/report-to-html/scripts/render_report.py \
  --input results.json --output report.html --threshold 3

# or stream straight from the CLI over stdin
mizan results list -o json | \
  python3 plugins/mizan-results/skills/report-to-html/scripts/render_report.py \
  --output report.html --threshold 3
```

The renderer aggregates over `Outcome.Score` (mean/min/max, pass-rate at
`--threshold`, distribution), groups by `Template.ID`, and buckets by `RunAt` for
the trend; pairwise results (a `PairwiseChoice`, no `Score`) are tallied
separately. It reads the **local** store only — **no network, no credentials** —
and **degrades gracefully on an empty store**, producing a valid report that says
there are no results yet. The bundled `assets/report.template.html` is the neutral
template it fills; the `results.Result` `-o json` contract is covered by the
hermetic drift test in `internal/skilldocs`.

**Optional server-computed enrichment (B3, additive).** On top of the client-side
default, the renderer can *optionally* consume the CLI's own server-side
aggregations and render them as **additive** sections — the client-side view stays
the default and is never replaced:

```bash
mizan results summary --threshold 3 -o json > summary.json       # []results.TemplateSummary
mizan results trend --metric <ns>/<slug> --bucket week -o json > trend.json  # []results.TrendPoint
python3 plugins/mizan-results/skills/report-to-html/scripts/render_report.py \
  --input results.json --summary-input summary.json --trend-input trend.json \
  --output report.html --threshold 3
```

`results summary` takes `--metric`/`--namespace`/`--since`/`--until`/`--limit`/`--threshold`
(the `threshold` pass/fail/pass_rate object appears only with `--threshold`);
`results trend` **requires** `--metric` and takes `--bucket day|week`,
`--per-criterion` (adds `per_criterion`, rubric-detail only), `--since`/`--until`.
`--tag` (incl. `results summary --tag`) stays **deferred and is never invoked**.
When `--summary-input`/`--trend-input` are absent, empty, or unreadable the report
**degrades gracefully** to the client-side view and never fails. Eval-set runs are
not persisted (no per-eval-set aggregation), and cost/token trend is out of scope
(`results trend` trends `Outcome.Score` only). The `results.TemplateSummary` and
`results.TrendPoint` `-o json` shapes are covered by the same hermetic drift gate
in `internal/skilldocs`.

See the skill source at
[`plugins/mizan-results/skills/report-to-html/SKILL.md`](../plugins/mizan-results/skills/report-to-html/SKILL.md).

#### `triage-a-result`

Explain and triage a **single** past run: retrieve its full record, lay out the
provenance, explain the verdict, and suggest concrete next steps. It reads the
**local** results store only — **no network, no credentials** — and re-runs
nothing on its own.

```bash
# find a run id (real filter flags only; no --tag), then pull the full record
mizan results list --metric <ns>/<slug> -o json      # newest-first array; take .[0].RunID
mizan results show <run-id> -o json                  # one results.Result object
```

The skill walks the **provenance** — `Template.ID`/`Version`/`ContentHash`/`Kind`,
`Autorater.Model`/`ModelSource`, `Inputs[]`, `Mizan` build, `Invocation` — then
**explains the verdict** from `Outcome` (`Score` or `PairwiseChoice`,
`Explanation`, and the free-form per-criterion `CustomOutput` when
`RubricDetail` is true), and proposes ranked next steps (revise the asset and
re-run `run-eval`, pin the autorater via `configure-mizan`, pick a better template
via `discover-and-import-templates`, or compare peers via `report-to-html`).

It does **not** use `results list --tag` (that flag exists but tag-filtered
discovery is deferred), nor `results summary`/`trend` (those commands exist, but
their summary/trend enrichment is out of scope for this skill — deferred, B3). The
`results.Result` `-o json` contract is documented and hermetically drift-gated
**once** (under `report-to-html`, in `internal/skilldocs`); triage reads from that
single shape rather than re-declaring it.

See the skill source at
[`plugins/mizan-results/skills/triage-a-result/SKILL.md`](../plugins/mizan-results/skills/triage-a-result/SKILL.md).

### `mizan-authoring` — template authoring

#### `author-and-validate-a-template-pack`

Author a metric template (or a whole pack), **validate it credential-free**, and
run the export→PR→import collaborator loop. The whole authoring and validation
loop (steps 1–5) is creds-free — it touches only the local registry and the
filesystem, never Vertex AI.

```bash
# 1. scaffold a pack (writes mizan-pack.yaml + empty templates/ and evalsets/)
mizan pack init <dir> --name <namespace>

# 2. author a template (pick --kind; --tag is folksonomy tagging for curation)
mizan registry create --id <ns>/<slug> --kind rubric \
  --prompt 'Evaluate: {{response}}' --input 'response:text:true' \
  --rubric-group 'clarity=clear;concise' --tag <industry> -o json

# 3. add it to the pack (or: registry export --out <dir> --id <ns>/<slug>)
mizan pack add <dir> --from <ns>/<slug>

# 4. validate the pack — the creds-free CI PR gate
mizan pack validate <dir>
```

`--kind` is one of `single`/`pointwise`, `compare`/`pairwise`, `rubric`, or
`custom_schema` (fill the kind-specific data with `--rubric-group` /
`--response-schema` / `--baseline-field`+`--candidate-field`). `-o json` on
`registry create` prints the created `MetricTemplate` (`ID`, `Kind`, `Tags`,
`Inputs`, …). This skill does **not** do tag-filtered *discovery* — `--tag` here
is for *authoring* folksonomy tags only.

**`pack validate` is text/exit-code driven — it ignores `-o json`.** Branch on the
exit code and parse the text report:

- **Exit `0` = accept.** No ERROR findings (warnings alone never fail). A clean
  pack prints `OK: no defects found.` and a `0 error(s), 0 warning(s)` summary.
- **Exit non-zero (`1`) = reject.** At least one ERROR. Findings are grouped by
  file (`  [ERROR] <message>` / `  [warn ] <message>`) and end with a
  `N error(s), M warning(s)` summary. Fix each `[ERROR]` and re-run until exit `0`.

```
templates/bad.yaml:
  [ERROR] metadata.version is required and must be semver

1 error(s), 0 warning(s)
```

The optional `--dry-run` adds a **live** step 6 (one materialize+call per template
to confirm API acceptance) that needs credentials — it is **not** part of the
creds-free CI gate; do not run it in CI.

The collaborator loop is export→PR→import: a validated pack dir is PR-ready
(Mizan never pushes), and the receiving side imports it with
`mizan registry import <src> --strategy newer -o json` (strategies
`newer`/`skip`/`overwrite`/`fork`; `-o json` reports the reconciliation counts and
per-template `Entries`). The `pack validate` exit-code + text-report contract is
covered by the hermetic gate in `internal/skilldocs` (no json shape — `pack
validate` emits none).

See the skill source at
[`plugins/mizan-authoring/skills/author-and-validate-a-template-pack/SKILL.md`](../plugins/mizan-authoring/skills/author-and-validate-a-template-pack/SKILL.md).

#### `rubric-generate-from-brand-book`

Turn a **brand book or guidance document** into a reviewed, **frozen** Mizan
rubric. The decomposition is **agent-side and suggest-first** (the owner's CUJ4
decision): the agent reads the guidance doc, suggests existing templates to reuse,
drafts sample prompts and candidate criteria, then drives the real `mizan`
generation and freeze commands. There is **no** Mizan "brand-book" command and the
skill presupposes none — Mizan supplies the generation and freeze plumbing; the
LLM decomposition/synthesis is the agent's.

The loop is **suggest-first → generate → union → validate → freeze → import**:

```bash
# 1. SUGGEST FIRST — find existing templates to reuse (no --tag filter; match client-side)
mizan registry list -o json

# 2. GENERATE a draft rubric from a representative sample prompt (one live call; uses ADC).
#    Writes a draft YAML with a rubricProvenance block; writes NOTHING to the registry.
mizan rubric generate \
  --sample '<a representative prompt for the asset being scored>' \
  --id <ns>/<slug> --out <draft.yaml> \
  --recipe general_quality_v1 --group-name brand \
  --add-criterion 'The response never uses the competitor names listed in the brand book.'

# 3. VALIDATE + FREEZE (creds-free): wrap the reviewed draft in a pack, validate, import.
mizan pack init packs/<ns> --name <ns>
cp <draft.yaml> packs/<ns>/templates/<slug>.yaml
mizan pack validate packs/<ns>                              # text + exit code (ignores -o json)
mizan registry import packs/<ns> --strategy newer -o json   # the freeze; reconciliation report
```

`--recipe` is one of `general_quality_v1` (default; prompt-aligned),
`instruction_following_v1` (most prompt-alignment-focused), or `text_quality_v1`
(more holistic). `--group-name` is independent of `--recipe` — it only labels the
output `RubricGroups` key. `--add-criterion` is repeatable and **unions**
hand-authored criteria *after* the generated ones (CUJ9, union-before-freeze); each
criterion's origin (`adaptive-generated` vs `hand-authored`) is recorded in
provenance so a reviewer can always tell them apart. **The draft file is not a
valid import source on its own** — wrap it in a pack (`registry create` has no
whole-file input), then import the pack to freeze.

The draft carries a `spec.rubricProvenance` block recording **how** the rubric was
drafted (`method: adaptive-generated`, `generatorModel`, `recipe`, a bounded
`sampleInputRef` — a capped preview + SHA-256 of the full sample, never the sample
verbatim — `generatedAt`, `apiVersion`, and per-criterion `rubricMeta` with
`origin`):

```yaml
spec:
  rubricProvenance:
    method: adaptive-generated
    generatorModel: gemini-2.5-flash
    recipe: general_quality_v1
    sampleInputRef: 'inline:"…" sha256:…'
    generatedAt: 2026-01-01T00:00:00Z
    apiVersion: v1beta1:generateInstanceRubrics
    rubricMeta:
      - {group: brand, criterion: "…", type: STICKY, importance: HIGH, origin: adaptive-generated}
```

> **Illustrative only.** The YAML above is a hand-written excerpt for reading
> here; it is **not** the drift-gated source of truth. The authoritative,
> mechanically drift-gated `rubricProvenance` shape lives in the
> `rubric-generate-from-brand-book` `SKILL.md` (behind its drift marker, checked
> against `registry.MarshalTemplate` + `registry.RubricProvenance`/`RubricMeta`
> by the hermetic gate in `internal/skilldocs`). If the two ever disagree, the
> SKILL.md block wins — treat this excerpt as documentation, not contract.

**Immediate eval + freeze in one step (CUJ8).** When the user has a prompt *and* a
response to score now, `mizan eval adaptive --prompt … --response … --save-as
<ns>/<slug>` generates criteria, scores the response (each one live call), and
freezes the generated rubric into the registry. `-o json` emits the standard
`eval.Result` object (same shape as `run-eval`, covered by the existing
`eval.Result` drift gate). `eval adaptive --save-as` cannot union hand-authored
criteria — use the `rubric generate --add-criterion` path for union-before-freeze.

**N5 — no credential custody.** The suggest step and the whole freeze loop
(`registry list`, `pack init`, `pack validate`, `registry import`) are
credential-free. Only `rubric generate` and `eval adaptive` issue live Vertex AI
calls, and they use the user's existing ADC exactly as the CLI does; the skill
never takes or stores keys.

Two hermetic gates in `internal/skilldocs` anchor this skill's documented shapes to
real code: the draft **`rubricProvenance`** YAML structure (against
`registry.MarshalTemplate` + `registry.RubricProvenance`/`RubricMeta`) and the
**`registry import -o json`** reconciliation report (against
`registry.ImportReport`). The `pack validate` text/exit-code contract reuses the
`author-and-validate-a-template-pack` gate, and the `eval adaptive` `eval.Result`
shape reuses the `run-eval` gate.

See the skill source at
[`plugins/mizan-authoring/skills/rubric-generate-from-brand-book/SKILL.md`](../plugins/mizan-authoring/skills/rubric-generate-from-brand-book/SKILL.md).

#### `discover-and-import-templates`

Bring ready-made metric templates into the local registry instead of authoring
from scratch. Import a pack source — a local checkout, a single pack dir, or a
git URL such as the community `mizan-templates` repo (with **no** `<src>`, the
configured `templates-repo` default) — **preview first**, reconcile id clashes
safely, then browse what landed. It touches only the local registry and pack
cache: **creds-free**, no LLM call.

```bash
# 1. PREVIEW (write nothing) — default source or an explicit git URL / local dir
mizan registry import --dry-run -o json
mizan registry import github.com/ghchinoy/mizan-templates --dry-run -o json

# 2. COMMIT with a reconciliation strategy (newer|skip|overwrite|fork)
mizan registry import -o json --strategy newer

# 3. BROWSE what landed (narrow by namespace / kind — NOT --tag)
mizan registry list --namespace <ns> -o json
mizan registry get <ns>/<slug> -o json
```

The `registry import -o json` result is a `registry.ImportReport` — per-action
counts (`Inserted`/`Updated`/`Skipped`/`Conflicted`/`Unchanged`/`Forked`) plus a
per-template `Entries[]` of `{ID, Action, Reason}`; the default `newer` strategy
never clobbers a locally edited (dirty) template. That shape is the **same
`registry.ImportReport` already drift-gated** under `rubric-generate-from-brand-book`
(freeze step), so it is not re-gated. The discovery **identity fields** read from
`registry list`/`get -o json` (`ID`, `Name`, `Description`, `Version`, `Kind`,
`Tags`, `Authors`, `License`, `Source`) are anchored to the real
`registry.MetricTemplate` encoder by a hermetic gate in `internal/skilldocs`.

**Deferred surface (explicit disclaimer):** `registry list --tag` exists but
**tag-filtered discovery is deferred / out of scope** — the skill narrows with
`--namespace`/`--kind` and matches tags **client-side**, and never invokes
`--tag`. It references no `kind: heuristic` authoring, and does not invoke `results
summary`/`trend` (those commands exist, but their enrichment is out of scope for
this skill — deferred, B3).

See the skill source at
[`plugins/mizan-authoring/skills/discover-and-import-templates/SKILL.md`](../plugins/mizan-authoring/skills/discover-and-import-templates/SKILL.md).

### `mizan-setup` — configuration & onboarding

#### `configure-mizan`

Get a Mizan environment ready to evaluate: read the **resolved** configuration,
set the handful of values a live eval needs, confirm the build, and verify
Application Default Credentials — **checking, never storing, any credential
(N5)**. It reads/writes only the local config file
(`<UserConfigDir>/mizan/.env`) and reads the resolved config over `-o json`; no
network.

```bash
mizan version -o json                            # {version, commit, date}
mizan config show -o json                        # resolved config + per-key Sources (alias: config list)
mizan config set project-id my-gcp-project       # required for live eval
mizan config set location us-central1
mizan config set default-model gemini-2.5-pro    # optional; else the built-in default
```

`config show -o json` renders the full `config.Config` — `ProjectID`, `Location`,
`StagingBucket`, `APIEndpoint`, the registry/results store paths and settings,
`DefaultTemplatesRepo`, `DefaultModel`, `AuthorName`, `DefaultLicense`, and a
`Sources` map (each `config set` key → `env`/`env-file`/`default`) — so the skill
can tell a real setting from a built-in default. Both the `config.Config` and
`version.Info` `-o json` shapes are covered by hermetic drift gates in
`internal/skilldocs`. `config set` writes **only non-secret** settings to the
`0600` config file (its output is a text confirmation line, not JSON) — it is not
a credential store. ADC presence is verified without capturing the token (e.g.
`gcloud auth application-default print-access-token >/dev/null`); a missing ADC is
fixed by the user's own `gcloud auth application-default login`.

See the skill source at
[`plugins/mizan-setup/skills/configure-mizan/SKILL.md`](../plugins/mizan-setup/skills/configure-mizan/SKILL.md).

## Non-goals

- **No server / self-contained.** Skills are static instruction files plus
  optional local helper scripts that drive the local `mizan` binary. Nothing
  listens on a port.
- **No credential custody.** Skills use the user's existing ADC and never take or
  store keys.
- **No new or changed `mizan` CLI command.** Skills wrap commands that exist
  today. Capabilities that would need unbuilt commands are deferred, not stubbed.

## Roadmap

`run-eval` was the first vertical slice; the flagship `report-to-html`
(`mizan-results`) results skill shipped on top of it, `run-eval-set` extends the
`mizan-eval` plugin with multi-concern scorecards and CI gating, and
`author-and-validate-a-template-pack` (`mizan-authoring`) covers creds-free pack
authoring and the export→PR→import loop, and `rubric-generate-from-brand-book`
extends `mizan-authoring` with suggest-first, agent-side rubric synthesis from a
brand book plus the generate→union→validate→freeze→import loop. The Tier-2
onboarding/discovery fan-out then landed: `discover-and-import-templates`
(`mizan-authoring`) for pulling in community/starter templates,
`configure-mizan` (`mizan-setup`) for first-run configuration and the ADC check,
and `triage-a-result` (`mizan-results`) for explaining and debugging a single
past run. The B3 enrichment then landed: `report-to-html` gained an **optional,
additive** path consuming `results summary`/`trend -o json` (the client-side
aggregation stays the default/fallback). Capabilities that remain out of scope —
per-eval-set persistence/trend, tag-filtered discovery (`registry list`/`results
list --tag`, incl. `results summary --tag`),
heuristic authoring (`kind: heuristic`), and an MCP-server-backed variant
(`mizan mcp`) — are deferred, not stubbed. See the project roadmap for sequencing.

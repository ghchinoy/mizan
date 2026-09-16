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

### `mizan-results` — results reporting

#### `report-to-html` (flagship)

Turn eval results already persisted in the local store into **one self-contained,
standalone HTML report** — a summary table, a score distribution, and a per-metric
trend over time. The report embeds its CSS, JavaScript, and data inline, so it
opens directly from the filesystem with **no server and no external request**; it
is deliberately **not** coupled to the docs-site styling.

The data source is the real, ships-today command `mizan results list -o json` (a
JSON array of `results.Result`, newest first). There is **no `mizan results
summary`/`trend` command**, and this skill deliberately does **not** use
`results list --tag` (that flag exists but tag-filtered discovery is out of scope
here) — filtering uses the real flags below, and all summary/trend numbers are
computed **client-side** by the bundled renderer:

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

See the skill source at
[`plugins/mizan-results/skills/report-to-html/SKILL.md`](../plugins/mizan-results/skills/report-to-html/SKILL.md).

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
(`mizan-results`) results skill has shipped on top of it. Planned fan-out includes
`run-eval-set`, template-authoring skills, and Tier-2 onboarding/discovery skills.
Capabilities that would need unbuilt CLI commands — a `results summary`/`trend`
enrichment of `report-to-html`, tag-filtered discovery, heuristic authoring — are
deferred, not stubbed. See the project roadmap for sequencing.

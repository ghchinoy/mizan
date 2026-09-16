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
│   └── mizan-eval/
│       ├── plugin.json           # Agent Plugins v1.0.0 manifest
│       └── skills/
│           └── run-eval/
│               └── SKILL.md      # the skill
├── scripts/
│   └── validate-plugins.sh       # structural + frontmatter conformance gate
└── internal/
    └── skilldocs/
        └── drift_test.go         # -o json contract drift gate (make check)
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
- **`internal/skilldocs/drift_test.go`** — a binder-style, **hermetic** drift
  test. For `run-eval` it constructs a representative `eval.Result`, renders it
  through the same `encoding/json` path the CLI's `-o json` uses, and asserts
  **key-set equality** (path-anchored) against the `eval.Result` JSON block
  documented in `SKILL.md`, plus a reflection pass proving every declared field
  is documented. It makes **no network or ADC calls**, so it passes in CI without
  credentials, and it fails the moment a field is added, renamed, or retagged on
  `eval.Result` until the skill is updated.

Both are aggregated by `make check` (which also runs build/vet/fmt/lint/vuln and
the full `go test ./...`).

## Installing the skills

The primary channel is the **Claude plugin marketplace** (the root
`.claude-plugin/marketplace.json`):

```text
# In Claude Code:
/plugin marketplace add ghchinoy/mizan
/plugin install mizan-eval@mizan
```

Because the repository also follows the `agent-skills` marketplace schema, the
same skills install through the other standard channels:

```bash
# npx skills
npx skills add ghchinoy/mizan --skill run-eval

# Gemini CLI
gemini skills install ghchinoy/mizan --path plugins/mizan-eval/skills/run-eval

# Or copy the skill directory into your agent's skills dir
cp -r plugins/mizan-eval/skills/run-eval ~/.claude/skills/
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
mizan results list --metric <namespace>/<slug> --limit 1 -o json   # read .RunID
mizan results show <run-id> -o json
```

See the skill source at
[`plugins/mizan-eval/skills/run-eval/SKILL.md`](../plugins/mizan-eval/skills/run-eval/SKILL.md).

## Non-goals

- **No server / self-contained.** Skills are static instruction files plus
  optional local helper scripts that drive the local `mizan` binary. Nothing
  listens on a port.
- **No credential custody.** Skills use the user's existing ADC and never take or
  store keys.
- **No new or changed `mizan` CLI command.** Skills wrap commands that exist
  today. Capabilities that would need unbuilt commands are deferred, not stubbed.

## Roadmap

`run-eval` is the first vertical slice. Planned fan-out (each conditional on this
slice) includes a flagship `report-to-html` results skill, `run-eval-set`, and
template-authoring skills. See the project roadmap for sequencing.

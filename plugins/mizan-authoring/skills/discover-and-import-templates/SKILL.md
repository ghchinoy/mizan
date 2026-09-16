---
name: discover-and-import-templates
description: Discover starter/industry metric templates from the community `mizan-templates` repo (or any pack source / git URL) and import them into the local Mizan registry, using the mizan CLI over -o json. Preview with `--dry-run`, reconcile id clashes with `--strategy`, and browse what landed with `registry list`/`get`. Use when onboarding, when a user wants ready-made evaluation templates instead of authoring from scratch, or asks to pull in / update templates from a repo.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest); importing a git URL also needs `git` on PATH. Reads/writes only the local template registry and pack cache — no LLM call and no credentials (creds-free); this skill never takes or stores keys.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Discover & import metric templates (`discover-and-import-templates`)

Bring ready-made metric templates into a local Mizan registry instead of
authoring every metric from scratch: import a pack source (a local checkout, a
single pack dir, or a git URL such as the community `mizan-templates` repo),
preview the outcome, reconcile id clashes safely, then browse what landed. This
skill drives the local `mizan` CLI over its machine-readable `-o json` output;
it **starts no server**, makes **no LLM call**, needs **no credentials**, and
**re-implements no CLI logic** (discovery and reconciliation are the CLI's job).

## When to use this skill

- "Get me some starter / industry evaluation templates."
- "Import the templates from `github.com/ghchinoy/mizan-templates`."
- "Pull in / update templates from this pack repo (and don't clobber my edits)."
- "What templates do I have locally now?" (browse via `registry list`/`get`).

## Prerequisites (check first)

1. **`mizan` is installed.** Stop with an install hint if it fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```
2. **`git` is needed only for a git-URL source** (a local dir needs no git).
3. **No credentials.** Import touches only the local registry and pack cache; it
   calls no LLM and needs no ADC. Do not collect or store keys.

## Step 1 — preview the import (`registry import --dry-run -o json`)

Always **preview first** with `--dry-run` so nothing is written until the outcome
is understood. The source `<src>` is one of: a local checkout containing a
`packs/` dir, a single pack dir, or a git URL. With **no `<src>`**, Mizan imports
from the configured default templates repo (`templates-repo`; default
`github.com/ghchinoy/mizan-templates`).

```bash
# preview importing the community starter templates (default source), as JSON
mizan registry import --dry-run -o json

# preview a specific git URL (scheme-less form is accepted; needs git)
mizan registry import https://github.com/ghchinoy/mizan-templates --dry-run -o json

# preview a local pack tree or a single pack dir
mizan registry import ./path/to/pack-or-checkout --dry-run -o json

# restrict to one namespace's packs
mizan registry import --namespace <ns> --dry-run -o json
```

Reason over the returned `registry.ImportReport` (below) to see exactly what a
real import would insert/update/skip/fork before committing.

## Step 2 — reconcile id clashes with `--strategy`

Incoming templates are reconciled against the local registry **by id** using
`--strategy` (default `newer`). A template you have **edited locally (dirty)** is
protected — under the default it is skipped with a warning rather than
overwritten. Re-importing an unchanged pack is a no-op.

| `--strategy` | behavior |
|---|---|
| `newer` *(default)* | take the higher version; on an equal-version but changed-content clash, report a **conflict** and skip (never clobber) |
| `skip` | only insert **absent** ids; never overwrite |
| `overwrite` | replace the local copy unconditionally (**including** dirty local edits) |
| `fork` | import a conflicting/dirty upstream under `<ns>-fork/<slug>`, keeping the local copy |

```bash
# commit the import once the dry-run looks right (drop --dry-run)
mizan registry import -o json --strategy newer

# only add what's missing, never touch existing local templates
mizan registry import ./pack -o json --strategy skip
```

### The `registry import -o json` result — `registry.ImportReport`

`registry import -o json` prints a single `registry.ImportReport`: the source
info, the strategy, whether it was a dry run, per-action **counts** that partition
the processed templates (each template increments exactly one of
`Inserted`/`Updated`/`Skipped`/`Conflicted`/`Unchanged`/`Forked`), and a
per-template `Entries` list of `{ID, Action, Reason}`. This is the **same
`registry.ImportReport` shape documented — and drift-gated — under the
`rubric-generate-from-brand-book` skill's freeze step** (marker
`<!-- drift:registry-import -o json -->`) and in `docs/agent-skills.md`; that
single hermetic drift gate in `internal/skilldocs` is the source of truth for the
shape, so it is not re-documented as a second contract block here. Read it as:

- **`Source`** — `{Type, Origin}` describing where templates came from.
- **`Strategy`** — the reconciliation strategy applied. **`DryRun`** — `true` for
  a preview (nothing written).
- **`Inserted` / `Updated` / `Skipped` / `Conflicted` / `Unchanged` / `Forked`** —
  the per-action counts.
- **`Entries[]`** — one `{ID, Action, Reason}` per processed template; `Reason`
  explains a skip, conflict, or the fork id. Surface conflicts and forks to the
  user.

There is no `--format`; the switch is `-o json`.

## Step 3 — browse what landed (`registry list` / `registry get`)

```bash
# list everything (newest schema), or narrow by namespace / kind
mizan registry list -o json
mizan registry list --namespace <ns> -o json
mizan registry list --kind rubric -o json        # single|pointwise, compare|pairwise, rubric, custom_schema

# full detail for one template
mizan registry get <namespace>/<slug> -o json
```

`registry list -o json` prints a JSON **array** of `registry.MetricTemplate`
objects; `registry get <id> -o json` prints **one**. For discovery you reason over
each template's stable **identity fields** (all top-level keys, Go's default
capitalized names — read them exactly as shown):

| field | use in discovery |
|---|---|
| `ID` | `<namespace>/<slug>` — the stable id to eval with / import |
| `Name` | human-readable title |
| `Description` | what the metric evaluates |
| `Version` | semver of the template |
| `Kind` | metric kind (`pointwise`/`pairwise`/`rubric`/`custom_schema`/…) |
| `Tags` | folksonomy tags (for **client-side** matching — see below) |
| `Authors` | attribution |
| `License` | template license |
| `Source` | provenance (e.g. `pack:<ns>@origin`) |

These documented identity fields are anchored to the real
`registry.MetricTemplate` encoder by a hermetic gate in `internal/skilldocs`
(`registry list`/`get -o json`). The full template body (prompt, rubric groups,
schema, inputs, etc.) is the authoring surface documented under
`author-and-validate-a-template-pack`; discovery only needs the identity fields
above.

## Discovery without a tag filter (explicit deferred-surface disclaimer)

**This skill does NOT use `registry list --tag`.** A `--tag` flag exists on
`registry list` (it filters to templates carrying ALL given tags), **but
tag-filtered discovery is deliberately out of scope / deferred** for this skill.
Discover by:

1. `registry import --dry-run` to see what a source offers, and/or
2. `registry list --namespace <ns>` / `--kind <k>` to narrow, then
3. **matching `Tags` client-side** (read each template's `Tags` from the JSON and
   filter in the agent) when a tag-like intent is given.

Do **not** invoke `--tag`, and do not reference any `results list --tag`,
`kind: heuristic` authoring, or `results summary`/`trend` command — those are
deferred capabilities, not part of this skill.

## Reporting back to the user

Summarize the source imported, the strategy, whether it was a dry run, and the
per-action counts; call out any **conflicts** or **forks** by id with their
`Reason`, and suggest the fix (e.g. re-run with `--strategy overwrite` or `fork`,
or resolve the local dirty edit). Then list the notable templates now available
(`ID`, `Name`, `Kind`) and suggest running `run-eval` against one. Surface any CLI
stderr verbatim with the concrete fix.

---
name: author-and-validate-a-template-pack
description: Author a Mizan metric template (or a whole template pack), validate it creds-free with `mizan pack validate` (the CI PR gate — it reads the exit code and text report, not JSON), and run the export→PR→import collaborator loop against a shared templates repo, driving the `mizan` CLI. Use when a user asks to author, scaffold, package, validate, or share a Mizan metric template, rubric, or template pack, or to prepare a pack for a pull request.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest). Authoring and validation (steps 1–5) are fully credential-free — they touch only the local registry and the filesystem, never Vertex AI. Only the optional `--dry-run` live-acceptance probe (step 6) calls Vertex AI and needs Google Application Default Credentials (ADC); this skill never takes or stores credentials.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Author and validate a Mizan template pack (`author-and-validate-a-template-pack`)

Scaffold a template pack, author metric templates into it, and **validate it
credential-free** with `mizan pack validate` — the same creds-free gate a shared
templates repo runs in CI on every pull request. Then run the
export→PR→import loop to share templates with collaborators. This skill wraps
commands that exist in the Mizan CLI today; it starts no server, stores no
credentials, and re-implements no CLI logic.

## When to use this skill

- "Author / write a new metric (rubric / pointwise / pairwise / custom_schema) template."
- "Scaffold a template pack" or "package these templates into a pack."
- "Validate this pack" or "will this pack pass CI?"
- "Share / contribute these templates" (export → open a PR → import on the other side).

## Prerequisites (check first)

1. **`mizan` is installed.** Run the precheck and stop with an install hint if it fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```
2. **No credentials needed for authoring/validation.** Steps 1–5 below are
   **creds-free**: they touch only the local registry and the filesystem, never
   Vertex AI. Do not collect or store keys. Only the optional `--dry-run` probe
   (step 6) issues a live API call and needs the user's existing ADC — never
   supply credentials yourself.

## The authoring loop (steps 1–5 are creds-free)

### Step 1 — Scaffold the pack

```bash
mizan pack init <dir> --name <namespace>
```

`pack init` writes a `mizan-pack.yaml` manifest (whose `metadata.name` is the
namespace), an empty `templates/` directory, and an empty `evalsets/` directory.
`--name` is required; the namespace is lowercase letters, digits, and hyphens
(e.g. `google-brand`).

### Step 2 — Author a metric template

Create the template in the local registry. Pick `--kind` by what you are scoring;
fill the kind-specific data with the matching flag:

```bash
# rubric — grouped criteria (repeat --rubric-group; same name accumulates)
mizan registry create --id <ns>/<slug> --kind rubric \
  --prompt 'Evaluate the response: {{response}}' \
  --input 'response:text:true' \
  --rubric-group 'clarity=clear;concise' --rubric-group 'tone=on-brand' \
  --tag <industry> --tag <concern> -o json

# single / pointwise — score one response
mizan registry create --id <ns>/<slug> --kind single \
  --prompt 'Rate the response: {{response}}' --input 'response:text:true' -o json

# compare / pairwise — pick the better of two responses
mizan registry create --id <ns>/<slug> --kind compare \
  --prompt 'Which response is better?' \
  --input 'baseline:text:true' --input 'candidate:text:true' \
  --baseline-field baseline --candidate-field candidate -o json

# custom_schema — constrain the judge to a JSON-Schema response
mizan registry create --id <ns>/<slug> --kind custom_schema \
  --prompt 'Assess the response: {{response}}' --input 'response:text:true' \
  --response-schema '{"type":"object","properties":{"score":{"type":"integer"}}}' -o json
```

Real, verified flags (from `cmd/mizan/registry.go`):

- `--id <namespace>/<slug>` — **required**; the stable template id.
- `--kind` — one of `single` (a.k.a. `pointwise`), `compare` (a.k.a. `pairwise`),
  `rubric`, `custom_schema`. (`heuristic` also exists but heuristic authoring is
  out of scope for this skill.)
- `--prompt` — the metric prompt template, with `{{var}}` placeholders.
- `--input 'name:modality[:required]'` — declare an input (repeatable; modality is
  `text|image|audio|video|music`; `required` defaults to `false`).
- `--rubric-group 'name=criterion one;criterion two'` — **rubric** criteria
  (repeatable; criteria split on `;`; same group name accumulates). Or
  `--rubric-groups-file <path>`.
- `--response-schema '<json>'` / `--response-schema-file <path>` — **custom_schema**.
- `--baseline-field` / `--candidate-field` — **pairwise** response field names.
- `--tag <tag>` — folksonomy tag for community curation (repeatable). This is the
  supported tag surface for **authoring**; note this skill does **not** do
  tag-filtered *discovery* (that is a separate, out-of-scope capability).

`-o json` prints the created `MetricTemplate` object to **stdout** (its `ID`,
`Kind`, `Tags`, `Inputs`, `RubricGroups`, `Version`, …) — that object is the only
thing on stdout. `registry create` emits no warnings; on failure (e.g. a
kind/rubric/schema mismatch) it writes the error to **stderr** and exits non-zero.
The one optional diagnostic is the `--infer-inputs` summary (how many input
placeholders were inferred), which also goes to stderr — keeping stdout pure JSON.
Read `ID` back to confirm the create. A new template starts at version `0.1.0`.

### Step 3 — Add the template to the pack

Either add straight from the registry, or export by id — both write one
schema-valid file under `<dir>/templates/`:

```bash
mizan pack add <dir> --from <ns>/<slug>          # thin convenience over export
# or
mizan registry export --out <dir> --id <ns>/<slug>
```

`registry export` also supports `--namespace <ns>` (a whole namespace) or `--all`;
give **exactly one** selector. Mizan writes the files but never pushes.

### Step 4 — Validate the pack (the creds-free CI gate)

**This is the load-bearing gate.** `pack validate` runs the creds-free steps 1–5
(structural schema, identity, kind-specific semantics, placeholder consistency,
and lint) over every `MetricTemplate` and `EvalSet` manifest under `<path>`.

```bash
mizan pack validate <dir>
```

`<path>` is a single pack dir or a repo tree containing a `packs/` directory.

**Read the exit code and the text report — NOT JSON.** `pack validate` does **not**
emit `-o json`; passing `-o json` is silently ignored and you still get the text
report. So parse the text report and branch on the exit code:

<!-- gate:pack-validate -->
- **Exit code `0` = accept.** No ERROR-severity findings. Warnings alone never
  fail — a pack can be accepted with warnings. On a clean pack the report is:
  ```
  OK: no defects found.

  0 error(s), 0 warning(s)
  ```
- **Exit code non-zero (`1`) = reject.** At least one ERROR was found. Findings are
  grouped by file; each line is `  [ERROR] <message>` or `  [warn ] <message>`; the
  report ends with a `N error(s), M warning(s)` summary line:
  ```
  templates/bad.yaml:
    [ERROR] metadata.version is required and must be semver

  1 error(s), 0 warning(s)
  ```

Interpretation rules for the agent:

- Branch on the **exit code** for the accept/reject decision (`0` vs non-zero);
  it is the authoritative signal a CI job keys off.
- Parse the trailing `N error(s), M warning(s)` line to report counts.
- Each `[ERROR]` line is a blocking defect to fix; each `[warn ]` line is advisory
  (missing description/license, no autorater model, …) — surface warnings but do
  not treat them as failures.
- On reject, fix the reported ERRORs (e.g. add `metadata.version`, add the
  kind-required data) and re-run `pack validate` until it exits `0`.

### Step 5 — Prepare for sharing (export side of the collaborator loop)

A validated pack dir is PR-ready. Commit it and open a pull request with your own
git/`gh` tooling — **Mizan does not push**. This is the "export → PR" half of the
collaborator loop; the "import" half is step 7.

## Step 6 — Optional live acceptance probe (`--dry-run`, needs credentials)

`--dry-run` adds a step 6 on top of steps 1–5: after they pass, it issues **one
live materialize+call per template** to confirm the autorater API accepts each
template's shape. This step calls Vertex AI and needs ADC + a configured project,
so it is **not** part of the creds-free CI gate — run it only interactively when
the user asks to confirm live acceptance:

```bash
mizan pack validate <dir> --dry-run   # steps 1–5 (creds-free) THEN one live call per template
```

Do not run `--dry-run` in CI or when credentials are unavailable; the creds-free
steps 1–5 are the gate.

## Step 7 — Import (the other side of the collaborator loop)

On the receiving side, import a shared pack (a local checkout of a shared
templates repo, a single pack dir, or a git URL) into the local registry:

```bash
mizan registry import <src> --strategy newer -o json
```

- `--strategy` — `newer` (default; take the higher version, never clobber a dirty
  local edit), `skip`, `overwrite`, or `fork`.
- `--namespace <ns>` — import only packs under one namespace.
- `--dry-run` — preview the reconciliation without writing.
- With **no** `<src>`, Mizan imports from the configured default templates repo.

`-o json` prints an import report to **stdout** with the counts
(`Inserted`/`Updated`/`Skipped`/`Unchanged`/`Conflicted`/`Forked`) and a
per-template `Entries` list (each with an `Action` and `Reason`). Summarize what
changed; re-importing an unchanged pack is a no-op.

## Reporting back to the user

Summarize: the pack dir and namespace, the templates authored (ids + kinds +
tags), and the **`pack validate` result** — accepted (exit `0`) or rejected
(exit non-zero) with the error/warning counts and each `[ERROR]` to fix. For the
collaborator loop, report the export destination (PR-ready) and, on import, the
reconciliation counts. On any failure, surface the CLI's stderr message verbatim
and suggest the concrete fix.

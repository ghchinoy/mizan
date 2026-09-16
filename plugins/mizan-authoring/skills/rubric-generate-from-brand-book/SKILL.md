---
name: rubric-generate-from-brand-book
description: Turn a brand book or guidance document into a reviewed, frozen Mizan rubric metric template — the agent reads the doc, suggests existing templates to reuse, drafts sample prompts and candidate criteria, generates a draft rubric with `mizan rubric generate`, unions in hand-authored criteria, then validates and freezes it into the registry via `mizan registry import`, driving the `mizan` CLI. Use when a user asks to build, generate, or derive Mizan evaluation rubrics/criteria/metrics from a brand book, style guide, brand guidelines, content standards, or any guidance document, or to auto-suggest and author evals from written brand/quality guidance.
license: Apache-2.0
compatibility: Requires the `mizan` CLI on PATH (go install github.com/ghchinoy/mizan/cmd/mizan@latest). The suggest step (`registry list`) and the freeze steps (`pack init`, `pack validate`, `registry import`) are credential-free. Only the generation steps (`rubric generate`, `eval adaptive`) issue live Vertex AI calls and use the user's existing Google Application Default Credentials (ADC); this skill never takes or stores credentials.
metadata:
  author: ghchinoy
  version: "0.1.0"
---

# Generate a Mizan rubric from a brand book (`rubric-generate-from-brand-book`)

Decompose a brand book / guidance document into a **draft Mizan rubric**, review
and union it with hand-authored criteria, then **freeze** it into a reproducible
`rubric` (KindRubric) template in the registry. The decomposition is done by
**you, the agent, inside this skill** — you read the guidance doc and produce
(a) *suggestions* of existing templates to reuse and (b) *sample prompts +
candidate criteria* that you feed to the real `mizan rubric generate` /
`mizan eval adaptive` commands. There is **no** Mizan "brand-book" command and
this skill presupposes none: Mizan is the *pluggable LLM decomposer/synthesizer*'s
generation and freeze plumbing; the decomposition is agent-side and suggest-first
(the owner's CUJ4 decision).

This skill wraps commands that exist in the Mizan CLI today; it starts no server,
stores no credentials, and re-implements no CLI logic.

## When to use this skill

- "Build / generate Mizan evals (rubrics, criteria, metrics) from this brand book
  / style guide / brand guidelines / content standards / guidance doc."
- "Suggest which existing templates I can reuse for this brand guidance, then
  author the rest."
- "Draft a rubric from these brand rules, let me review it, then freeze it so I
  can rerun it reproducibly."

## The in → out contract

- **In:** one or more brand book / guidance documents + a target template id
  (`<namespace>/<slug>`).
- **Out:** (1) a **suggestion list** of existing registry templates worth
  reusing, and (2) a reviewed, **frozen** `rubric` template imported into the
  registry, whose criteria are the union of adaptive-generated ∪ hand-authored,
  with an auditable `rubricProvenance` record.

## Prerequisites (check first)

1. **`mizan` is installed.** Run the precheck and stop with an install hint if it
   fails:
   ```bash
   command -v mizan >/dev/null 2>&1 || {
     echo "mizan not found on PATH. Install with: go install github.com/ghchinoy/mizan/cmd/mizan@latest" >&2
     exit 1
   }
   ```
2. **Credentials (N5 — no credential custody).** The suggest step and the whole
   freeze loop (`registry list`, `pack init`, `pack validate`, `registry import`)
   are **creds-free**. Only the two generation commands (`rubric generate` and
   `eval adaptive`) issue live Vertex AI calls; they use the user's **existing
   ADC** exactly as the CLI does. **Never** collect, supply, or store keys — if
   ADC is not configured, ask the user to run `gcloud auth application-default
   login` themselves; do not work around it.

## Step 1 — Read the brand book and DECOMPOSE (agent-side, N4)

You (the agent) perform the decomposition — Mizan does not. Read the supplied
brand book / guidance doc(s) and extract:

- **Concerns / dimensions** the guidance cares about (e.g. tone-of-voice,
  clarity, inclusivity, factual accuracy, on-brand terminology).
- For each concern, a short **candidate criterion** phrased as an evaluable
  statement (e.g. "The response uses the brand's approved terminology and avoids
  banned phrases").
- One or more **sample prompts** that represent the kind of asset the rubric will
  score (this is what `rubric generate --sample` / `eval adaptive --prompt` needs
  — the generator drafts criteria *aligned to a representative prompt*).

Keep this decomposition transparent to the user: list the concerns and candidate
criteria you extracted before generating, so they can steer.

## Step 2 — SUGGEST FIRST: find existing templates to reuse

Before generating anything, look for templates already in the registry that cover
the same concerns, so you can suggest reuse or union instead of duplicating:

```bash
mizan registry list -o json
```

`registry list -o json` prints the stored templates to **stdout** (warnings go to
stderr). Reason over the parsed array — match on `Namespace`, `Kind` (prefer
`rubric`), `Tags`, `Name`/`Description` — and present a **suggestion list**:
"these existing templates already cover concern X; reuse or extend them." Note
there is **no `--tag` filter** on `registry list` (tag-filtered discovery is a
separate, out-of-scope capability); filter client-side over the JSON you parsed.
Let the user decide what to reuse vs. what to newly generate.

## Step 3 — GENERATE a draft rubric (one live call; needs ADC)

Feed a representative sample prompt and the target id to `rubric generate`. This
writes a **draft YAML** and writes **nothing** to the registry:

```bash
mizan rubric generate \
  --sample '<a representative prompt for the asset being scored>' \
  --id <namespace>/<slug> \
  --out <draft.yaml> \
  --recipe general_quality_v1 \
  --group-name brand
```

Real, verified flags (from `cmd/mizan/rubric.go`):

- `--sample <prompt>` — **required**; the representative prompt the generator
  aligns criteria to.
- `--id <namespace>/<slug>` — **required**; the draft template id.
- `--out <path>` — **required**; where the draft YAML is written.
- `--recipe <recipe>` — the predefined generation recipe. The confirmed values
  are `general_quality_v1` (**default**; prompt-aligned), `instruction_following_v1`
  (most prompt-alignment-focused), and `text_quality_v1` (more holistic/generic
  quality dimensions). Choose the recipe that best matches the brand book's
  emphasis. (An `MIZAN_ALLOW_CUSTOM_RECIPE=1` escape hatch exists for power users;
  do not use it unless the user explicitly asks.)
- `--group-name <name>` — the `RubricGroups` key for the output (defaults to the
  recipe family name). `--recipe` and `--group-name` are **independent** flags —
  the recipe selects *how* criteria are drafted, the group name only labels the
  output group.
- `--add-criterion "<criterion>"` — hand-authored criterion to **union** into the
  draft (repeatable; see Step 4).
- `--name <name>` — optional human-readable draft name.
- `--project` / `--location` — optional GCP target overrides for the generation
  call.

The draft it writes is a `kind: rubric` `MetricTemplate` manifest carrying a
`spec.rubricProvenance` block that records **how** the rubric was drafted. The
provenance shape (verified against `internal/registry` — `RubricProvenance` /
`RubricMeta`):

<!-- drift:rubricProvenance -->
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
      - group: brand
        criterion: The response uses approved brand terminology.
        type: STICKY
        importance: HIGH
        origin: adaptive-generated
```

Provenance field meanings:

- `method` — how the rubric was produced (`adaptive-generated`). **Do not rely on
  any other field name for this** — it is a cross-team contract key.
- `generatorModel` — the builtin generator model (`gemini-2.5-flash`).
- `recipe` — the recipe used (omitted if empty).
- `sampleInputRef` — a **bounded** reference to the sample: a length-capped
  single-line preview plus the SHA-256 of the full sample (the full prompt is
  pinned by hash for reproducibility, never dumped verbatim).
- `generatedAt` — generation timestamp.
- `apiVersion` — the RPC/surface that produced the rubric.
- `rubricMeta` — per-criterion metadata (`group`, `criterion`, `type`,
  `importance`, and `origin`), kept in declared order and aligned 1:1 with the
  criteria. `origin` is `adaptive-generated` for generated criteria and
  `hand-authored` for `--add-criterion` ones (see Step 4).

`rubric generate` prints the drafted criteria to **stdout** and a review-and-freeze
hint to **stderr**. **The draft file is not a valid import source on its own** and
`registry create` has no whole-file input — to bring the reviewed draft into the
registry you wrap it in a pack (Step 5).

## Step 4 — UNION with hand-authored criteria (CUJ9, union-before-freeze)

The brand book almost always contains rules the generator will not capture. Add
them as hand-authored criteria that are **unioned after** the generated ones,
appended in flag order:

```bash
mizan rubric generate \
  --sample '<representative prompt>' \
  --id <namespace>/<slug> \
  --out <draft.yaml> \
  --recipe general_quality_v1 --group-name brand \
  --add-criterion 'The response never uses the competitor names listed in the brand book.' \
  --add-criterion 'The response keeps sentences under the brand style-guide length limit.'
```

- `--add-criterion` is **repeatable**; criteria are appended **after** the
  generated criteria, in flag order. Conservative exact-after-normalization
  duplicates are dropped and reported to stderr.
- The union is recorded honestly in provenance: each `rubricMeta` entry's
  `origin` is `adaptive-generated` or `hand-authored`, so a reviewer can always
  tell which criteria came from the model and which the user added. This is the
  **union-before-freeze** the owner chose (CUJ9): draft = generated ∪
  hand-authored, then freeze the union.

**Review the draft with the user and edit `<draft.yaml>` directly** before
freezing — the draft is an *authoring aid*; once frozen it is an ordinary
reproducible static rubric. Editing criteria (or provenance) is expected.

### Alternative — immediate eval + freeze in one step (CUJ8)

When the user has a concrete prompt **and** a response to score *right now* and
wants to freeze the rubric that scored it (no separate draft-review pass), use the
immediate-eval-with-save path instead of Steps 3+5:

```bash
mizan eval adaptive \
  --prompt '<prompt>' --response '<response text>' \
  --recipe general_quality_v1 --group-name brand \
  --save-as <namespace>/<slug> -o json
```

`eval adaptive` generates criteria from the prompt (one live call), scores the
response against them (one live call), and — with `--save-as` — **freezes** the
generated rubric into the registry under `<namespace>/<slug>` as an ordinary
reproducible template. Without `--save-as` the generated rubric is held in memory
and never persisted. `-o json` prints the standard **`eval.Result`** object to
stdout (`Score`/`PairwiseChoice`, `Explanation`, `Stats`, warnings, …) — the same
shape `run-eval` documents; that contract is covered by the existing
`eval.Result` drift gate in `internal/skilldocs`, so it is referenced, not
re-documented here. `--save-as` persists the same `rubricProvenance` shown above
through the default store. Note `eval adaptive --save-as` cannot union hand-authored
criteria — for union-before-freeze use the `rubric generate --add-criterion` path
(Steps 3–5).

## Step 5 — VALIDATE, then FREEZE into the registry (creds-free)

A reviewed draft is frozen by wrapping it in a pack, validating it creds-free, and
importing it. All three steps are credential-free.

```bash
# 5a. scaffold a pack (writes mizan-pack.yaml + empty templates/ and evalsets/)
mizan pack init packs/<namespace> --name <namespace>

# 5b. drop the reviewed draft into the pack's templates/ dir
cp <draft.yaml> packs/<namespace>/templates/<slug>.yaml

# 5c. validate the pack — the creds-free CI gate (reads TEXT + EXIT CODE, not JSON)
mizan pack validate packs/<namespace>

# 5d. freeze: import the validated pack into the local registry
mizan registry import packs/<namespace> --strategy newer -o json
```

### `pack validate` — text + exit code (it ignores `-o json`)

`pack validate` runs the creds-free structural/semantic checks over every template
in the pack. **It does not emit `-o json`; passing `-o json` is silently ignored.**
Read the exit code and the text report (this contract is shared with the
`author-and-validate-a-template-pack` skill and covered by the hermetic
`pack validate` gate in `internal/skilldocs`):

- **Exit code `0` = accept.** No ERROR findings (warnings alone never fail). A
  clean pack prints `OK: no defects found.` and a `0 error(s), 0 warning(s)`
  summary.
- **Exit code non-zero (`1`) = reject.** At least one ERROR. Findings are grouped
  by file (`  [ERROR] <message>` / `  [warn ] <message>`) and end with a
  `N error(s), M warning(s)` summary. Fix each `[ERROR]` and re-run until exit `0`.

### `registry import` — the freeze; `-o json` reconciliation report

`registry import <src> --strategy newer` reconciles the pack's templates into the
local registry per the conflict matrix; `--strategy` is `newer` (default; take the
higher version, never clobber a dirty local edit), `skip`, `overwrite`, or `fork`.
With `-o json` it prints an import report object to **stdout**:

<!-- drift:registry-import -o json -->
```json
{
  "Source": { "Type": "local", "Origin": "packs/acme" },
  "Strategy": "newer",
  "DryRun": false,
  "Inserted": 1,
  "Updated": 0,
  "Skipped": 0,
  "Conflicted": 0,
  "Unchanged": 0,
  "Forked": 0,
  "Entries": [
    { "ID": "acme/brand-rubric", "Action": "inserted", "Reason": "" }
  ]
}
```

The per-action counts (`Inserted`/`Updated`/`Skipped`/`Conflicted`/`Unchanged`/
`Forked`) partition the processed templates (each increments exactly one), and
`Entries` lists each template with its `Action` and a human-readable `Reason`.
`--dry-run` previews the reconciliation without writing; re-importing an unchanged
pack is a no-op. After a successful import the frozen rubric is reproducible with
`mizan eval run --metric <namespace>/<slug>`.

## Reporting back to the user

Summarize, in order:

1. **Suggestions** — existing templates worth reusing (from Step 2), with why.
2. **Decomposition** — the concerns and candidate criteria you extracted.
3. **Draft** — the generated criteria, the hand-authored criteria unioned in, the
   recipe used, and the provenance (`method`, `recipe`, per-criterion `origin`).
4. **Freeze** — the `pack validate` result (accepted at exit `0`, or the
   `[ERROR]`s to fix) and the `registry import` reconciliation counts, plus the
   frozen template id and its `mizan eval run --metric <id>` rerun command.

On any failure, surface the CLI's stderr message verbatim and suggest the concrete
fix. Never fabricate a `mizan` subcommand — every command here exists in the CLI
today.

# Mizan — Collaboration / Contribution Design

Status: design for review (pre-implementation)
Date: 2026-08-09
Author: mizan-architect
Inputs: docs/research.md (ground truth), docs/architecture.md (draft), design/plan-v1.md,
        user decisions 2026-08-09 (brief mizan-architect.md)

> This document owns the **contribution layer** — the part the existing docs
> underweight. It defines the template-pack file format, pack layout, versioning,
> attribution, validation, the `mizan registry` / `mizan pack` CLI surface, the
> git-based PR workflow, and — most load-bearing — the **Store / codec / sync
> seam** that lets a future Firestore/GCS central registry (model B) drop in
> without reworking the CLI or GUI.

---

## 1. Problem & Goals

Mizan users author LLM-as-a-Judge **metric templates** locally. The user's
emphasis is that collaborators must be able to **contribute** templates to a
shared body of work and consume each other's. We are starting with **hybrid
model C**:

- **Local SQLite** is the working copy and the runtime index the eval engine
  reads from. Single-user, zero external infra, offline-capable.
- **Git-backed template packs** (versioned YAML files) are the contribution
  channel. Sharing is a `git` PR against a pack repo; consuming is an import.

### Goals

1. A file format for templates that reviews well in a git PR and round-trips
   losslessly to/from the local SQLite working copy.
2. A pack layout that minimizes merge conflicts when many contributors add
   templates independently.
3. Versioning + attribution + integrity so a consumer knows what they imported,
   from whom, and whether it changed.
4. A validation pipeline strong enough to be the **PR merge gate** (CI) and the
   local pre-import check.
5. A `Store` / `Codec` / `SyncBackend` seam such that **adding a Firestore/GCS
   central shared registry later requires no change to `cmd/mizan`,
   `cmd/mizan-desktop`, or the eval engine** — only a new backend implementation
   wired in at construction time.

### Success criteria

- A contributor can `mizan registry export` a local template into a pack repo,
  open a PR, have CI validate it, and after merge another user can
  `mizan registry import` it and run it — no code changes, no manual DB edits.
- Swapping the contribution channel from git-pack to Firestore is a
  constructor-wiring change plus one new package; grep of `cmd/` shows no
  git-specific or Firestore-specific symbols.

---

## 2. Non-Goals

- **Firestore/GCS central registry (model B) implementation.** Out of scope for
  now by user decision; this design only guarantees the *seam* it will plug into.
  It is listed in the phased plan as a later, conditional phase, not built here.
- **Live multi-user real-time sync / conflict-free merge (CRDT).** Git is the
  merge tool; conflicts are resolved by humans in PR review, as with code.
- **A hosted Mizan template marketplace / discovery service.** The pack repo(s)
  are ordinary git repos; discovery is "clone the repo(s) you trust."
- **Authentication / identity beyond git.** Authorship is asserted in metadata
  and corroborated by git commit history; we do not cryptographically sign
  templates in v1 (see Open Questions — signing is an additive later step).
- **Asset (media file) distribution.** Packs carry *templates* (and small
  example inputs by reference), not large media corpora. Large assets live in GCS
  and are referenced by `gs://` URI.

---

## 3. Proposed Design

### 3.1 Layered model — the load-bearing seam

The collaboration layer is three interfaces, deliberately separated so the
contribution channel is swappable independently of local persistence and of the
CLI/GUI:

```
        cmd/mizan (CLI)          cmd/mizan-desktop (Wails)
                \                     /
                 v                   v
             ┌───────────────────────────┐
             │   registry.Service        │   orchestration: import/export/CRUD
             │   (the ONLY type CLI/GUI   │   reconciliation, validation calls
             │    and eval engine touch)  │
             └───────────────────────────┘
                 |            |            \
                 v            v             v
           ┌─────────┐  ┌──────────┐  ┌──────────────────┐
           │  Store  │  │  Codec   │  │  SyncBackend     │
           │ (local  │  │ template │  │ (contribution    │
           │ persist)│  │  <-> bytes│  │  channel)        │
           └─────────┘  └──────────┘  └──────────────────┘
                 |            |               |
          SQLiteStore   YAMLCodec       GitPackBackend   <-- model C (now)
          (FirestoreStore) (JSONCodec)  (FirestoreBackend / GCSBackend) <-- model B (later)
```

**Why three interfaces, not one:**

- `Store` is *local runtime persistence* — what the eval engine reads at eval
  time. Fast, offline, indexed. SQLite now. **The literal requirement "Store
  interface must admit a future Firestore backend cleanly" is satisfied here:
  `FirestoreStore` is a drop-in `Store`.**
- `Codec` is *serialization* — `MetricTemplate <-> []byte`. YAML for authored
  git files now; a `JSONCodec` (or Firestore document mapping) reuses the same
  canonical model later with zero engine impact.
- `SyncBackend` is the *contribution channel* — where shared templates come from
  and go to. `GitPackBackend` (read/write pack files on disk) now;
  `FirestoreBackend` / `GCSBackend` (push/pull to a central store) later.

`registry.Service` is the single façade the CLI, GUI, and eval engine depend on.
Migrating to model B is: implement `FirestoreStore` and/or `FirestoreBackend`,
and change **one constructor** in `cmd/*/main.go`. No CLI command, no GUI
binding, no eval code changes. That is the whole point of the seam.

#### Interface stubs (illustration — not production code)

```go
// internal/registry/store.go
type Store interface {
    Get(ctx context.Context, id string) (*MetricTemplate, error)
    List(ctx context.Context, f ListFilter) ([]MetricTemplate, error)
    Put(ctx context.Context, t *MetricTemplate) error   // upsert by ID
    Delete(ctx context.Context, id string) error
    // sync-friendly primitive both SQLite and Firestore can serve cheaply:
    ListChangedSince(ctx context.Context, since time.Time) ([]MetricTemplate, error)
}

type ListFilter struct {
    Modalities []Modality
    Kinds      []MetricKind
    Namespace  string   // e.g. "google-brand"
    Source     string   // provenance filter (which pack/url it came from)
    DirtyOnly  bool      // locally-modified-since-import only
}

// internal/registry/codec.go
type Codec interface {
    Marshal(t *MetricTemplate) ([]byte, error)
    Unmarshal(data []byte) (*MetricTemplate, error)
    Ext() string // "yaml"
}

// internal/registry/sync.go  (the contribution channel)
type SyncBackend interface {
    // Load reads all templates a source currently offers (pack files on disk
    // now; a Firestore query later).
    Load(ctx context.Context) ([]MetricTemplate, error)
    // Save writes the given templates to the channel (pack files now; Firestore
    // docs later). For git-pack, Save writes files; the human does commit + PR.
    Save(ctx context.Context, ts []MetricTemplate) error
    Describe() SourceInfo // human-readable origin (path / repo / collection)
}

// internal/registry/service.go — the ONLY type CLI/GUI/eval depend on
type Service struct {
    store   Store
    codec   Codec
    // sync backends are supplied per-operation (you import from a specific
    // source), not held as singletons — a user imports from several packs.
}
func (s *Service) Import(ctx, src SyncBackend, opt ImportOptions) (ImportReport, error)
func (s *Service) Export(ctx, dst SyncBackend, sel Selector) (ExportReport, error)
func (s *Service) Get/List/Save/Delete(...)   // CRUD over the local Store
```

> **This is a real integration, not a stub.** SQLite, YAML, and git-pack are all
> implemented in the phases below against the schemas defined here. The
> Firestore backend is deliberately *absent* from the phases and named only in
> Non-Goals / Open Questions — a blocked backend is visible to everyone
> downstream; a faked one is not.

### 3.2 Template-pack file format — YAML, and why

**Decision: authored pack files are YAML** (`apiVersion` + `kind` +
`metadata` + `spec`, a k8s-familiar shape).

Justification (this is a load-bearing, hard-to-reverse choice, so the trade-off
is explicit):

- The dominant content of a template is **multi-line prose**:
  `metricPromptTemplate` and `systemInstruction` are frequently 5–40 lines. YAML
  block scalars (`|`) render these as literal, indented text that **diffs
  line-by-line in a PR**. The reviewer sees the actual prompt change.
- The same content in JSON collapses to a single `"...\n...\n..."` string with
  escaped newlines: a one-line diff that is unreadable in review. Since the
  entire value of the git channel is *human review of contributed prompts*, JSON
  defeats the purpose.
- YAML supports comments, so contributors can annotate rationale inline
  (stripped on import; not persisted).
- YAML is a superset of JSON — the *same* canonical `MetricTemplate` model is
  parsed either way, so a future `JSONCodec` (for a Firestore document shape or
  API wire format) is trivial and non-conflicting. **We are choosing YAML for
  the human-facing file, not for the internal model.**

Rejected alternative — JSON on disk: better tooling ubiquity and no
indentation-sensitivity footguns, but the unreadable-prompt-diff problem is
disqualifying for a review-centric workflow. We keep JSON as the *wire/document*
format for the future remote backend, where humans don't diff it.

Rejected alternative — TOML: good for flat config, poor for deeply nested
`responseSchema` and rubric maps; not established in the user's toolchain.

#### Format spec (`apiVersion: mizan.dev/v1alpha1`, `kind: MetricTemplate`)

```yaml
apiVersion: mizan.dev/v1alpha1     # format version — lets the format evolve
kind: MetricTemplate

metadata:
  id: google-brand/video-brand-alignment   # STABLE global id: <namespace>/<slug>
  name: Video Brand Alignment
  description: >
    Scores whether a short video ad aligns with a supplied brand guideline,
    covering tone, visual identity, and messaging.
  version: 1.2.0                    # semver; bump on any spec change
  authors:
    - name: Jane Doe
      email: jane@example.com
  maintainers:
    - google-brand-team
  license: Apache-2.0
  tags: [advertising, brand-safety, video]
  created: 2026-07-30T00:00:00Z
  updated: 2026-08-05T00:00:00Z
  # contentHash is COMPUTED (not authored) — see 3.4. Present in exports.

spec:
  kind: pointwise                  # pointwise | pairwise | rubric | custom_schema
  modalities: [video, text]        # asset types this template accepts

  # Declared inputs — placeholders the template references, with modality.
  # Validation cross-checks these against the template body (see 3.5).
  inputs:
    - name: response               # the asset under evaluation
      modality: video
      required: true
    - name: brand_guideline        # reference text
      modality: text
      required: true

  metricPromptTemplate: |          # {{placeholder}} substitution
    You are a brand compliance rater. Given a brand guideline and a video ad,
    rate how well the ad aligns with the guideline on a 1-5 scale.

    Brand guideline:
    {{brand_guideline}}

    Evaluate the video: {{response}}

    Consider tone, visual identity (logo, colors), and messaging consistency.

  systemInstruction: |
    Be strict. Penalize off-brand tone even when production quality is high.

  autorater:
    model: gemini-2.5-pro          # publisher model or tuned endpoint resource
    samplingCount: 4               # AutoraterConfig.SamplingCount (1-32)
    flipEnabled: false             # pairwise-only; ignored for pointwise

  # pairwise-only fields (present only when spec.kind == pairwise):
  # candidateFieldName: candidate
  # baselineFieldName: baseline

  # rubric-only field (present only when spec.kind == rubric):
  # rubricGroups:
  #   quality:
  #     - "Does the ad state the product name clearly?"
  #     - "Is the call-to-action unambiguous?"

  # custom_schema-only field (present only when spec.kind == custom_schema):
  # responseSchema: { ... JSON Schema ... }
```

The mapping from this file's `spec` to the runtime protos
(`PointwiseMetricSpec` / `PairwiseMetricSpec` / `LLMBasedMetricSpec` /
`AutoraterConfig`, or the `genai.GenerateContentConfig.ResponseSchema` for
`custom_schema`) is exactly the `MetricTemplate` domain type in
architecture.md §3 — the pack file is the on-disk projection of that struct.
See §6 for the reconciled struct.

> **Pending spike verdict (spike-core / Spike 1):** placeholder syntax is written
> as `{{placeholder}}` to match the proto field naming in research.md, but Spike 1
> must confirm whether the native API uses Go-template `{{x}}` or Python-style
> `{x}` substitution. The format spec adopts whatever Spike 1 records; if the API
> is `{x}`, the pack keeps authoring `{{x}}` and the codec/engine normalizes on
> materialization (one place to change), so this does not affect the file format.

### 3.3 Pack directory layout

A **template pack** is a directory (typically the root or a subdir of a git repo)
with a manifest and **one file per template**:

```
my-pack/                          # a git repo, or packs/<name>/ within one
  mizan-pack.yaml                 # pack manifest (see below)
  templates/
    video-brand-alignment.yaml    # one MetricTemplate per file
    image-safety.yaml
    text-helpfulness.yaml
  rubrics/                        # OPTIONAL: shared rubric groups, referenced by id
    brand-quality.yaml
  examples/                       # OPTIONAL: tiny sample inputs / gs:// refs, docs only
    video-brand-alignment.example.yaml
  README.md
  .github/workflows/validate.yaml # CI gate (see 3.7)
```

**One-file-per-template is deliberate:** two contributors adding different
templates touch different files and never merge-conflict. A single monolithic
`templates.yaml` would serialize all contributions through one file and one
conflict surface. The pack manifest does **not** enumerate templates (that would
re-introduce the conflict surface); the tool globs `templates/*.yaml`.

```yaml
# mizan-pack.yaml
apiVersion: mizan.dev/v1alpha1
kind: Pack
metadata:
  name: google-brand              # becomes the default namespace for its templates
  version: 3.4.0                  # pack-level semver (for humans / release tags)
  description: Brand-compliance judge templates for Google marketing assets.
  maintainers: [google-brand-team]
  license: Apache-2.0
spec:
  # minimum mizan format version required to consume this pack:
  requiresApiVersion: mizan.dev/v1alpha1
```

`metadata.name` is the pack's **namespace**; every template's `metadata.id` must
begin `"<pack-name>/"`. This gives cross-pack collision avoidance for free (two
packs can each have a `helpfulness` template without clobbering each other on
import) while keeping ids human-readable in the git tree.

### 3.4 Versioning & integrity

Three independent version axes, each with a distinct job:

| Axis | Field | Purpose | Bumped by |
|---|---|---|---|
| Format version | `apiVersion` | lets the file schema evolve | Mizan releases |
| Pack version | `mizan-pack.yaml metadata.version` | human release tag for the whole pack | maintainer |
| Template version | template `metadata.version` (semver) | drives import conflict resolution | contributor |

- **`contentHash`** (computed, not authored): SHA-256 over the canonicalized
  `spec` + identity fields, excluding `metadata.updated` and the hash itself.
  Written into exports and stored locally. Used to (a) detect drift ("this
  imported template was edited locally"), (b) short-circuit no-op imports, and
  (c) give the future remote backend a cheap change signal.
- Git history/blame is the **real provenance ledger** — who changed what, when,
  reviewed by whom. Metadata `authors`/`maintainers` are the *asserted*
  attribution; git corroborates it. We intentionally lean on git rather than
  reinventing an audit trail.

### 3.5 Validation pipeline (the PR gate)

`mizan pack validate <dir>` runs, in order, failing fast per template but
reporting all templates:

1. **Structural** — each `templates/*.yaml` validates against the published JSON
   Schema for `MetricTemplate` (`apiVersion`, `kind`, required `metadata`/`spec`
   fields, enum membership for `spec.kind`/`modalities`, types). The JSON Schema
   is shipped in-repo (`internal/registry/schema/`) and is the single source of
   structural truth.
2. **Identity** — `metadata.id` is `"<pack-name>/<slug>"`, slug is
   `[a-z0-9-]+`, unique within the pack; `version` parses as semver.
3. **Semantic (kind-specific)**:
   - `pairwise` requires `candidateFieldName` + `baselineFieldName`, both present
     in `inputs`.
   - `custom_schema` requires a valid `responseSchema` (valid JSON Schema); this
     is the only kind that routes to the `genai` fallback.
   - `rubric` requires non-empty `rubricGroups`.
   - `pointwise` forbids pairwise/rubric/custom fields.
4. **Placeholder consistency** — every `{{x}}` referenced in
   `metricPromptTemplate` / `systemInstruction` must be declared in
   `spec.inputs`; every `required` input must be referenced; each input's
   `modality` must be a member of `spec.modalities`.
5. **Lint (warnings, non-fatal)** — missing `description`; `samplingCount`
   outside 1–32; no `autorater.model` (will use configured default); `license`
   absent.
6. **Optional live dry-run** (`--dry-run`, opt-in, costs API + needs creds) —
   materializes the spec and issues one `EvaluateInstances` (or `genai`) call
   with a trivial synthetic input to confirm the spec is accepted by the API.
   **Not** run in CI by default (no creds / cost); available locally.

Steps 1–5 are pure and creds-free, so they run in CI as the merge gate.

### 3.6 CLI surface

Two command families. `mizan pack *` operates on pack files on disk; `mizan
registry *` operates on the local working copy (Store) and moves templates
between it and packs.

```
# Authoring / packaging (operate on pack directories)
mizan pack init <dir> --name <namespace>       # scaffold manifest + dirs + CI workflow
mizan pack validate <dir> [--dry-run]          # the PR gate; exit non-zero on error
mizan pack add <dir> --from <template-id>       # write a local template into a pack dir
                                                #   (thin convenience over registry export)

# Working-copy <-> pack movement
mizan registry export --out <pack-dir> \        # write local templates to pack files
    [--id <id> ...] [--namespace <ns>] [--all]
mizan registry import <src> \                    # src = local pack dir | file | git URL
    [--strategy newer|skip|overwrite|fork] \
    [--namespace-remap <old>=<new>] [--dry-run]

# Local working copy (CRUD — used by CLI and, via Service, by GUI)
mizan registry create|list|get|update|delete ... # (as in architecture.md §6)
```

- `import <git-url>` is a convenience: Mizan shells out to the user's `git` to
  clone/pull into a cache dir (`$XDG_CACHE_HOME/mizan/packs/<host>/<repo>`), then
  imports from the checkout. Mizan does **not** embed a git library; git
  operations are thin and delegated, so credentials/SSH config are the user's
  existing git setup. Primary/first-class input is a **local pack directory**;
  git-url is sugar over "clone then import from path."
- `--strategy` controls reconciliation (§3.8). Default `newer`.
- `--dry-run` on import prints the reconciliation report without writing.

### 3.7 Git-based PR contribution workflow

Canonical shared templates live in a git pack repo (e.g.
`github.com/ghchinoy/mizan-templates`, or a `packs/` tree inside the main repo —
see Open Questions). The round trip:

```
Contributor                                   Consumer
-----------                                   --------
mizan registry export --id X --out ./mizan-templates
git checkout -b add-X
git add templates/X.yaml && git commit
git push && gh pr create                       (waits for merge)
        │
        ▼
   CI runs `mizan pack validate .`  ── fails ──► PR blocked, author fixes
        │ passes
        ▼
   maintainer reviews prompt diff (readable, thanks to YAML) → merge
                                               │
                                               ▼
                              mizan registry import github.com/ghchinoy/mizan-templates
                              (or: git pull && mizan registry import ./mizan-templates)
                              → template X now in local SQLite, runnable
```

CI workflow shipped by `mizan pack init` (illustration):

```yaml
# .github/workflows/validate.yaml
name: validate-pack
on: { pull_request: { paths: ["templates/**", "mizan-pack.yaml", "rubrics/**"] } }
jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26.x' }
      - run: go install github.com/ghchinoy/mizan/cmd/mizan@latest
      - run: mizan pack validate .        # creds-free steps 1-5; exit != 0 fails PR
```

### 3.8 Import reconciliation (conflict resolution)

For each incoming template, keyed by `metadata.id`:

| Local state | Incoming vs local | `newer` (default) | `skip` | `overwrite` | `fork` |
|---|---|---|---|---|---|
| absent | — | insert | insert | insert | insert |
| present, hash equal | same content | no-op | no-op | no-op | no-op |
| present, incoming version > local | upgrade | update | skip | update | update |
| present, incoming version < local | older | skip (warn) | skip | update | skip |
| present, version equal, hash differs | conflict | **skip + report conflict** | skip | update | import as `<ns>-fork/<slug>` |
| present, locally `dirty` (edited since import) | any | **skip + warn** unless `overwrite` | skip | update | fork |

`fork` lets a consumer keep local edits while still pulling the upstream version
under a distinct namespace. The `dirty` flag (set when a user edits an imported
template locally) protects local work from being silently clobbered on re-import
— important once model B adds automated pulls.

Every import prints an `ImportReport`: inserted / updated / skipped / conflicted
counts with per-template reasons.

### 3.9 Provenance in the local Store

The SQLite `metric_templates` table (architecture.md §8) gains columns to make
the working copy sync-aware — these also serve the future Firestore backend:

- `source` TEXT — origin (`pack:google-brand@github.com/ghchinoy/mizan-templates`
  or `local`).
- `content_hash` TEXT — last-known hash (drift detection).
- `imported_at` TIMESTAMP, `updated_at` TIMESTAMP.
- `dirty` INTEGER (bool) — set when locally edited after import.

These are additive to the schema in architecture.md §8 and carry no meaning for a
purely local user, but they are exactly what a Store-to-Store sync (model B)
needs, so we pay for them once, now.

---

## 4. Alternatives Considered

### Contribution channel

- **Model A (pure git packs, no local DB).** The eval engine would read templates
  directly from checked-out files each run. Rejected: loses fast indexed
  lookup/listing, complicates the GUI, and there is nowhere to record local edits
  or provenance. Model C keeps A's sharing ergonomics but adds a real runtime
  store.
- **Model B now (Firestore/GCS central registry).** Real-time multi-user, but
  requires standing GCP infra, an auth/identity story, and offline degradation
  handling before any collaboration works at all. User explicitly deferred it.
  Our seam (§3.1) makes it a later drop-in rather than a rewrite.
- **Model C (chosen).** Local SQLite working copy + git-pack contribution. Zero
  new infra, developer-native review workflow, and a clean upgrade path to B.

### File format

- JSON on disk — rejected (unreadable multi-line prompt diffs; see §3.2).
- TOML — rejected (poor for nested `responseSchema`/rubrics; not in user's stack).
- YAML (chosen) — readable prompt diffs, comments, superset of JSON so the
  internal model and a future JSON/Firestore codec are unaffected.

### ID scheme

- Opaque UUID ids — rejected: collision-free but produce meaningless git
  filenames and unreviewable diffs.
- Bare slug (`helpfulness`) — rejected: collides across packs on import.
- **Namespaced slug `<pack>/<slug>` (chosen)** — collision-safe *and* readable.

### Pack file granularity

- One monolithic `templates.yaml` — rejected: single merge-conflict surface for
  all contributors.
- **One file per template (chosen)** — independent contributions never conflict.

There was a genuine single-obvious-choice on the **abstraction seam**: separating
`Store` (persistence) from `SyncBackend` (channel) is the only shape that
satisfies "add Firestore later without reworking CLI/GUI" *and* the literal
"Store must admit a Firestore backend" — so both a `FirestoreStore` (as primary
persistence) and a `FirestoreBackend` (as a remote channel over SQLite) are
admissible without touching consumers. We state that explicitly rather than
pretending we weighed a worse seam.

---

## 5. Migration / Rollout

The collaboration layer is **additive** to the P1 CLI MVP:

1. P1 ships local-only CRUD + eval against SQLite. No pack code exists yet.
2. P2 adds `internal/registry` codec + pack + sync packages and the
   `pack`/`registry import|export` commands. The P1 `Store` interface is
   unchanged; P2 only adds the provenance columns (a forward-compatible
   `ALTER TABLE` / new-table migration) and new command families.
3. Because CLI/GUI depend only on `registry.Service`, introducing P2 does not
   modify any P1 command implementation beyond wiring the new subcommands.
4. Model B, if/when approved, adds `FirestoreStore` and/or `FirestoreBackend`
   and changes one constructor. Existing local users are unaffected; they opt in
   by pointing config at a remote.

Schema migration is versioned (a `schema_version` row / pragma in SQLite);
P1→P2 is a single additive migration applied on first P2 launch.

---

## 6. Reconciled `MetricTemplate` (authoritative struct)

This supersedes architecture.md §3 by adding the fields the collaboration layer
requires. It is the single in-memory model the `Codec` (YAML) and future
Firestore document mapping both target.

```go
type MetricTemplate struct {
    // Identity / attribution
    ID          string     // "<namespace>/<slug>", stable, primary key
    Name        string
    Description string
    Version     string     // semver
    Authors     []Author
    Maintainers []string
    License     string
    Tags        []string

    // Behavior
    Kind                 MetricKind   // pointwise|pairwise|rubric|custom_schema
    Modalities           []Modality
    Inputs               []InputSpec  // declared placeholders + modality (NEW)
    MetricPromptTemplate string
    SystemInstruction    string
    CandidateFieldName   string       // pairwise
    BaselineFieldName    string       // pairwise
    RubricGroups         map[string][]string // rubric
    ResponseSchema       *Schema      // custom_schema only
    AutoraterModel       string
    SamplingCount        int32        // 1-32
    FlipEnabled          bool         // pairwise

    // Provenance / sync (NEW — see 3.9)
    Source      string
    ContentHash string
    Dirty       bool
    CreatedAt   time.Time
    UpdatedAt   time.Time
    ImportedAt  time.Time
}

type InputSpec struct {
    Name     string
    Modality Modality
    Required bool
}
type Author struct{ Name, Email string }
```

---

## 7. Open Questions

1. **Pack repo location** — a dedicated `ghchinoy/mizan-templates` repo, or a
   `packs/` tree inside `ghchinoy/mizan`? A separate repo keeps template review
   independent of code review and lets non-engineers contribute without touching
   the codebase; a monorepo is simpler to bootstrap. *Needs user decision.*
2. **Template signing** — do we need cryptographic signing/verification of
   contributed templates (supply-chain trust) beyond git history in a later
   phase? Additive; not v1. *Defer.*
3. **Placeholder syntax** — `{{x}}` vs `{x}` at the API. *Pending Spike 1
   verdict* (does not affect the file format; only the codec normalization).
4. **Rubric sharing granularity** — are shared `rubrics/` referenced across
   templates by id in v1, or inlined per template? Leaning inline-per-template
   for P2 simplicity, with `rubrics/` as a P2+ enhancement. *Confirm during P2.*

---

## 8. Acceptance Criteria (collaboration layer)

- YAML pack files matching §3.2/§3.3 round-trip through
  `registry export` → `registry import` with **no field loss** and a stable
  `contentHash` (export→import→export produces byte-identical files modulo
  computed `updated`).
- `mizan pack validate` fails (non-zero) on: unknown enum, missing kind-specific
  required field, a `{{placeholder}}` not declared in `inputs`, invalid semver,
  duplicate id within a pack. Passes on the §3.2 example.
- The §3.2 **video** example imports and `mizan eval run --metric
  google-brand/video-brand-alignment --field response=<gs-uri> --field
  brand_guideline=<text>` materializes a `PointwiseMetricSpec` +
  `ContentMapInstance` and returns a score (live path; pending eval-engine P1).
- Import reconciliation honors every row of the §3.8 table (unit-tested with a
  matrix of local/incoming states).
- A grep of `cmd/mizan` and `cmd/mizan-desktop` shows **no** SQLite-, YAML-, or
  git-specific symbols — only `registry.Service`. (This is the seam test: it
  proves model B is a drop-in.)
- CI workflow from `mizan pack init` runs steps 1–5 creds-free and blocks a PR
  that adds an invalid template.

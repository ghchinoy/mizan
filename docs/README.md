# Mizan — Documentation Index

**Project status: pre-implementation / design phase.** No code is built or
released yet; there is no installable CLI or desktop app. These documents are the
design record the implementation will be built against.

## Start here

1. **`research.md`** — the ground truth. Read this first: it is the researched
   basis (Vertex AI Gen AI Evaluation Service, API surface, precedents) that every
   other doc derives from. Do not re-derive its findings elsewhere.
2. **`architecture-final.md`** — the current architecture. This is the single
   source of truth for module layout, domain/engine design, config, dependencies,
   the CLI surface, and the desktop bindings.

## The documents

| Doc | Purpose | Status |
|---|---|---|
| `research.md` | Ground-truth research: eval service, API, modalities, precedents. | Reference (do not re-derive). |
| `architecture-final.md` | Current, authoritative architecture (rev 3). Module layout, domain model & engine, config, deps, CLI surface (§12), desktop bindings (§13). | Design for review (pre-implementation). |
| `collaboration-design.md` | The contribution layer: template-pack format, versioning, validation, the Store/codec/sync seam, and the authoritative `MetricTemplate` struct (§6). | Design for review (rev 3). |
| `implementation-plan.md` | Phasing (P1→P4) and acceptance criteria. | Design for review (pre-implementation). |
| `spikes.md` | De-risking spikes for the unknowns in `research.md` and `architecture-final.md`. | Working notes. |

## Reconciliation note (2026-08-09)

`architecture.md` (the 2026-08-07 draft) has been folded into
`architecture-final.md` and **removed**. Its unique, still-accurate content now
lives here:

- **Eval-layer domain types** (`Instance`, `AssetRef`, `Result`, `Engine`) →
  `architecture-final.md` **§6**.
- **CLI surface** (the `mizan …` command examples) → `architecture-final.md`
  **§12**, reconciled to the rev-3 command set.
- **Wails desktop bindings** (the `App` method signatures) →
  `architecture-final.md` **§13** (Phase 4).

Cross-references in the other docs that used to point into the draft have been
repointed to `architecture-final.md`.

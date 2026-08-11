# Mizan — Documentation Index

**Mizan is installable today as a working CLI.** Registry CRUD, config, and
all four metric kinds (pointwise, rubric, custom_schema, pairwise) —
including multimodal (image/audio/video/music) — are built and verified
end-to-end against real Vertex AI. Template packs/sharing, batch eval, and
the desktop app remain planned — see [`roadmap.md`](roadmap.md) (or the
[user guide](user-guide.md#coming-soon--roadmap)) for the current boundary.
The design documents below remain the architectural record;
`architecture-final.md` in particular is kept current as the single source of
truth even as code lands.

## Start here

1. **New to the CLI?** Read **`user-guide.md`** for install, configuration, and
   a full command walkthrough with real output.
1. **Want to know what you can judge?** Read
   **[`llm-as-judge-scenarios.md`](llm-as-judge-scenarios.md)** — a
   capability-first, goal-organized guide ("I want to score X / compare A vs B /
   grade against a rubric") mapping each LLM-as-a-Judge scenario to the Mizan
   template and command that achieves it.
2. **`research.md`** — the ground truth. It is the researched basis (Vertex AI
   Gen AI Evaluation Service, API surface, precedents) that every other doc
   derives from. Do not re-derive its findings elsewhere.
3. **`architecture-final.md`** — the current architecture. This is the single
   source of truth for module layout, domain/engine design, config, dependencies,
   the CLI surface, and the desktop bindings.

## The documents

| Doc | Purpose | Status |
|---|---|---|
| `user-guide.md` | End-user guide: install, configure, registry walkthrough, running evaluations, troubleshooting, roadmap. | Current — verified against the built CLI. |
| `llm-as-judge-scenarios.md` | Capability-first use-case guide: the LLM-as-a-Judge scenarios you can achieve (pointwise, pairwise, rubric overall + per-criterion `--rubric-detail`, custom_schema, multimodal, model selection, observability), each mapped to a template/command and grounded in code. | Current — code-grounded against `main`. |
| `testing-guide.md` | Hands-on, copy-pasteable recipes for exercising every metric kind (pointwise, rubric, custom_schema, pairwise) and multimodal, plus the pairwise placeholder contract and the `--flip-enabled` P1 limitation. | Current — verified against the built CLI. |
| `roadmap.md` | Planned capabilities that are not built yet: template packs/sharing, batch evaluation, desktop app. | Current — planned scope only. |
| `research.md` | Ground-truth research: eval service, API, modalities, precedents. | Reference (do not re-derive). |
| `architecture-final.md` | Current, authoritative architecture (rev 3). Module layout, domain model & engine, config, deps, CLI surface (§12), desktop bindings (§13). Now includes embedded architecture/sequence diagrams. | Current, updated as code lands. |
| `collaboration-design.md` | The contribution layer: template-pack format, versioning, validation, the Store/codec/sync seam, and the authoritative `MetricTemplate` struct (§6). | Design for review (rev 3) — not yet implemented. |
| `implementation-plan.md` | Phasing (P1→P4) and acceptance criteria. | Design for review — P1 vertical slice landed; P2-P4 not started. |
| `spikes.md` | De-risking spikes for the unknowns in `research.md` and `architecture-final.md`. | Working notes. |
| `diagrams/` | Graphviz sources (`.dot`) and rendered WebP images for the component-architecture and eval-sequence diagrams embedded in `architecture-final.md`. | Current. |
| `examples/` | Shipped fixture files referenced by copy-pasteable recipes in `testing-guide.md` (e.g. `compliance-schema.json` for the `custom_schema` recipe). | Current. |

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

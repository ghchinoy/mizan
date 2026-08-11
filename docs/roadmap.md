# Mizan Roadmap — planned capabilities (not built yet)

Everything on this page is **planned, not shipped**. None of these commands
exist in the built `mizan` binary; do not expect them to work. For what Mizan
can actually do today, see [**Works today**](../README.md#works-today) in the
root README and the [user guide](user-guide.md).

## Template packs and sharing

Planned commands: `mizan pack init|validate|add` and `mizan registry
import|export`. **No `pack` command and no `registry import|export` exist in
the built `mizan` binary yet.**

The intended model: packs are contributed via pull requests to the dedicated
[`github.com/ghchinoy/mizan-templates`](https://github.com/ghchinoy/mizan-templates)
repo (data + CI only, no Mizan application code); its `validate-packs` CI
workflow is already wired up and runs on every PR there, but it currently
fails for the same reason — the validation step invokes `mizan pack validate`,
which doesn't exist yet — so that gate goes green once the command ships.
Packs are then pulled in with `mizan registry import` (that repo is the
configured default source) once the command exists.

`mizan-templates` is not just a stub — it already holds a real worked example
pack (`packs/google-brand/`), its own pack-format docs, and an active CI gate
— but this repo's binary has no code path that talks to it yet.

The pack format, versioning, validation rules, and the sync seam this would
build on are specified in the proposed design (§3) of
[`collaboration-design.md`](collaboration-design.md), with the authoritative
`MetricTemplate` struct in its §6 — a design document, not a description of
shipped behavior.

## Batch evaluation

Planned: evaluating GCS-hosted datasets in bulk through the Vertex AI
`EvaluateDataset` API. Not built — today `mizan eval run` and `mizan eval
pairwise` evaluate a single instance per invocation.

## Desktop app

A Wails v2 desktop frontend (`cmd/mizan-desktop`) is design-stage scaffolding
only; there is no built or runnable desktop app. The intended bindings are
sketched in the Wails desktop bindings section (§13) of
[`architecture-final.md`](architecture-final.md).

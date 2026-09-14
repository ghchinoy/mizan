# Mizan Roadmap — planned capabilities (not built yet)

The capabilities below — batch evaluation and the desktop app — are
**planned, not shipped**; neither `eval batch` nor a runnable desktop app
exists in the built `mizan` binary. For what Mizan can actually do today, see
[**Works today**](../README.md#works-today) in the root README and the
[user guide](user-guide.md).

Template packs & sharing have **shipped** and are no longer on this page:
`mizan pack init|add|validate` and `mizan registry import|export` are built and
verified — see [**Works today**](../README.md#works-today) and the
[user guide](user-guide.md#coming-soon--roadmap).

## Batch evaluation

Planned: evaluating GCS-hosted datasets in bulk through the Vertex AI
`EvaluateDataset` API. Not built — today `mizan eval run` and `mizan eval
pairwise` evaluate a single instance per invocation.

## Desktop app

A Wails v2 desktop frontend (`cmd/mizan-desktop`) is design-stage scaffolding
only; there is no built or runnable desktop app. The intended bindings are
sketched in the Wails desktop bindings section (§13) of
[`architecture-final.md`](architecture-final.md).

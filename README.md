# Mizan

Mizan is a Go tool over the **Vertex AI Gen AI Evaluation Service** to create,
manage, share, and run Gemini "LLM-as-a-Judge" metric templates across all
modalities (text, image, audio, video, music). It is designed around one shared Go
core with two thin frontends: a Cobra CLI (the primary deliverable) and a later
Wails v2 desktop app.

## Status

**Phase 1 is complete: all four metric kinds and multimodal are shipped and
installable today.** The table below is honest about the boundary between
what runs now and what is still roadmap — Mizan never claims a capability it
hasn't actually shipped.

### Works today

- **Metric registry CRUD** — `mizan registry create|list|get|update|delete`,
  backed by a local, pure-Go SQLite store (no C toolchain required).
- **Config** — `mizan config show|set`, persisted to
  `<UserConfigDir>/mizan/.env`, with environment-variable overrides.
- **Pointwise evaluation** — `mizan eval run` calls the real Vertex AI
  `EvaluateInstances` API (region `us-central1` by default) and returns a live
  `Score` + `Explanation` for a `pointwise` metric template.
- **Rubric evaluation** — `registry create --kind rubric --rubric-group
  "name=criterion one;criterion two"` (or `--rubric-groups-file`) authors the
  rubric criteria; `eval run` returns a live `Score` + `Explanation`.
- **Custom-schema evaluation** — `registry create --kind custom_schema
  --response-schema '<json>'` (or `--response-schema-file`) authors the
  response schema; `eval run` returns live structured `CustomOutput` fields
  via the `genai.GenerateContent` path.
- **Pairwise evaluation** — `mizan eval pairwise --metric <id> --baseline
  key=value --candidate key=value` returns a live `Choice`
  (`BASELINE`/`CANDIDATE`/`TIE`) + `Explanation`.
- **Multimodal evaluation** (image/audio/video/music) — `eval run`/`eval
  pairwise` accept `--file key=/path` (auto-staged to your configured GCS
  staging bucket) or `--gcs key=gs://...` (pre-staged) for non-text fields.

See the [user guide](docs/user-guide.md) and
[testing guide](docs/testing-guide.md) for full walkthroughs with real,
live-verified command output.

### Roadmap (not built yet — do not expect these to work)

- **Template packs and sharing** (`mizan pack init|validate|add`,
  `registry import|export`) — no `pack` command and no `registry
  import|export` exist in the built `mizan` binary yet. The intended model:
  packs are contributed via pull requests to the dedicated
  [`github.com/ghchinoy/mizan-templates`](https://github.com/ghchinoy/mizan-templates)
  repo (data + CI only, no Mizan application code); its `validate-packs` CI
  workflow is already wired up and runs on every PR there, but it currently
  fails for the same reason — the validation step invokes `mizan pack
  validate`, which doesn't exist yet — so that gate goes green once the
  command ships. Packs are then pulled in with `mizan registry import`
  (that repo is the configured default source) once the command exists.
  `mizan-templates` is not just a stub — it already holds a real worked
  example pack (`packs/google-brand/`), its own pack-format docs, and an
  active CI gate — but this repo's binary has no code path that talks to it
  yet. Roadmap phase P2.
- **Batch evaluation** (`EvaluateDataset` over GCS-hosted datasets). Roadmap
  phase P3.
- **The Wails desktop app** (`cmd/mizan-desktop`) — design-stage scaffolding
  only; there is no built or runnable desktop app. Roadmap phase P4.

## Quickstart

Prerequisites:

- Go 1.26+ (only needed if building from source — `go install` fetches its own
  toolchain requirement automatically).
- A GCP project with the Vertex AI API enabled.
- Application Default Credentials available, e.g.:

  ```sh
  gcloud auth application-default login
  ```

Install (cgo-free — no C compiler needed, thanks to the pure-Go
`modernc.org/sqlite` driver):

```sh
go install github.com/ghchinoy/mizan/cmd/mizan@main
```

Point Mizan at your project:

```sh
mizan config set project-id <your-project-id>
mizan config show
```

Create a text pointwise metric and run it:

```sh
mizan registry create --id demo/conciseness --name "Conciseness" \
    --description "Scores how concise a response is" \
    --kind pointwise \
    --prompt "Rate how concise this response is from 0 (verbose) to 1 (concise). Response: {{response}}" \
    --model gemini-2.5-flash

mizan eval run --metric demo/conciseness --field response="The cat sat on the mat."
```

Example output (verified live against Vertex AI):

```
Score:        1
Explanation:  The response 'The cat sat on the mat.' is a very short, direct, and grammatically complete sentence that conveys its meaning with no superfluous words, making it maximally concise.
```

For the full walkthrough (registry lifecycle, interpreting results,
troubleshooting), see the [user guide](docs/user-guide.md).

## Architecture

`cmd/mizan` composes `registry.Service`, `eval.Engine`, and `config` through a
single `internal/wire` composition root; it never imports the SQLite driver,
sync/codec layers, or the Vertex AI proto types directly. See
[`docs/architecture-final.md`](docs/architecture-final.md) for the full design,
including this component diagram distinguishing implemented paths from
roadmap ones:

![Mizan component architecture: cmd/mizan through wire to registry.Service and eval.Engine, with implemented paths solid and roadmap paths dashed](docs/diagrams/component-architecture.webp)

## Documentation

- Start with the docs index: [`docs/README.md`](docs/README.md).
- New to the CLI? Read the [user guide](docs/user-guide.md).
- Current architecture (single source of truth):
  [`docs/architecture-final.md`](docs/architecture-final.md).

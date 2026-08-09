# Mizan

Mizan is a Go tool over the **Vertex AI Gen AI Evaluation Service** to create,
manage, share, and run Gemini "LLM-as-a-Judge" metric templates across all
modalities (text, image, audio, video, music). It is designed around one shared Go
core with two thin frontends: a Cobra CLI (the primary deliverable) and a later
Wails v2 desktop app.

## Status

**A working vertical slice is shipped and installable today.** The table below
is honest about the boundary between what runs now and what is still roadmap —
Mizan never claims a capability it hasn't actually shipped.

### Works today

- **Metric registry CRUD** — `mizan registry create|list|get|update|delete`,
  backed by a local, pure-Go SQLite store (no C toolchain required).
- **Config** — `mizan config show|set`, persisted to
  `<UserConfigDir>/mizan/.env`, with environment-variable overrides.
- **Text pointwise evaluation** — `mizan eval run` calls the real Vertex AI
  `EvaluateInstances` API (region `us-central1` by default) and returns a live
  `Score` + `Explanation` for a `pointwise` metric template whose fields are all
  plain text.

See the [user guide](docs/user_guide.md) for a full walkthrough with real
command output.

### Roadmap (not built yet — do not expect these to work)

- **Multimodal evaluation** (image/audio/video/music) — requires GCS staging on
  the native path; not wired into the CLI or engine yet. PR #9 (open, in
  review) adds this.
- **Pairwise evaluation** (`eval pairwise`, flip-bias mitigation) — `registry
  create --kind pairwise` accepts the flag today, but `eval run` against a
  pairwise template fails fast with `not implemented in P1 slice`; there is
  no engine implementation yet. PR #9 (open, in review) adds this.
- **Rubric-based metrics** — the evaluation engine actually supports these
  today (native path, integration-tested), but `registry create`/`update`
  has no flag to set the required `RubricGroups`, so a CLI-created template
  can't be run yet — see [`docs/testing-guide.md`](docs/testing-guide.md).
- **`custom_schema` metric execution** via the `genai` fallback path — same
  story as rubric: the engine path works and is tested, but there's no CLI
  flag to set `ResponseSchema` yet — see
  [`docs/testing-guide.md`](docs/testing-guide.md).
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
  yet.
- **Batch evaluation** (`EvaluateDataset` over GCS-hosted datasets).
- **The Wails desktop app** (`cmd/mizan-desktop`) — design-stage scaffolding
  only; there is no built or runnable desktop app.

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
troubleshooting), see the [user guide](docs/user_guide.md).

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
- New to the CLI? Read the [user guide](docs/user_guide.md).
- Current architecture (single source of truth):
  [`docs/architecture-final.md`](docs/architecture-final.md).

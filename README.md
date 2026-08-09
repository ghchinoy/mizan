# Mizan

Mizan is a Go tool over the **Vertex AI Gen AI Evaluation Service** to create,
manage, share, and run Gemini "LLM-as-a-Judge" metric templates across all
modalities (text, image, audio, video, music). It is designed around one shared Go
core with two thin frontends: a Cobra CLI (the primary deliverable) and a later
Wails v2 desktop app.

**Status: pre-implementation / design phase — not yet released; no installable build.**

There is no working CLI or desktop app yet. This repository currently holds the
design record only.

## Documentation

- Start with the docs index: [`docs/README.md`](docs/README.md).
- Current architecture (single source of truth):
  [`docs/architecture-final.md`](docs/architecture-final.md).

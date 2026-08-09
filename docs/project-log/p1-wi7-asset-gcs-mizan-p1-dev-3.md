# WI-P1-7 — Asset ingestion + GCS staging

**Agent:** mizan-p1-dev-3
**Branch:** `feat/p1-asset-gcs` (from `main`)
**PR:** https://github.com/ghchinoy/mizan/pull/5 (to `main`, **not merged** — eng-manager gates merge)
**Date:** 2026-08-09

> Note: brief specified `design/project-log/…`; the repo uses `docs/` (no `design/`
> dir exists), so the log lives at `docs/project-log/` to match the repo convention.

## Scope

The `internal/asset` package: detect MIME/modality for local files and stage local
non-text assets to GCS, returning a `gs://` URI. This is foundational for native
multimodal eval — native `EvaluateInstances` accepts `gs://` `FileData` ONLY; inline
bytes are silently dropped (spike-core, Spike 2). WI-P1-4 consumes this package to
wire multimodal eval, so the public interface is a contract implemented exactly.

`internal/eval` was **not** modified (WI-P1-4 owns it).

## Files

- `internal/asset/mime.go` — MIME hardening (rewrite of existing).
- `internal/asset/stager.go` — `Stager` interface + `StageInput`/`StageResult` + validation.
- `internal/asset/gcs.go` — `GCSStager` + `NewGCSStager` + real GCS uploader seam.
- `internal/asset/mime_test.go`, `stager_test.go` — unit tests (cgo-free/network-free).
- `internal/asset/gcs_integration_test.go` — `//go:build integration` live upload test.
- `go.mod`/`go.sum` — added `cloud.google.com/go/storage`.
- `.gitignore` — ignore repo-root `mizan`/`mizan-desktop` build outputs.

## MIME hardening

`DetectMIME(filename string, head []byte) string` now sniffs magic bytes **first**
(content wins over extension — a wrong top-level type silently drops the asset), then
falls back to a curated extension map, then `mime.TypeByExtension`, then
`application/octet-stream`.

Sniffed formats:
- image: jpeg (`FF D8 FF`), png (`\x89PNG…`), gif (`GIF8[79]a`), webp (`RIFF…WEBP`)
- audio: mp3 (`ID3` or frame sync `FF Ex`), wav (`RIFF…WAVE`), ogg (`OggS`),
  flac (`fLaC`), m4a (`ftyp M4A`)
- video: mp4 (`ftyp`), quicktime/mov (`ftyp qt`), webm (EBML `1A 45 DF A3`)

`normalizeMIME` canonicalizes aliases (`audio/wave`→`audio/wav`, `audio/mp3`→`audio/mpeg`,
…) and strips `; charset=` params. `http.DetectContentType` is a secondary fallback but
`text/plain` results are rejected so text-ish binaries don't mask a real media type.

New: `DetectMIMEFile(path string) (string, error)` reads the leading 512 bytes itself.
`ModalityForMIME` kept unchanged.

## Stager contract (implemented EXACTLY — WI-4 depends on it)

```go
type Stager interface {
    Stage(ctx context.Context, in StageInput) (StageResult, error)
}
type StageInput struct {
    LocalPath string // local file to upload (mutually exclusive with GCSUri)
    GCSUri    string // already gs://... -> returned as-is
    MIME      string // optional explicit override; else detected
}
type StageResult struct {
    GCSUri string // gs://bucket/object
    MIME   string // resolved top-level-accurate MIME
}
```

`GCSStager`:
- `NewGCSStager(ctx, bucket string) (*GCSStager, error)` — bucket is the bare name from
  `config.StagingBucket` (already `gs://`-stripped; constructor also strips defensively).
  Empty bucket → `ErrNoBucket` (a clear, `errors.Is`-matchable message telling the user
  to set `StagingBucket`). WI-4 surfaces this when multimodal eval runs with no bucket.
- `Stage`: `GCSUri` set → pass through as-is, MIME from override/extension. `LocalPath`
  set → detect MIME, upload to `gs://<bucket>/mizan-staging/<sha256(content)><ext>`,
  return the `gs://` URI. Content-addressing gives idempotent dedup (nice-to-have) and
  preserves the extension. Safe for concurrent use.
- Upload goes through an unexported `uploader` seam so unit tests exercise control flow
  without network/creds; production impl wraps `*storage.Client`.
- `Close()` releases the storage client.

## Verification (gates run)

| Gate | Result |
|---|---|
| `go build ./...` | ✅ pass |
| `go vet ./...` | ✅ pass |
| `go vet -tags integration ./internal/asset/` | ✅ pass (integration test compiles) |
| `gofmt -l internal/asset/` | ✅ clean (no output) |
| `CGO_ENABLED=0 go build ./cmd/mizan` | ✅ pass (cgo-free) |
| `go test ./...` (unit) | ✅ pass |
| import cycle check (`go list -deps ./internal/asset`) | ✅ imports `registry`, **not** `eval` |
| live integration upload | ✅ pass (below) |

## Live integration upload output

Command:
```
PROJECT_ID=ghchinoy-genai-sa go test -tags integration ./internal/asset/ -run Live -v
```
Output:
```
=== RUN   TestGCSStager_LiveUpload
    gcs_integration_test.go:63: staged: GCSUri=gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/3a779ccc19ad5eb129fa7855e4cecab22b23a1ecbbf042ee5b9aa8e6493a5f08.png MIME=image/png
    gcs_integration_test.go:85: object exists: size=38 contentType=image/png
    gcs_integration_test.go:93: cleaned up: deleted gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/3a779ccc19ad5eb129fa7855e4cecab22b23a1ecbbf042ee5b9aa8e6493a5f08.png
--- PASS: TestGCSStager_LiveUpload (0.20s)
PASS
ok  	github.com/ghchinoy/mizan/internal/asset	0.212s
```
Confirms: real upload to `gs://ghchinoy-genai-sa-mizan-staging` returns a `gs://` URI,
the object exists with `contentType=image/png`, and cleanup deletes it.

## Notes for WI-P1-4

- Inject `asset.Stager` into the eval Engine; call `Stage` per asset before building the
  native `FileData` part. Use the returned `StageResult.MIME` as the `FileData` MIME —
  it is the top-level-accurate, normalized value.
- Surface `asset.ErrNoBucket` (via `errors.Is`) to the user when a multimodal eval is
  attempted with no `StagingBucket`.
- Genai (custom_schema) path accepts inline bytes and does not need the Stager; keep the
  two converters distinct (arch §6).

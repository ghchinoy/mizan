# WI-P1-7 review-fix pass — `internal/asset` (PR #5, branch `feat/p1-asset-gcs`)

- **Author:** mizan-p1-fix-wi7 (developer)
- **Date:** 2026-08-09
- **Base:** `origin/feat/p1-asset-gcs` @ `c5923a6` (test-2 tests on dev-3's `0a5409c`)
- **New HEAD:** `e167125b07fb7292d5af97c119d078879fc73a53`
- **Toolchain:** go1.26.5, govulncheck v1.6.0, CGO checks with CGO_ENABLED=0
- **Scope:** `internal/asset/{gcs.go,stager.go,mime.go}` + tests, `go.mod`/`go.sum`.
  Did NOT touch `internal/eval` or `internal/config`. Did NOT merge.

Inputs read first: the audit (`p1-wi7-audit-mizan-p1-audit-2.md`), review
(`p1-wi7-review-mizan-p1-rev-3.md`), and test assessment
(`p1-wi7-test-mizan-p1-test-2.md`).

## Commits (on top of c5923a6)
1. `d8a9bfc` fix(deps): bump golang.org/x/text to v0.39.0 (GO-2026-5970)
2. `4a8b5e4` fix(asset): harden local upload path + MIME accuracy
3. `e167125` test(asset): cover MIME accuracy fixes + upload-path hardening

## Per-finding disposition

### [HIGH — MANDATORY] GO-2026-5970 x/text infinite-loop DoS — reachable — FIXED
`go get golang.org/x/text@v0.39.0 && go mod tidy`. The dep was reachable via
`GCSStager.Close → storage.Client.Close → norm.Form.*`. cgo-free preserved.
- **govulncheck BEFORE (c5923a6):** `reachable = 1` — Vulnerability #1 GO-2026-5970
  in `golang.org/x/text@v0.38.0`, 4 example traces through `asset.GCSStager.Close`.
  `Your code is affected by 1 vulnerability from 1 module.`
- **govulncheck AFTER (e167125):** `No vulnerabilities found. Your code is affected
  by 0 vulnerabilities.` (`reachable = 0`.) The remaining "1 vulnerability in
  modules you require" is not-called (unchanged from the audit baseline).

### [MEDIUM] Unbounded in-memory read of LocalPath (OOM/hang DoS) — FIXED
`gcs.go` `stageLocal`:
- `filepath.EvalSymlinks` resolves symlinks; `os.Lstat` + `Mode().IsRegular()`
  rejects dirs/devices/FIFOs/sockets (kills the `/dev/zero` read-forever case)
  before any read.
- `maxAssetBytes = 512 << 20` (512 MiB) named const with comment; `fi.Size()`
  over the cap returns a clear error.
- Upload now **streams** the file (`io.Copy` into the storage `Writer` via the
  `uploader` seam) instead of `os.ReadFile`; sniff uses a bounded `sniffLen`
  header and the sha256 is computed in a streaming pass, so the asset never
  fully lands in memory.
- Errors are `%w`-wrapped with the path.

### [MEDIUM] No path confinement on LocalPath (arbitrary-file read) — DOCUMENTED (defense-in-depth, not sandboxed) 
Per the brief, in P1 `LocalPath` is user-supplied CLI input: a CLI user can
legitimately reference any file they can read, so this is explicitly **not** a
sandbox and paths are not confined to a base dir. The `stageLocal` doc comment
states this trust boundary and frames the regular-file/size guards as
defense-in-depth / DoS protection for when WI-4 wires (possibly dataset-derived)
rows into `LocalPath`. Base-dir confinement is left to WI-4's dataset wiring
where the dataset root is known.

### [LOW] ftyp brand heuristic misclassifies audio-only MP4 as video — FIXED
`mime.go` `sniffMIME`: unambiguous audio brands `M4A`/`M4B`/`M4P` → `audio/mp4`;
a generic/ambiguous brand (`mp42`/`isom`/…) uses the extension as a tie-breaker
(`mp4AudioExt` = `.m4a/.m4b/.aac` → `audio/mp4`), else defaults to `video/mp4`.
Cross-type mismatch still lets content win (mp42 bytes named `.jpg` → `video/mp4`).

### [LOW] OggS always sniffed as audio/ogg — `.ogv` mis-typed — FIXED
`sniffMIME` honors a `.ogv` extension → `video/ogg` (else `audio/ogg`); added
`.ogv → video/ogg` to `extMIME` for the extension-only path. Documented that the
OggS container magic cannot distinguish audio from video, so `.ogv` opts in.

### [LOW] Object-key extension not sanitized — FIXED
`gcs.go` `safeExt`: the preserved object-key extension is whitelisted to
recognized media extensions (`extMIME` keys); unrecognized extensions are dropped
(the key core stays the content-addressed sha256). Keeps spaces/control
chars/newlines/unicode out of object names. `gs://` pass-through unchanged.

### [LOW] Untrusted MIME override can force a droppable top-level type — FIXED
`stager.go` `resolveMIME`: when content sniffing yields an unambiguous type whose
**top-level class** (image/audio/video) conflicts with the override, the sniffed
type wins (a wrong top-level silently drops the asset on the native path). An
inconclusive sniff still honors the override (operator-trusted hint). Choice
documented in the function comment.

### rev-3 non-blockers
- **[Nit] redundant "last resort" branch in `resolveMIME`** — FIXED (collapsed).
- **[Optional] whole-file buffered before upload** — FIXED (now streamed; see MEDIUM).
- **[Optional] concurrency not exercised** — already covered by
  `TestStageConcurrent` (test-2), still `-race` clean after these changes.
- **[FYI] text-under-media-ext / DetectMIMEFile drops non-EOF read error** —
  intentionally left as-is (documented intended bias; harmless for regular files).
- **[INFO] dedup weakened by ext in key** — left as-is (P1 correctness/cost note,
  not a defect).

## Tests added (`internal/asset/asset_fix_test.go`, cgo-free + network-free)
- `TestDetectMIME_FtypAudioBrandDisambiguation` — M4A/M4B, generic-brand±audio-ext,
  and cross-type mismatch cases.
- `TestDetectMIME_OggVideo` — `.ogv`→video/ogg (bytes + ext-only), audio default,
  cross-type mismatch.
- `TestStageLocalMIMEOverrideConflict` — sniff wins on top-level conflict; override
  honored on same top-level.
- `TestStageLocalRejectsNonRegularFile` — FIFO rejected before read.
- `TestStageLocalRejectsOversize` — sparse file over the cap rejected.
- `TestStageLocalResolvesSymlink` — symlink to a regular file staged successfully.
- `TestObjectKeyExtensionSanitized` — unrecognized ext dropped, recognized ext kept.

## Re-verification evidence (all from e167125)
- `go build ./...` — **PASS**
- `go vet ./...` — **PASS**; `go vet -tags integration ./internal/asset/` — **PASS**
- `gofmt -l internal/asset/` — **clean** (no output)
- `CGO_ENABLED=0 go build ./cmd/mizan` — **PASS** (cgo-free preserved)
- `import "C"` absent in `internal/asset` — confirmed cgo-free
- `internal/asset` does not import `internal/eval` — confirmed no cycle
- `CGO_ENABLED=0 go test ./... -count=1` (unit, cgo-free) — **PASS** (all packages)
- `go test ./... -race -count=1` (race needs cgo) — **PASS** (all packages)
- **Live upload:** `PROJECT_ID=ghchinoy-genai-sa go test -tags integration
  ./internal/asset/ -run Live -v` — **PASS**:
  ```
  staged: GCSUri=gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/3a779ccc...5f08.png MIME=image/png
  object exists: size=38 contentType=image/png
  cleaned up: deleted gs://ghchinoy-genai-sa-mizan-staging/mizan-staging/3a779ccc...5f08.png
  --- PASS: TestGCSStager_LiveUpload (0.28s)
  ```
  (confirms the new streaming-upload path works against real GCS + cleanup.)
- `govulncheck ./...` — **reachable = 0** (see HIGH above).

## Public contract — UNCHANGED (WI-4 depends on it)
`Stager` / `StageInput` / `StageResult` / `NewGCSStager` / `ErrNoBucket` /
`DetectMIME` / `DetectMIMEFile` / `ModalityForMIME` — all signatures unchanged.
Only private helpers changed (`sniffMIME` now takes the ext; `resolveMIME`/
`objectKey` internals; new `stageLocal`/`safeExt`/`topLevelType`).

## Note
Not merged (per brief — eng-manager's gate). Pushed to `feat/p1-asset-gcs` (PR #5).

<!-- headroom:rtk-instructions -->
# RTK (Rust Token Killer) - Token-Optimized Commands

When running shell commands, **always prefix with `rtk`**. This reduces context
usage by 60-90% with zero behavior change. If rtk has no filter for a command,
it passes through unchanged — so it is always safe to use.

## Key Commands
```bash
# Git (59-80% savings)
rtk git status          rtk git diff            rtk git log

# Files & Search (60-75% savings)
rtk ls <path>           rtk read <file>         rtk grep <pattern>
rtk find <pattern>      rtk diff <file>

# Test (90-99% savings) — shows failures only
rtk pytest tests/       rtk cargo test          rtk test <cmd>

# Build & Lint (80-90% savings) — shows errors only
rtk tsc                 rtk lint                rtk cargo build
rtk prettier --check    rtk mypy                rtk ruff check

# Analysis (70-90% savings)
rtk err <cmd>           rtk log <file>          rtk json <file>
rtk summary <cmd>       rtk deps                rtk env

# GitHub (26-87% savings)
rtk gh pr view <n>      rtk gh run list         rtk gh issue list

# Infrastructure (85% savings)
rtk docker ps           rtk kubectl get         rtk docker logs <c>

# Package managers (70-90% savings)
rtk pip list            rtk pnpm install        rtk npm run <script>
```

## Rules
- In command chains, prefix each segment: `rtk git add . && rtk git commit -m "msg"`
- For debugging, use raw command without rtk prefix
- `rtk proxy <cmd>` runs command without filtering but tracks usage
- If `rtk` returns `operation not permitted` inside the sandbox (e.g., after a harness restart), fall back to raw commands inside the sandbox rather than escalating read-only steps to `BypassSandbox: true`.
<!-- /headroom:rtk-instructions -->

## Mizan & Mizan-Templates Development Guidelines

### Repository Boundary: Code (`mizan`) vs. Template Packs (`mizan-templates`)
- **Never Create Top-Level `packs/` in `ghchinoy/mizan`**: `ghchinoy/mizan` contains the CLI, MCP server, desktop app, and evaluation/validation engine only. Canonical, shareable `MetricTemplate` and `EvalSet` packs live **exclusively** in [`github.com/ghchinoy/mizan-templates`](https://github.com/ghchinoy/mizan-templates) under `packs/<namespace>/` and must be opened as PRs there.
- **Where Pack Files Are Allowed in `ghchinoy/mizan`**: Only minimal, synthetic unit-test fixtures under `internal/registry/testdata/` belong in this repo (strip human `README.md` prose and use `t.Context()` in tests). Never place new fixtures directly under `internal/registry/testdata/packs/`, because `import_test.go` and `sync_test.go` assert exact single-template counts on `"testdata"` (`testdata/packs/google-brand`); instead isolate new synthetic pack trees under `internal/registry/testdata/<fixture>/packs/<ns>/`. Never couple Go tests in `internal/` to real packs via `../../packs/...`.
- **Split Mixed Contributions**: If a task spans both a new/updated template pack and CLI/validator/engine code (e.g., new `ValidatePack` checks), split them into separate PRs:
  1. Pack PR against `ghchinoy/mizan-templates` (place sample `compare-engines --dataset` JSONL files under `packs/<pack>/examples/` rather than `fixtures/`, and pin `gemini-3.5-flash-lite`, `gemini-4-hydrogen`, or `gemini-3.5-flash` — never retired `gemini-2.x`).
  2. Focused code/test PR(s) against `ghchinoy/mizan` (keeping validator contract changes separate from `testdata` coverage).

### CLI Binary & Environment
- **Binary Path**: Always use the repo-local binary `./bin/mizan` (rebuild via `rtk go build -o bin/mizan ./cmd/mizan`) rather than a global `mizan` on `$PATH`, which may lag behind recent commands such as `mizan pack`.
- **Vertex AI Project**: Set `MIZAN_PROJECT_ID=<your-gcp-project-id>` (or pass `--project <your-gcp-project-id>`) when running live `mizan eval run` or `mizan eval compare-engines` commands.
- **Secrets & `.env`**: Hugging Face credentials (`HF_TOKEN`) and local secrets live in `.env`. Ensure `.env` and `.env.*` remain in `.gitignore` across both `mizan` and `../mizan-templates`.

### Structured Evaluation Primitives (`boul`, `choice`, `score`) & Public Go SDK (`pkg/mizan`)
- **Default & Tiered Autorater Models**: Use `gemini-3.5-flash-lite` as the fast default `spec.autorater.model` for single-proposition `boul` and low-cardinality `choice` templates (`~800–1,080 ms` p50, `0` thinking tokens), **`gemini-4-hydrogen`** (superseding `gemini-3.8-flash`; Pareto-optimal with `--thinking-level low` / `WithThinkingLevel("low")`) for pairwise preference, nuanced instruction-following, prompt toxicity, Likert `score`, and high-cardinality `choice` templates (up to 255 static choices; runtime `Variations` capped at `MaxChoices = 32`), and `gemini-3.5-flash` for dense multimodal grounding (and `eval.BuiltinDefaultModel`). Never pin retired `gemini-2.x` models.
- **Vertex, Multi-Region (`global`, `us`, `eu`), & `R-GLOBAL` Routing**: `boul`, `choice`, and `score` templates use direct `google.golang.org/genai` structured JSON output and automatically upgrade default `us-central1` to `location=global` for `gemini-3.x` and `gemini-4-*` availability while preserving explicit `"us"` and `"eu"` multi-region locations (`{us|eu}-aiplatform.googleapis.com` on REST; `aiplatform.googleapis.com:443` with `locations/{us|eu}` on native gRPC `EvaluateInstances`). In `internal/eval/route.go`, keep `"gemini-4-"` (with trailing hyphen, e.g. `"gemini-4-hydrogen"`, `"gemini-4-argon"`) in `globalOnlyModelPrefixes` rather than `"gemini-4"`, because `global_route_test.go` and `global_route_kinds_test.go` reserve `"gemini-4.0-preview"` as an unlisted mock model to exercise regional-404 -> global self-correcting retries.
- **Multimodal Preamble Ordering & Dynamic `Choices`**: Structured `genai` calls emit labeled media preamble parts ahead of the rendered prompt and schema directive (`[LabelPart_1, MediaPart_1, ..., LabelPart_N, MediaPart_N, TextPart(renderedPrompt + directiveSuffix)]`) while preserving `len(Parts) == 1` on text-only runs. Runtime N-variation evaluation binds `mizan.Variations(...)` to a single placeholder and derives `KindChoice` enums via `WithChoicesFromVariations` or `WithChoices` without mutating the `Template` or `ContentHash`.
- **Public Go SDK (`github.com/ghchinoy/mizan/pkg/mizan`) Guardrails**:
  - `TestPkgMizanImportSeam` (`pkg/mizan/seam_test.go`) forbids transitive imports of `modernc.org/sqlite`, `cloud.google.com/go/storage`, `github.com/joho/godotenv`, `github.com/spf13/cobra`, `github.com/modelcontextprotocol/go-sdk`, and `github.com/ghchinoy/mizan/internal/{wire,config,mcpserver,sqliteutil}` (always qualify internal rules with the full module prefix so external subpackages such as `google.golang.org/genai/interactions/internal/config` do not false-match), forbids direct `os/exec`, and forbids exported `aiplatformpb` types.
  - `TestPublicTypeParity` (`pkg/mizan/parity_test.go`) enforces 1:1 exported field parity across `Result`, `Stats`, `TokenUsage`, `AppliedJudge`, `FinishError`, `EvalSetResult`, `MemberResult`, and `Aggregate`, plus 1:1 `MetricKind` and `Modality` (`text`, `image`, `audio`, `video`, `music`) constants.
- **Polarity Convention for Detectors**: For `boul` templates that detect a condition (e.g., toxicity, prompt injection, trajectory drift), define `passed: true` to mean *"the proposition holds / condition is detected"* so boolean verdicts align directly with dataset ground-truth labels.
- **Template Pack Workflow**: Validate packs in `../mizan-templates` with `./bin/mizan pack validate ../mizan-templates` and sync into the local SQLite registry with `./bin/mizan registry import ../mizan-templates/packs/<pack> --strategy overwrite`.

### Benchmarking & `jq` Post-Processing
- **`compare-engines` Output (schema v2)**: with `--dataset`, each engine is scored against the item's gold `expected` label first (`engine_a/engine_b.accuracy` + bootstrap CI, calibration, Likert MAE/Spearman), then paired (`paired`: McNemar, kappa, agreement). Inter-engine agreement is secondary: two wrong engines agree. Per-case engine errors are recorded (not fatal); re-run failed items with `scripts/judge-eval/retry_errors.sh` instead of dropping them (failures correlate with hard/long items).
- **Diffusion engine hygiene**: every diffusion result carries `readout_mode`; a non-envelope (free-form) reply is an error unless `--allow-diffusion-fallback`. Point `--diffusion-endpoint` at a structured server, a dgem gateway (`--diffusion-backend vertex|cloudrun` to pin), or a Vertex dedicated endpoint URL; auth is ADC (`diffusion-auth auto`). Never benchmark against raw vLLM.
- **No-model, Vertex-native kinds, & Autorater Qualification**: `kind: computation` (exact_match, bleu, rouge, tool_*, trajectory_*) runs locally with no credentials (`--engine vertex` for parity); `kind: prebuilt` runs Vertex predefined metrics with the service's own judge (the API rejects AutoraterConfig for them). Experiment 07 (`docs/experiments/07-judge-capability-rerun.md`) and Experiment 08 (`docs/experiments/08-gemini-4-hydrogen-spike.md`; repeatable 4-stage qualification harness at `scripts/judge-eval/qualify_model.py`, with runs in dated folders under `docs/experiments/judge-eval/runs/` and `backends.json` recording the serving version) are the current evidence base; Experiments 01–06 are superseded.
- **Safe `jq` Extraction**: Never use `(.passed | tostring) // .choice // (.score | tostring)` to extract predictions from `report.json` (`false` is falsy in `jq` and `null | tostring` evaluates to truthy `"null"`). Always use explicit type checks:
  ```jq
  if .passed != null then (.passed | tostring)
  elif .choice != null then .choice
  elif .score != null then (.score | tostring)
  else "ERROR" end
  ```
- **External API Verification**: Do not delegate live HTTP/API probes (such as Hugging Face Datasets Server or GitHub Release checks) to `research` subagents, as they lack `run_command` and `read_url_content` tools. Run `rtk curl` or `python3` probes directly.

### Code Health, Linting & Security Audits
- **`golangci-lint` Toolchain Parity**: If `golangci-lint` stalls after a `go.mod` `toolchain` upgrade (due to `GOCACHE` export-data mismatch with an older compiler), rebuild it under the active toolchain at the version pinned in `.github/workflows/ci.yml`:
  ```bash
  rtk go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
  rtk ~/go/bin/golangci-lint run ./...
  ```
- **Vulnerability Scanning & `docs-site` Builds**:
  - Go: `rtk go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` (keep version synced with `.github/workflows/ci.yml`).
  - Node (`docs-site/`): `rtk node ~/projects/agent-skills/plugins/repo-authoring/skills/node-security-audit/scripts/audit-repo.mjs .`
  - Sandboxed Astro build (`docs-site/`): pass `ASTRO_TELEMETRY_DISABLED=1 npm run build --prefix docs-site` so `@astrojs/telemetry` does not fail with `EPERM` attempting to write `~/Library/Preferences/astro/config.json`.
- **Go Readability & Security Guardrails (`~/projects/agent-skills`)**:
  - Prefer `any` over `interface{}`, `var s []T` over `s := []T{}`, and `cmp.Or` over custom `firstNonEmpty` helpers.
  - Do not end error strings with punctuation (`:` or `.`), as enforced by `revive` (`error-strings`).
  - **MCP & Asset Input Hygiene**: Validate `gs://` prefixes on GCS asset inputs (`internal/mcpserver/mapping.go`), enforce `EvalSymlinks` + `IsRegular()` + size caps on local file reads, and keep `DISABLE_AUTH=true` restricted to loopback addresses (`127.0.0.1`, `::1`, `localhost`) on `mizan mcp http`.
- **Python Linting (`ruff`)**: Run `rtk ruff check plugins/ scripts/` before committing Python changes (enforced in `.github/workflows/ci.yml` via `pyproject.toml`, with `E501` ignored for markdown table templates).
- **CLI Output, JSON Key-Set, & Doc-Drift Invariants**: `pack validate` and `config set` intentionally ignore `-o json` (`internal/skilldocs/drift_pack_test.go` and `drift_config_test.go`), and all human-readable CLI output must remain ANSI-free (`TestStripANSI`). `skilldocs` JSON key-set drift gates (`drift_test.go`, `drift_evalset_test.go`, `drift_results_test.go`) lock serialized JSON keys on `eval.Result`, `evalset.EvalSetResult`, and `results.Result`: tag SDK-only telemetry fields `json:"-"` on `internal/eval` structs and serialize them on `pkg/mizan` output structs instead. Note that `.github/workflows/ci.yml` `doc-drift` watches `pkg/mizan/*` alongside `cmd/mizan/*` and `internal/eval/*`.
- **Plugin & Marketplace Parity**: Keep `description` strings in `.claude-plugin/marketplace.json` byte-identical to `plugins/mizan-*/plugin.json`, and run `./scripts/validate-plugins.sh && ./scripts/test-validate-plugins.sh` after manifest or skill edits.
- **Release & Changelog Workflow**: CLI version is tag-driven (`vX.Y.Z` triggers `.github/workflows/release.yml`), while plugin versions live in `plugins/mizan-*/plugin.json`. Update `CHANGELOG.md` and push `main` before creating and pushing an annotated release tag (`git tag -a vX.Y.Z -m "Release vX.Y.Z"`).

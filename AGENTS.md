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
<!-- /headroom:rtk-instructions -->

## Mizan & Mizan-Templates Development Guidelines

### CLI Binary & Environment
- **Binary Path**: Always use the repo-local binary `./bin/mizan` (rebuild via `rtk go build -o bin/mizan ./cmd/mizan`) rather than a global `mizan` on `$PATH`, which may lag behind recent commands such as `mizan pack`.
- **Vertex AI Project**: Set `MIZAN_PROJECT_ID=<your-gcp-project-id>` (or pass `--project <your-gcp-project-id>`) when running live `mizan eval run` or `mizan eval compare-engines` commands.
- **Secrets & `.env`**: Hugging Face credentials (`HF_TOKEN`) and local secrets live in `.env`. Ensure `.env` and `.env.*` remain in `.gitignore` across both `mizan` and `../mizan-templates`.

### Structured Evaluation Primitives (`boul`, `choice`, `score`)
- **Default Model**: Use `gemini-3.5-flash-lite` as the default `spec.model.name` for `boul`, `choice`, and `score` templates unless a benchmark demonstrates a clear multi-class taxonomy advantage for `gemini-3.8-flash`.
- **Vertex Routing**: `boul`, `choice`, and `score` templates use direct `google.golang.org/genai` structured JSON output and automatically upgrade `us-central1` to `location=global` for `gemini-3.x` preview availability. Note that `autorater.samplingCount` is not applied on this path.
- **Polarity Convention for Detectors**: For `boul` templates that detect a condition (e.g., toxicity, prompt injection, trajectory drift), define `passed: true` to mean *"the proposition holds / condition is detected"* so boolean verdicts align directly with dataset ground-truth labels.
- **Template Pack Workflow**: Validate packs in `../mizan-templates` with `./bin/mizan pack validate ../mizan-templates` and sync into the local SQLite registry with `./bin/mizan pack import ../mizan-templates/packs/<pack> --strategy overwrite`.

### Benchmarking & `jq` Post-Processing
- **`compare-engines` Output**: `mizan eval compare-engines` computes inter-engine agreement (`overall_agreement`), while carrying ground-truth `expected` labels through to `report.json` untouched.
- **Safe `jq` Extraction**: Never use `(.passed | tostring) // .choice // (.score | tostring)` to extract predictions from `report.json` (`false` is falsy in `jq` and `null | tostring` evaluates to truthy `"null"`). Always use explicit type checks:
  ```jq
  if .passed != null then (.passed | tostring)
  elif .choice != null then .choice
  elif .score != null then (.score | tostring)
  else "ERROR" end
  ```
- **External API Verification**: Do not delegate live HTTP/API probes (such as Hugging Face Datasets Server or GitHub Release checks) to `research` subagents, as they lack `run_command` and `read_url_content` tools. Run `rtk curl` or `python3` probes directly.

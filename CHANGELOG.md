# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Added opt-in `--tie-break expected-grade` and credential-free `--from-report` re-scoring to `mizan eval compare-engines`, resolving discrete integer score/rubric ties via probability-weighted expected grades (`custom_output.expected_grade`) while keeping top-level `score` discrete (#120).
- Added `mizan eval calibrate-gate` for seeded K-fold cross-validated cascade escalation threshold calibration under selective-risk (`--target-risk`) and exact Paired McNemar parity (`--mcnemar-alpha`) constraints (#121).
- Documented `--tie-break expected-grade`, `--from-report`, and `mizan eval calibrate-gate` in [`docs/user-guide.md`](docs/user-guide.md).

## [0.3.2] - 2026-11-02

### Security
- Hardened [`IsVertexEndpointURL`](internal/eval/diffusion/client.go) and [`ResolveAuth`](internal/eval/diffusion/auth.go) to require `https://` and `*.prediction.vertexai.goog` / `*.googleapis.com` hostnames before selecting OAuth2 `cloud-platform` ADC access-token auth, preventing token exfiltration on arbitrary `/invoke` URLs.
- Added redirect protection in [`NewClient`](internal/eval/diffusion/client.go) to block HTTPS-to-HTTP downgrades and strip `Authorization` headers on cross-host redirects.
- Hardened [`ValidateModel`](internal/eval/model.go) and [`mcpserver`](internal/mcpserver/handlers.go) so full `projects/...` model resource overrides validate segment structure and cannot bypass `AllowedProjects`, and enforced `config.ValidateProjectID` / `config.ValidateLocation` at the MCP boundary.
- Fixed IPv6 listen address formatting in [`mcp http`](cmd/mizan/mcp_http.go) via `net.JoinHostPort`, added a 4 MiB request body limit via `http.MaxBytesHandler`, and pinned `HS256` with optional `MIZAN_MCP_JWT_AUDIENCE` / `MIZAN_MCP_JWT_ISSUER` checks in [`mcp_auth.go`](cmd/mizan/mcp_auth.go).

### Fixed
- Preserved `boul` boolean verdicts and `choice` selections in [`render_report.py`](plugins/mizan-results/skills/report-to-html/scripts/render_report.py) when `Outcome.Score` is absent.
- Serialized empty slices as `[]` rather than `null` in [`renderTemplateList`](cmd/mizan/helpers.go) under `-o json`.
- Recorded per-call `project` and `location` overrides in MCP result provenance (`mcpserver`).

### Added
- Added `--limit` to `mizan registry list` and `limit` to `mizan_list_metrics`.
- Added `--dry-run` and `-o json` support to `mizan registry delete`.
- Added root [`pyproject.toml`](pyproject.toml) and `ruff` CI lint gate in [`.github/workflows/ci.yml`](.github/workflows/ci.yml).

### Changed
- Raised the Go toolchain directive in [`go.mod`](go.mod) to `go 1.26.10` and upgraded Go and Node (`docs-site`) dependencies to latest patched releases.
- Synchronized plugin manifest descriptions in [`plugin.json`](plugins/mizan-authoring/plugin.json) with [`.claude-plugin/marketplace.json`](.claude-plugin/marketplace.json) and bumped `mizan-results` and `mizan-authoring` plugins to `0.1.1`.

## [0.3.1] - 2026-10-20

### Changed
- Updated built-in default autorater model and documentation prior to `v0.3.2`.

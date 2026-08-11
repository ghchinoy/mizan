# ITEM C fix pass — dev-itemc-2 (PR #36, feat/vernacular-aliases)

## Task
Clear the one remaining non-blocking finding from the test-engineer on PR #36:
in `registry list --kind`, the `--kind` filter value was normalized/validated
(`registry.NormalizeKind`) *after* the SQLite DB was opened. Reorder so validation
happens *before* the DB is opened, so an invalid `--kind` fails fast with the clear
enumerated error and does no DB work. No behavior change beyond ordering.

## Change
- `cmd/mizan/registry.go` (`newRegistryListCmd` RunE): moved the `--kind` normalization
  block (build `registry.ListFilter`, fold vernacular aliases via `NormalizeKind`) to
  run immediately after `mustConfig()` and **before** `wire.OpenService(cfg)`. The DB is
  now opened only once the filter value is known valid. Valid single/compare/pointwise/
  pairwise/rubric/custom_schema filters still filter identically; empty filter still
  lists all.
- `cmd/mizan/registry_kind_alias_e2e_test.go`: added
  `TestRegistryListKindInvalidFailsFastBeforeDBOpen`, which points `MIZAN_REGISTRY_DB`
  at an unopenable path (a regular file where the DB's parent dir would be, so
  `OpenService`'s `MkdirAll` fails) and asserts `registry list --kind bogus` still
  returns the enumerated `unknown metric kind` error — proving normalization runs before
  any DB work. Existing coverage untouched: `TestRegistryListKindUnknownErrors`,
  `TestRegistryListKindAliasFiltersSameAsCanonical`, `TestRegistryCreateWithKindAliasPersistsCanonical`,
  and `registry_kind_alias_test.go` all still pass.

Only two files changed; no goldens changed.

## Verification (all green, cgo-free / CGO_ENABLED=0)
- `go build ./...` — OK
- `go vet ./...` — OK
- `gofmt -l .` — empty
- `go test ./...` — all packages pass (incl. cmd/mizan e2e + unit alias tests)
- `golangci-lint run` — 0 issues
- `govulncheck ./...` (run via `go run golang.org/x/vuln/cmd/govulncheck@latest`) —
  No vulnerabilities found (0 called; 1 in a required module, not called — pre-existing,
  unrelated to this change).

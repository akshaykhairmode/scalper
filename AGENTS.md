# Agent Guidelines & Quality Gates for Scalper

All AI assistants and pair programming agents operating in this repository must follow these rules:

## 1. Mandatory Pre-Commit Gates
Before committing ANY code, you MUST run and verify that all of the following commands succeed with zero errors:

1. **`make lint`**:
   - Runs `gofmt` check, `golangci-lint run`, and `deadcode check`.
   - **Must report 0 issues.**
   - Common pitfalls to prevent:
     - Check error return values for all `io.Closer` (e.g. `defer func() { _ = resp.Body.Close() }()`).
     - Avoid package name stuttering on exported types (e.g., in package `scrip`, use `SearchResult` instead of `ScripSearchResult`, and `Query` instead of `ScripQuery`).
2. **`make test`**:
   - Runs the full test suite (`go test ./...`).
   - **Must pass 100% across all packages.**
3. **Frontend / Web Assets (if `web/` or templates are touched)**:
   - Run `npm --prefix web run typecheck` and `npm --prefix web run build`.
   - Pre-compress static assets: `go run ./scripts/compress_assets.go`.
   - Sync mobile bundle assets: `node mobile/scripts/sync-assets.js`.

**Never commit changes when `make lint` or `make test` fails.**

## 2. Engineering Standards Reference
- Follow all non-negotiables in [`GO_STANDARDS.md`](./GO_STANDARDS.md).
- Follow architectural specifications in [`docs/KOTAK_API_V3.md`](./docs/KOTAK_API_V3.md).
- Enforce strict broker rate-limiting optimizations (client debouncing, session-isolated server caching, local in-memory queries).

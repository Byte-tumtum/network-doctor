# Repository Guidelines

## Project Structure & Module Organization

`main.go` = CLI handling, JSON output, startup. Network probes, target parsing, fix hints, diagnosis logic → `internal/diagnostic/`. Bubble Tea interface, tool jobs, rendering, report copy/save → `internal/ui/`. Output sanitization shared by both layers isolated in `internal/textsafe/`. Tests co-located as `*_test.go`. Platform impls use build tags + suffixes `_linux.go`, `_darwin.go`, `_windows.go`, `_unix.go`, `_other.go`. Release + CI config in `.goreleaser.yaml` and `.github/workflows/`.

## Build, Test, and Development Commands

- `go build -o netdoc .` builds local executable.
- `go run . github.com:443` runs TUI against target, no install.
- `go run . --json github.com` exercises headless reporting path.
- `go test ./...` runs unit + subprocess tests.
- `go test -race ./...` checks concurrent probe + job code for races (CI runs on Linux).
- `go test -tags integration ./internal/diagnostic` runs offline-safe real-loopback socket checks.
- `go vet ./...` static checks required by CI.
- `golangci-lint run ./...` runs CI lint suite.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...` scans deps + reachable code for known vulns.
- `goreleaser check` validates release config, no publish.

Use Go version declared in `go.mod`.

## Coding Style & Naming Conventions

Run `gofmt -w` on changed Go files. Standard Go tabs + import grouping. Exported = `PascalCase`, internal helpers = `camelCase`, tests = `TestBehavior`. Keep OS behavior in build-tagged or platform-suffixed files; pass `GOOS` into testable tables where practical. Preserve probe dependency graph + bounded timeouts. Prefer command argument slices over shell strings.

## Testing Guidelines

Add focused tests beside changed package. Prefer table-driven tests for target parsing, diagnosis branches, platform commands. Real-socket tests must keep `integration` build tag, stay loopback-only. Ordinary tests deterministic + rootless. Before submit, run full gate from `README.md`: vet, build, unit + integration tests, race detection, sanitizer fuzzing, lint, vuln scanning, `goreleaser check`. CI runs normal gate on Linux, macOS, Windows; race + fuzz on Linux.

After changing `internal/textsafe`, fuzz sanitizer: `go test -fuzz=FuzzSanitize -fuzztime=10s ./internal/textsafe`. `internal/ui/jobs_test.go` uses `GO_HELPER` subprocesses to verify process-group cancellation.

## Cross-Platform Guidelines

After changing platform-tagged file, compile-check other targets: `GOOS=darwin go build ./...` and `GOOS=windows go build ./...`. Release builds use `CGO_ENABLED=0` — no cgo. Keep `internal/diagnostic` independent of `internal/ui`: network semantics in `diagnostic`, interaction + rendering in `ui`.

## Commit & Pull Request Guidelines

Recent commits use concise imperative subjects: `Cover portalCheck's real HTTP round trip`, `Floor per-attempt durations at 1ms too`. One behavior per commit. PRs explain user-visible effect, list validation commands, link issues, include screenshots or terminal captures for TUI layout changes. Call out platform-specific behavior + any untested OS explicitly.

Commit direct to `main`, no `Co-Authored-By` trailer. Keep `--help` on standard `fs.PrintDefaults` formatting. Preserve version injection through `-ldflags "-X main.version=..."` (local builds use `dev`). Release = tag commit already on `main`, push `vX.Y.Z`. GoReleaser publishes GitHub release, Homebrew cask, AUR package.

## Security & Configuration Tips

Keep probes unprivileged, time-bounded, safe for arbitrary host input. Sanitize external command output. Never interpolate targets into shell. No automatic privilege escalation or config rewrites.

## graphify

Project has knowledge graph at graphify-out/ with god nodes, community structure, cross-file relationships.

When user types `/graphify`, use installed graphify skill or instructions first.

Rules:
- Codebase questions: first run `graphify query "<question>"` when graphify-out/graph.json exists. `graphify path "<A>" "<B>"` for relationships, `graphify explain "<concept>"` for focused concepts. Return scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep.
- Dirty graphify-out/ files expected after hooks or incremental updates. Not reason to skip graphify. Skip only if task about stale/incorrect graph output, or user says not to.
- If graphify-out/wiki/index.md exists, use for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review, or when query/path/explain not enough context.
- After code changes, run `graphify update .` to keep graph current (AST-only, no API cost).
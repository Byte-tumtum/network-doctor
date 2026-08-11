# Repository Guidelines

## Build, Test, and Development Commands

Full validation gate: see "Tests" in `README.md`.

## Coding Style & Naming Conventions

Keep OS behavior in build-tagged or platform-suffixed files; pass `GOOS` into testable tables where practical. Preserve probe dependency graph + bounded timeouts. Prefer command argument slices over shell strings.

## Testing Guidelines

Real-socket tests must keep `integration` build tag, stay loopback-only. Ordinary tests deterministic + rootless.

After changing `internal/textsafe`, fuzz sanitizer: `go test -fuzz=FuzzSanitize -fuzztime=10s ./internal/textsafe`. After changing the encrypted-DNS response verifier, fuzz it: `go test -fuzz=FuzzEncryptedDNSResponseVerifier -fuzztime=10s ./internal/diagnostic`. `internal/ui/jobs_test.go` uses `GO_HELPER` subprocesses to verify process-group cancellation.

## Cross-Platform Guidelines

After changing platform-tagged file, compile-check other targets: `GOOS=darwin go build ./...` and `GOOS=windows go build ./...`. Release builds use `CGO_ENABLED=0` — no cgo. Keep `internal/diagnostic` independent of `internal/ui`: network semantics in `diagnostic`, interaction + rendering in `ui`.

## Commit & Pull Request Guidelines

One behavior per commit. PRs explain user-visible effect, list validation commands, link issues, include screenshots or terminal captures for TUI layout changes. Call out platform-specific behavior + any untested OS explicitly.

Commit direct to `main`. Keep commit subjects short, simple, and imperative. Do not use Conventional Commit type or scope prefixes (for example, `Add Star History chart`, never `docs: add Star History chart`, `feat: ...`, or `fix: ...`). Do not add a `Co-Authored-By` trailer. Keep `--help` on standard `fs.PrintDefaults` formatting. Preserve version injection through `-ldflags "-X main.version=..."` (local builds use `dev`). Release = tag commit already on `main`, push `vX.Y.Z`. GoReleaser publishes GitHub release, Homebrew cask.

## Security & Configuration Tips

Keep probes unprivileged, time-bounded, safe for arbitrary host input. Sanitize external command output. Never interpolate targets into shell. No automatic privilege escalation or config rewrites.

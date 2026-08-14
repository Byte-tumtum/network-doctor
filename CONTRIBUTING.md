# Contributing to Network Doctor

Thanks for helping make network troubleshooting less mysterious. Bug reports,
small fixes, platform-specific testing, and focused feature proposals are all
welcome. Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Before opening an issue

- Search existing issues first.
- Run `netdoc --version` and include the result.
- Include your OS, architecture, install method, target form, and the smallest
  sequence that reproduces the problem.
- Review reports and command output before posting. They may contain hostnames,
  IP addresses, usernames, interface names, or other sensitive network details.

Please use a GitHub Security Advisory or the contact in [SECURITY.md](SECURITY.md)
for vulnerabilities instead of a public issue.

## Development

Network Doctor requires the Go version declared in `go.mod`.

```sh
git clone https://github.com/heymaikol/network-doctor.git
cd network-doctor
go test ./...
go run . github.com
```

The code is split by responsibility:

- `main.go` owns CLI arguments, process I/O, and application startup.
- `internal/diagnostic` owns target parsing, native probes, per-OS route/SSID lookups, and verdict logic without depending on terminal presentation.
- `internal/ui` owns Bubble Tea state, rendering, and tool jobs.
- `internal/textsafe` sanitizes untrusted remote and subprocess text shared by both layers.
- `internal/simulation` + `cmd/netdoc-sim` build virtual networks to test the diagnosis engine against; nothing here ships in the `netdoc` binary.

The UI depends on diagnostics; diagnostics do not depend on the UI. Add network
semantics under `internal/diagnostic`, and interaction or rendering behavior
under `internal/ui`. The simulator depends on `internal/diagnostic` for probe
ids and target parsing, and on nothing in `internal/ui`.

Keep network semantics independent of the UI. Put OS-specific behavior in
build-tagged or platform-suffixed files, keep probes unprivileged and bounded,
and pass commands as argument slices rather than shell strings.

## Cleaning build output

Local builds and release dry runs leave generated files in the clone. Remove
them by name; the command is safe to repeat and safe when nothing is there:

```sh
rm -rf netdoc netdoc-sim network-doctor dist vendor network-doctor-*-vendor.tar.gz
```

`vendor/` matters most. GoReleaser's before-hooks write it and the matching
tarball into the repo root at release time, and a leftover `vendor/` silently
switches every later `go build` and `go test` in the clone to `-mod=vendor`,
resolving dependencies from that snapshot instead of `go.mod`. A stale `dist/`
also aborts the next GoReleaser run, which requires it empty.

Prefer the explicit list over `git clean -Xdf`: everything above is ignored,
but so are editor and tool settings such as `.claude/` and `.codex/`, which
that command would delete too.

## Validation

Start with the tests nearest your change, then run the complete validation gate
documented in the [README](README.md#tests) before submitting a pull request.

Additional requirements:

- Real-socket tests use the `integration` build tag and loopback only.
- When a man page's documented behavior or other material content changes,
  update its `.TH` date to that change's date; unrelated source edits do not.
- Changes to `internal/textsafe` require the sanitizer fuzz test shown in the
  validation gate.
- Platform-specific changes should compile for macOS and Windows with
  `GOOS=darwin go build ./...` and `GOOS=windows go build ./...`.
- Release builds use `CGO_ENABLED=0`; do not add cgo dependencies.

## Pull requests

Keep each pull request focused on one behavior. Describe the user-visible
effect, list the validation commands you ran, and call out platform-specific or
untested behavior. Include a screenshot or terminal capture for TUI layout
changes.

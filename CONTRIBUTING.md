# Contributing to Network Doctor

Thanks for helping make network troubleshooting less mysterious. Bug reports,
small fixes, platform-specific testing, and focused feature proposals are all
welcome.

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

- `main.go` owns CLI parsing, process I/O, and application startup.
- `internal/diagnostic` owns targets, probes, and verdict logic.
- `internal/ui` owns Bubble Tea interaction, rendering, and tool jobs.
- `internal/textsafe` sanitizes remote and subprocess output.

Keep network semantics independent of the UI. Put OS-specific behavior in
build-tagged or platform-suffixed files, keep probes unprivileged and bounded,
and pass commands as argument slices rather than shell strings.

## Validation

Start with the tests nearest your change, then run the complete validation gate
documented in the [README](README.md#tests) before submitting a pull request.

Additional requirements:

- Real-socket tests use the `integration` build tag and loopback only.
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

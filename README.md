# Network Doctor

[![CI](https://github.com/heymaikol/network-doctor/actions/workflows/ci.yml/badge.svg)](https://github.com/heymaikol/network-doctor/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/heymaikol/network-doctor)](https://github.com/heymaikol/network-doctor/releases/latest)
[![License: Apache-2.0](https://img.shields.io/github/license/heymaikol/network-doctor)](LICENSE)
[![Documentation](https://img.shields.io/badge/docs-heymaikol.github.io-1f6feb)](https://heymaikol.github.io/network-doctor/)

**Find exactly where your connection breaks.** Network Doctor is a
cross-platform network troubleshooting TUI that turns interface, DNS, TCP,
TLS, HTTP, proxy, and path-MTU checks into one plain-English diagnosis.

![Network Doctor diagnosing an office printer hostname that will not resolve: the DNS row fails, every check that depended on it is skipped, and the verdict names the missing DNS record as the fix](assets/hero.gif)

Instead of handing you a wall of `ping`, `dig`, and `curl` output, Network
Doctor answers the useful question: **is the problem on my network, along the
path, or at the service?**

## Why Network Doctor

- **Pinpoints the failed layer.** Independent probes distinguish local-link,
  DNS, egress, target, TLS, HTTP, proxy, and path-MTU failures.
- **Explains what to do next.** Results include evidence and targeted fix hints,
  with familiar drill-down tools one keypress away.
- **Needs no root access.** Even the path-MTU check and LAN map use unprivileged
  sockets and bounded probes.
- **Works interactively or in automation.** Use the TUI for live investigation,
  `--watch` for intermittent faults, or stable JSON and exit codes in scripts.
- **Runs everywhere.** The same diagnosis engine supports Linux, macOS, and
  Windows, with native packages and prebuilt binaries.

## Quick start

Install `netdoc` using the package for your platform below, then diagnose any
host or service:

```sh
netdoc github.com       # DNS → TCP → TLS → HTTP diagnosis
netdoc github.com:22    # SSH path and banner diagnosis
netdoc --watch host     # catch intermittent failures
netdoc --json host      # structured report for scripts or bug reports
```

Run `netdoc` with no target to check the local interface, internet egress,
configured proxy, public DNS, and Wi-Fi metadata. In the TUI, select a failed
row to see its evidence and suggested fix; press `?` for every shortcut.

## Install

Runs on **Linux, macOS, and Windows**. Project = `network-doctor`; installed binary = `netdoc`.

### Windows

Scoop, from own bucket:

```powershell
scoop bucket add heymaikol https://github.com/heymaikol/scoop-bucket
scoop install network-doctor
```

A release reaches the bucket as soon as it publishes, so `scoop update network-doctor` picks it up like any other app.

### macOS and Linux (Homebrew)

```sh
brew install network-doctor
```

The Homebrew Core formula, bottled for both platforms, so `brew upgrade` picks up releases like any other formula. It installs `netdoc` alone; for `netdoc-sim` too, take a [Linux package](#linux).

### Linux

Fedora: the [COPR repo](https://copr.fedorainfracloud.org/coprs/heymaikol/network-doctor/) builds from source, upgrades through `dnf` like any other repo:

```sh
sudo dnf copr enable heymaikol/network-doctor
sudo dnf install network-doctor
```

Covers Fedora 43, 44, and rawhide on `x86_64` and `aarch64`. COPR signs with its own per-project key (a separate trust root from the GitHub attestation below), which `dnf copr enable` installs for you.

Everything else: `.deb`, `.rpm`, and `.apk` packages are on the [latest release](https://github.com/heymaikol/network-doctor/releases/latest), for `amd64` and `arm64`. Download one and install it locally:

```sh
sudo apt install ./network-doctor_X.Y.Z_linux_amd64.deb    # Debian, Ubuntu, Mint
sudo dnf install ./network-doctor_X.Y.Z_linux_amd64.rpm    # Fedora, RHEL, Rocky, Alma
sudo apk add --allow-untrusted ./network-doctor_X.Y.Z_linux_amd64.apk    # Alpine
```

These don't auto-update the way COPR does, so `dnf`/`apt` won't pull the next version for you.

Every Linux package (COPR, `.deb`, `.rpm`, `.apk`) installs two commands at the same version: `netdoc`, and `netdoc-sim`, the simulator behind [Challenge Mode](#think-you-can-beat-network-doctor). Confirm both:

```sh
netdoc --version
netdoc-sim help
```

`netdoc-sim` is Linux-only: it builds its networks out of Linux namespaces, so the macOS and Windows downloads ship `netdoc` alone. Those hosts run the same simulator from [a container](docs/simulation.md#running-it-in-a-container) instead.

### Everywhere else

Grab a prebuilt binary from the [latest release](https://github.com/heymaikol/network-doctor/releases/latest) (Windows ships as a `.zip`, the rest as bare binaries), or install with Go 1.25+:

```sh
go install github.com/heymaikol/network-doctor@latest
```

(`go install` names the binary `network-doctor` after the module; rename it to `netdoc` if you like.) Check what you are running with `netdoc --version`.

Or build from clone:

```sh
git clone https://github.com/heymaikol/network-doctor
cd network-doctor
go build -o netdoc .
```

### Verify your download

Releases carry a signed attestation binding each artifact to the workflow run that built it (not available for v1.8.4 and earlier). With the GitHub CLI installed and `gh auth login` done:

```sh
VERSION=X.Y.Z
gh attestation verify "./netdoc_${VERSION}_linux_amd64" \
  --repo heymaikol/network-doctor \
  --signer-workflow heymaikol/network-doctor/.github/workflows/release.yml
```

This proves the bytes were built from the tagged commit by the release workflow. The source tarball, the `.deb`/`.rpm`/`.apk` packages, and the Windows `.zip` are attested too, so pass whichever filename you downloaded. The vendored-dependency tarball (`*-vendor.tar.gz`, which lets COPR build offline) is attested as well; COPR packages themselves are rebuilt on Fedora's own builders and carry COPR's signature instead.

## How it diagnoses

Probes form a **dependency graph with independent branches**, so an unrelated failure never hides a working one:

- **Direct-egress path** (independent of DNS): `Interface → Internet (TCP
  egress)`. Always runs, so "DNS down but internet up" stays diagnosable.
- **QUIC path**: `Interface → QUIC / UDP 443`, with its own real handshake so a UDP send alone can't masquerade as reachability.
- **Proxy-egress path** (independent of both): `Interface → Internet (env
  proxy)`, reported separately so a proxy-only network reads "online via proxy" rather than offline.
- **Public-DNS and encrypted-DNS paths** (independent of system DNS and of each other): a network can carry ordinary DNS while blocking DoH and DoT, or vice versa.
- **Wi-Fi metadata path**: SSID discovery runs beside network checks, so slow OS lookup never delays them.
- **Selected target path**: `Interface → DNS → TCP → TLS → HTTPS`, or the applicable protocol row for other ports.
- **Path-MTU branch** (hangs off connect, not off any protocol): black hole breaks SSH and SMTP exactly as thoroughly as TLS, and it's found without root or raw sockets.

Each row lands in one of five states: **✓ Pass**, **! Warn** (reachable but degraded), **✗ Fail**, **⊘ Skip** (a prerequisite failed), or **– N/A** (doesn't apply). Warn never counts as a failure.

The full probe table, with exact pass conditions, JSON causes, and how the unprivileged Path MTU check works, is in **[docs/reference.md](docs/reference.md#how-it-diagnoses)**. See the wiki's [How Network Doctor Works](https://github.com/heymaikol/network-doctor/wiki/How-Network-Doctor-Works) for why the branches are independent, and [Understanding Your Diagnosis](https://github.com/heymaikol/network-doctor/wiki/Understanding-Your-Diagnosis) for turning a row into a next action.

## Think you can beat Network Doctor?

Challenge Mode drops you into a deliberately broken network without telling you
what's wrong. Investigate it, commit to a diagnosis, then let Network Doctor take
a shot at the exact same problem, with both graded against the simulator's
independently observed ground truth.

There's a daily challenge, and everybody who plays that day gets the same
broken network:

```sh
netdoc-sim challenge -daily         # today's, the same one for everybody
netdoc-sim challenge                # draw one at random
netdoc-sim challenge -id V4-8F42C1  # replay the one a friend sent you
```

It ends with a result you can post. It names no fault, so it spoils nothing for
the next player, and `-daily` sends it to your clipboard for you (OSC 52, which
survives SSH and the container; if your terminal doesn't do it, the block is
printed anyway):

```
🩺 Network Doctor Challenge V4-8F42C1 (easy)
📅 Daily 2026-03-04
🧑 Me ✅   🤖 Network Doctor ❌
🏆 I beat Network Doctor in 3m 20s
🔁 Your turn: netdoc-sim challenge -id V4-8F42C1
```

On **macOS, Windows or Linux**, one container image is the whole install: the
real Linux namespace simulator inside a Linux container, not an imitation of it:

```sh
docker run --rm -it --cap-add SYS_ADMIN ghcr.io/heymaikol/netdoc-sim:latest challenge -daily
```

`podman run --rm -it` works too, without needing the added capability. On Linux,
any [package](#linux) installs `netdoc-sim` natively.

Everything is local and reproducible: no account, no server, no leaderboard, and
a challenge id is the whole puzzle, so the same id is the same broken network
on anyone's machine. See the wiki's
[Challenge Mode](https://github.com/heymaikol/network-doctor/wiki/Challenge-Mode)
guide for the full walkthrough, the daily challenge, and starter packs, and
**[docs/simulation-challenge.md](docs/simulation-challenge.md)** for the
contract behind scoring and why Network Doctor never gets to see the answer
either.

## Usage

```sh
netdoc                  # generic local + internet diagnosis
netdoc github.com       # diagnose the path to a host (→ HTTP + TLS + HTTPS)
netdoc github.com:22    # port selects the protocol rows (→ SSH banner)
netdoc https://host:80  # explicit scheme selects the protocol (→ TLS + HTTPS on :80)
netdoc ssh://host:2222  # explicit scheme keeps SSH on a nonstandard port
netdoc --json host      # headless: one JSON report on stdout (scripts, CI, bug reports)
netdoc --watch host     # TUI: re-run continuously and track intermittent failures
netdoc --json --watch host  # headless: one JSON report per line, until interrupted
netdoc --check dns,target_tcp,tls example.com  # run only these IDs and their prerequisites
netdoc --skip internet_tcp,quic_udp_443 example.com  # omit these probe branches
netdoc --iface wg0 host # bind probe traffic to wg0's source address
netdoc --public-dns 9.9.9.9 host  # take the second opinion from Quad9 instead
netdoc --no-history host          # don't read or save the target history file
```

`--timeout` overrides the per-check probe timeout. `--check`/`--skip` select probes by stable ID plus their dependency closure; `--iface` and address-only binding follow probe traffic through the drill-down tools too. Full flag semantics, the target-parsing rules, and the history file are in **[docs/reference.md](docs/reference.md#usage-details)**.

| Key | Action |
|-----|--------|
| `↑`/`↓` (`k`/`j`) | select a probe row, or a device in the network map |
| `a` | expand the checks a finished run collapsed (the passing rows, and the toolbox on a clean run), and collapse them again |
| `v` | run a LAN scan and show a network map of the local private `/24` (unprivileged `nmap`) |
| `enter` | set the selected map device as the new target, or open the current tool job's output |
| `/` (viewer) | filter the viewer to matching lines (`enter` commits, `esc` clears it, a second `esc` leaves) |
| `home`/`end`, `pgup`/`pgdn` (viewer) | jump to top/bottom (`end` re-enables follow) or page through the output |
| `y` / `w` (viewer) | copy / save the viewer's retained output (up to 5,000 lines; respects its filter) |
| `r` | restart with a new target |
| `S` | SSH login: a form for username, key, and password, then hands the terminal to `ssh` (hinted only once the SSH banner check passes, but usable against any target) |
| `tab` | switch between running tool jobs |
| `esc` | cancel the focused job only (`tab` picks which); `q` is the stop-everything path |
| `y` / `w` | yank / write (copy / save locally) a reviewable report of the chain plus every tool job |
| `?` | full-screen key cheatsheet; any key closes it |
| `q` | quit (cancels running jobs first, then exits) |

### Vim keybindings

```sh
netdoc --keys vim
```

Adds `gg`/`G` for first/last, `ctrl+b`/`ctrl+f` for page up/down, and `ctrl+u`/`ctrl+d` for half-page up/down. Existing keys continue to work.

## Drill-down tools

Each diagnosis row is *evidence*; when you want proof, run the real tools as cancellable streaming jobs: several run at once, `tab` switches between the live ones, and output is sanitized before it hits your terminal. Review your local copy before sharing, since tool evidence may contain sensitive data.

| Key | Linux | macOS | Windows |
|-----|-------|-------|---------|
| `i` | `ip route` | `netstat -rn` | `route print -4` |
| `s` | `ss -tunp` | `netstat -an -p tcp` | `netstat -ano` |
| `p` | `ping -c 4 -W 2` | `ping -c 4` | `ping -n 4 -w 2000` |
| `d` | `dig +time=2 +tries=1` | `dig +time=2 +tries=1` | `nslookup` |
| `c` | `curl` (protocol-aware: SSH/SMTP targets get a handshake probe instead) | same | `curl.exe` |
| `t` | `traceroute -w 2 -q 1 -m 20` | same | `tracert -w 2000 -h 20` |
| `m` | `mtr --report --report-cycles 5` | same (via brew) | `pathping -h 20 -q 5 -p 100 -w 500` |
| `n` | `nmap -sT -Pn --host-timeout 110s` | same | same |

`n` and `v` are gated behind an explicit confirmation before their active probes run. Full per-tool argument details, binding rules, and `--toolbox` are in **[docs/reference.md](docs/reference.md#drill-down-tools)**.

### SSH login

`S` logs in to the current target, the machine the checks are about. `tab` moves between fields, `←`/`→` picks the key, `enter` connects, `esc` backs out; anything left blank (passphrase, host-key check, 2FA) is asked by `ssh` itself on the real terminal.

```
╭────────────────────────────────────────────────────╮
│ SSH login to 192.168.1.50:2222                     │
│   Username  mplaczek                               │
│ ▸ Key       id_rsa  (3 of 4)  ←/→                  │
│   Password  *******                                │
╰────────────────────────────────────────────────────╯
```

The typed password never reaches argv or shell history; it's handed to `ssh` through `SSH_ASKPASS`. Full field mapping and the askpass/`ProxyJump` prompt-routing details are in **[docs/reference.md](docs/reference.md#ssh-login)**.

### JSON output

`--json` runs the same probe DAG headless and prints one JSON document to stdout:

```json
{
  "version": "1.2.3",
  "target": {"host": "github.com", "port": 443, "protocol": "tls+http"},
  "checks": [
    {"id": "dns", "name": "DNS github.com", "status": "PASS", "ms": 12, "detail": "github.com → 140.82.113.3", "addrs": ["140.82.113.3"]}
  ],
  "summary": "All checks passed. github.com:443 looks healthy.",
  "verdict": "ok",
  "ok": true
}
```

`status` is one of `PASS`, `WARN`, `FAIL`, `SKIP`, `N/A`. `verdict` answers the question a script actually asks:

| `verdict` | Meaning |
|---|---|
| `ok` | Every check passed |
| `degraded` | Everything asked for works, but some rung is impaired |
| `dns` | The name did not resolve |
| `network` | **The path is unavailable** |
| `service` | **The path works, the far end does not** |
| `incomplete` | A check has no result (the chain did not finish) |

Field names and the status vocabulary are stable, so they are safe to script against. The full field reference (`cause` values, `address_families`, `failed_stage`, `--json --watch` NDJSON) is in **[docs/reference.md](docs/reference.md#json-output)**.

### Exit codes

| Situation | Exit |
|---|---|
| Chain completed, no failed row (Skips allowed) | `0` |
| Any failed row | `1` |
| Quit before the chain finished | `1` |
| Bad arguments, validation reject, or no terminal for the TUI | `2` |

```sh
netdoc github.com || echo "path to github is broken"
```

## Platform support

All probes, the diagnosis engine, and the TUI are pure Go and identical on Linux, macOS, and Windows. Platform-specific garnish (the default gateway, the Wi-Fi SSID) degrades to empty rather than failing the probe when the OS lookup fails.

`netdoc-sim` and Challenge Mode are the exception: their backend is Linux namespaces and there is no other one, so macOS and Windows run [the published image](docs/simulation.md#running-it-in-a-container) on a Linux container runtime rather than a port. `netdoc` itself needs no container anywhere.

## Documentation

Everything explanatory is published at
**[heymaikol.github.io/network-doctor](https://heymaikol.github.io/network-doctor/)**:

- [Getting Started](https://heymaikol.github.io/network-doctor/wiki/Getting-Started/): install, first run, and what the screen is showing you.
- [Understanding Your Diagnosis](https://heymaikol.github.io/network-doctor/wiki/Understanding-Your-Diagnosis/): turning a verdict into a next action, including telling "my network" and "their service" apart.
- [How Network Doctor Works](https://heymaikol.github.io/network-doctor/wiki/How-Network-Doctor-Works/): why the probe branches are independent, and how path MTU is measured without root.
- [Troubleshooting and FAQ](https://heymaikol.github.io/network-doctor/wiki/Troubleshooting-and-FAQ/): the rows that behave surprisingly, and the questions that come up most.
- [Reference](https://heymaikol.github.io/network-doctor/docs/reference/) and the [simulator guide](https://heymaikol.github.io/network-doctor/docs/simulation/): the same `docs/` files that live beside the code.

The site is built from `docs/` and from the [wiki](https://github.com/heymaikol/network-doctor/wiki), so each page is still edited exactly where it lives; nothing is duplicated to publish it.

## Feature summary

Native DAG probes + diagnosis engine + two-pane UI, concurrent cancellable streaming tool jobs (`ping`/`dig`/`curl`/`traceroute`/`mtr`/`ss`/`ip`/`nmap`) + filterable output viewer + `--toolbox` mode, `Warn` state, proxy-aware diagnosis, unprivileged path-MTU check, public-DNS second opinion, LAN network map, `S` SSH login, source-interface pinning (`--iface`), probe selection (`--check`/`--skip`), `--watch` (TUI history strip and `--json` NDJSON), `--json` output, report copy/save.

## Built with

[Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles), and
[Lip Gloss](https://github.com/charmbracelet/lipgloss).

## Contributing

Bug reports, focused pull requests, and platform testing are welcome. See
[CONTRIBUTING.md](CONTRIBUTING.md) for setup, validation, and reporting guidance.
Please report suspected vulnerabilities privately as described in
[SECURITY.md](SECURITY.md).

## Support

Network Doctor is free software maintained independently. If it saves you time,
you can [sponsor its development](https://github.com/sponsors/heymaikol). Your
support helps fund the time spent on cross-platform testing, packaging, releases,
and ongoing maintenance. Sponsorship is optional and does not affect access to
the software or how issues are prioritized.

## Tests

Before submitting a change, run the complete CI gate. Every tool runs through `go run` at the version CI uses, so a Go toolchain is the only prerequisite:

```sh
go vet ./...
CGO_ENABLED=0 go build ./...
go test ./...
go test -tags integration ./internal/diagnostic ./internal/simulation
go test -tags netns_integration -count=1 -v ./internal/simulation
go test -race ./...
go test -race -tags integration ./internal/diagnostic ./internal/simulation
go test -fuzz=FuzzSanitize -fuzztime=10s ./internal/textsafe
go test -fuzz=FuzzEncryptedDNSResponseVerifier -fuzztime=10s ./internal/diagnostic
go test -fuzz=FuzzParseTarget -fuzztime=10s ./internal/diagnostic
go test -fuzz=FuzzGenerateHuntCase -fuzztime=10s ./internal/simulation
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/goreleaser/goreleaser/v2@v2.17.1 check
```

If the change touched the `Dockerfile` or the image's release job, also build the
image and test the artifact. It needs Docker or Podman, which is why it is not in
the gate above:

```sh
docker build --build-arg VERSION=dev -t netdoc-sim:test .
NETDOC_CONTAINER_IMAGE=netdoc-sim:test go test -tags container -count=1 -v .
```

If the change touched `docs/`, `site/`, or `cmd/docsite`, also build the
documentation site the way [the pages workflow](.github/workflows/pages.yml)
does. It needs the wiki checkout and the same container image GitHub Pages
builds with, which is why it is not in the gate above:

```sh
git clone --depth 1 https://github.com/heymaikol/network-doctor.wiki.git ../network-doctor.wiki
go run ./cmd/docsite -wiki ../network-doctor.wiki -out _docsite
docker run --rm -v "$PWD":/gh -e GITHUB_WORKSPACE=/gh \
  -e INPUT_SOURCE=_docsite -e INPUT_DESTINATION=_site \
  -e GITHUB_REPOSITORY=heymaikol/network-doctor \
  ghcr.io/actions/jekyll-build-pages:v1.0.13
go run ./cmd/docsite -verify _site
```

If the change touched a build-tagged or `_linux`/`_darwin`/`_windows` suffixed
file, also compile for macOS and Windows:

```sh
GOOS=darwin go build ./...
GOOS=windows go build ./...
```

Race, fuzz, and network-namespace checks run only on Linux in CI. The
`netns_integration` tests skip themselves on a host without unprivileged user
namespaces; they never need root. That gate keeps `-v` because a skipped run and
a real one both print just `ok` otherwise, and `-count=1` because a cached
result would not have exercised any namespace at all.

## Testing Network Doctor against broken networks

`netdoc-sim` builds a throwaway virtual network from a YAML scenario, breaks it
on purpose, runs the real netdoc binary inside it, and grades the diagnosis
against the injected fault. It is an unprivileged, Linux-only development tool for
deterministic regression testing that never touches the host network.

```sh
netdoc-sim scenarios
netdoc-sim run broken-dns
```

`netdoc-sim scenarios` is the source of truth for the complete set of shipped
built-in scenarios rather than the README. See the **[complete simulator
guide](docs/simulation.md)** for setup, scenario authoring, campaigns, hunts,
triage, and tests, or the wiki's [Simulator
Overview](https://github.com/heymaikol/network-doctor/wiki/Simulator-Overview)
for an orientation.

The simulator is also what [Challenge Mode](#think-you-can-beat-network-doctor)
runs on: the same virtual network, with the fault hidden from you instead of
named for you.

## Development

The package layout and the dependency rules between the packages are documented
in [CONTRIBUTING.md](CONTRIBUTING.md#development).

## License

Network Doctor is licensed under the [Apache License, Version 2.0](LICENSE). Package metadata declares this as `Apache-2.0`.

## Star History

<a href="https://www.star-history.com/?repos=heymaikol%2Fnetwork-doctor&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=heymaikol/network-doctor&type=date&theme=dark&legend=top-left&sealed_token=4XgBnUitKav8JRmYTBIst1x9bwnwAJEe_qDlPb20W2iSTPj_FG9cXicHok2d59GSb9QcFWynwWwexSj1vBNPTojS13SGdu0UUhNb9dx930Yaj-93UZ9oVw" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=heymaikol/network-doctor&type=date&legend=top-left&sealed_token=4XgBnUitKav8JRmYTBIst1x9bwnwAJEe_qDlPb20W2iSTPj_FG9cXicHok2d59GSb9QcFWynwWwexSj1vBNPTojS13SGdu0UUhNb9dx930Yaj-93UZ9oVw" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=heymaikol/network-doctor&type=date&legend=top-left&sealed_token=4XgBnUitKav8JRmYTBIst1x9bwnwAJEe_qDlPb20W2iSTPj_FG9cXicHok2d59GSb9QcFWynwWwexSj1vBNPTojS13SGdu0UUhNb9dx930Yaj-93UZ9oVw" />
 </picture>
</a>

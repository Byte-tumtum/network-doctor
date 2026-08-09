# Network Doctor

[![CI](https://github.com/heymaikol/network-doctor/actions/workflows/ci.yml/badge.svg)](https://github.com/heymaikol/network-doctor/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/heymaikol/network-doctor)](https://github.com/heymaikol/network-doctor/releases/latest)
[![License: GPL-3.0-or-later](https://img.shields.io/github/license/heymaikol/network-doctor)](LICENSE)

**Find exactly where your connection breaks.** Network Doctor is a
cross-platform network troubleshooting TUI that turns interface, DNS, TCP,
TLS, HTTP, proxy, and path-MTU checks into one plain-English diagnosis.

![Network Doctor diagnosing github.com:443: the check list, a traceroute and mtr running concurrently, the filtered output viewer, a LAN scan, the SSH login form, and watch mode](assets/demo.gif)

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

Or winget:

```powershell
winget install heymaikol.NetworkDoctor
```

A release reaches the Scoop bucket right away; winget lands whenever Microsoft merges the manifest PR, so it can trail a version behind.

### macOS (Homebrew)

The binary is unsigned, so the cask strips the quarantine attribute and Gatekeeper does not prompt. That removes a warning rather than adding a check — verification is a separate step you run yourself ([Verify your download](#verify-your-download)):

```sh
brew tap heymaikol/tap
brew install --cask network-doctor
```

### Linux

Fedora — [COPR repo](https://copr.fedorainfracloud.org/coprs/heymaikol/network-doctor/) builds from source, upgrades through `dnf` like any other repo:

```sh
sudo dnf copr enable heymaikol/network-doctor
sudo dnf install network-doctor
```

Covers Fedora 43, 44, and rawhide on `x86_64` and `aarch64`. COPR signs with its own per-project key, which `dnf copr enable` installs — a separate trust root from the GitHub attestation below, not the same one.

Everything else — `.deb`, `.rpm`, and `.apk` packages are on the [latest release](https://github.com/heymaikol/network-doctor/releases/latest), for `amd64` and `arm64`. Download one and install it locally:

```sh
sudo apt install ./network-doctor_X.Y.Z_linux_amd64.deb    # Debian, Ubuntu, Mint
sudo dnf install ./network-doctor_X.Y.Z_linux_amd64.rpm    # Fedora, RHEL, Rocky, Alma
sudo apk add --allow-untrusted ./network-doctor_X.Y.Z_linux_amd64.apk    # Alpine
```

Because these are downloaded by hand, they do not auto-update — `dnf`/`apt` won't pull the next version for you. COPR does.

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

Releases carry a signed attestation binding each artifact to the workflow run that built it. v1.8.4 and earlier were published before the release workflow attested anything, so they have none. With the GitHub CLI installed and `gh auth login` done:

```sh
VERSION=X.Y.Z
gh attestation verify "./netdoc_${VERSION}_linux_amd64" \
  --repo heymaikol/network-doctor \
  --signer-workflow heymaikol/network-doctor/.github/workflows/release.yml
```

This proves the bytes were built from the tagged commit by the release workflow, which gates the release on CI and an ancestor-of-`main` check. The source tarball is attested too, as are the `.deb`/`.rpm`/`.apk` packages and the Windows `.zip` — pass whichever filename you downloaded.

`*-vendor.tar.gz` (the vendored Go dependencies, which exist so COPR can build offline) is attested as well — it is what COPR compiles, and `-mod=vendor` never checks `go.sum`, so the COPR job verifies it against this attestation before it builds anything. One exception: COPR packages get rebuilt on Fedora's builders, so COPR's signature covers them, not this attestation.

## How it diagnoses

Probes form a **dependency graph with independent branches**, so an unrelated failure never hides a working one:

- **Direct-egress path** (independent of DNS): `Interface → Internet (TCP
  egress)`. Always runs, so "DNS down but internet up" stays diagnosable.
- **Proxy-egress path** (independent of both): `Interface → Internet (env
  proxy)`. Native probes deliberately bypass proxies, so this row reports the environment-configured proxy separately — a proxy-only corporate network reads "online via proxy" rather than offline.
- **Public-DNS path** (independent of system DNS): `Interface → DNS (public
  8.8.8.8)`. Failing to reach the third-party resolver is N/A; differing answers warn about split DNS or filtering but never fail the run on their own.
- **Wi-Fi metadata path**: `Interface → Wi-Fi network`. SSID discovery runs beside network checks, so slow OS lookup never delays them.
- **Plain HTTP path**: `Interface → DNS → HTTP :80`.
- **Selected target path**: `Interface → DNS → TCP → TLS → HTTPS` for secure web targets, or applicable protocol row for other ports.
- **Path-MTU branch** (hangs off connect, not off any protocol): `TCP → Path
  MTU`. Black hole breaks SSH and SMTP exactly as thoroughly as TLS.

Each row lands in one of five states: **✓ Pass**, **! Warn** (reachable but degraded — high latency, some addresses failing, an ambiguous source interface), **✗ Fail**, **⊘ Skip** (a prerequisite failed), or **– N/A** (doesn't apply — e.g. DNS on an IP literal). Warn never counts as a failure.

| Probe | Passes when | Notes |
|-------|-------------|-------|
| **Interface** | A non-loopback interface is up and running | |
| **Internet (TCP egress)** | A TCP connect to well-known anycast `:443` endpoints succeeds | IPv4 and IPv6 probed independently in parallel; either family passes, both are reported |
| **Internet (env proxy)** | The `HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY` proxy grants a tunnel | HTTP `CONNECT`; `socks5` resolves locally and sends an address, while `socks5h` sends the hostname for proxy-side DNS; N/A when no proxy is configured or `NO_PROXY` exempts the probe host |
| **DNS** | The host resolves to an IPv4 or IPv6 address (system resolution) | IP-literal targets are N/A; all A/AAAA records are retained |
| **DNS (public 8.8.8.8)** | A direct query to Google Public DNS provides a second opinion | N/A when outbound DNS is unavailable; disagreement is Warn, not Fail |
| **TCP** | A TCP connect to the target port succeeds | races A/AAAA records Happy-Eyeballs style (RFC 8305), pins the winner |
| **Path MTU** | a 24 KiB write drains beyond the measured kernel send buffer | finds evidence consistent with an MTU/PMTU black hole — never a Fail, see below |
| **TLS** | The TLS handshake (SNI + cert verification) succeeds | certificate time, hostname, issuer, protocol, timeout, early-close, and TCP failures receive stable JSON causes |
| **HTTP** | Port 80 returns any HTTP response (incl. 3xx/4xx/5xx) | Independent HEAD after DNS, redirects off, proxy off |
| **HTTPS** | The selected TLS port returns any HTTP response | HEAD against the TLS-validated IP, redirects off, proxy off |
| **SSH/SMTP banner** | TCP connects (banner read best-effort) | bounded read; connected but silent → Warn (not a failure) |

### Path MTU without root

A path MTU smaller than the local interface's, on a path that also filters the ICMP that would say so, is the classic tunnel/VPN/PPPoE mystery: TCP connects, then the connection dies the moment either side sends a real packet. `ping` and `curl` can't tell you that, and confirming it normally means raw sockets and the DF flag.

The **Path MTU** row looks for it from an ordinary socket. The TCP handshake is the control: SYN/SYN-ACK are small enough to cross a narrowed link, so a completed connect already proves that small packets arrive. The probe asks the kernel for a 4 KiB send buffer, reads back the effective size the OS actually installed, and writes 24 KiB. A write that advances beyond that measured buffer could not have remained entirely queued locally, so:

- the write drains beyond the effective send buffer → bulk TCP data moved, **Pass**, with the connection's TCP MSS when the OS exposes it,
- the write stalls without draining that buffer → **Warn**, naming the evidence and an MSS/MTU experiment,
- the peer hangs up first → **N/A**: inconclusive, and it will not guess.

It deliberately never Fails: a peer that accepts the connection and then stops accepting data can stall the write the same way. A normal TCP socket also cannot discover the exact path MTU when ICMP feedback is filtered. The row therefore reports the bytes written, the effective send-buffer size, the TCP MSS when available, and the local interface MTU as context—never as a measured path MTU. Only when both this write and a protocol exchange time out does the overall verdict identify a probable network-path problem; certificate and other immediate protocol failures remain service failures.

The 24 KiB payload is inert, self-labelling filler; TLS targets get a record header in front so the TLS server reads the payload instead of resetting on the first byte. This is the only probe that sends bulk data — under `--watch` that is 24 KiB per pass, once every 5 seconds.

No verdict depends on ICMP. Plenty of healthy hosts drop ping, so a failed `ping` proves nothing and a successful one proves less than a TCP connect — RTT is measured from the TCP-connect handshake instead (no ICMP, no root). `ping` is available as a drill-down tool, where it is evidence for a human rather than input to the diagnosis. The source IP and interface are read from the winning connection's `LocalAddr`, with a UDP-connect fallback (which sends no packets) for path identity on failure. Every probe is bounded by a 4-second timeout.

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
netdoc --iface wg0 host # bind probe traffic to wg0's source address
```

`--timeout` overrides the per-check probe timeout; see `netdoc --help` for the default. `--watch` starts another pass five seconds after each run; in the TUI it shows the last 20 states plus a failure count for every check, and with `--json` it streams the same report on stdout, one compact JSON object per line, until the process is interrupted. Those lines carry an extra `ts` field (RFC 3339, UTC) and are otherwise the one-shot report unchanged — one-shot output stays pretty-printed, with no `ts`.
`--iface` binds probe connections and DNS lookups to the interface's first IPv4 address (or its first IPv6 when there is no IPv4). Pass an exact local IP instead when the interface has multiple addresses.

The target parser has two independent axes: **port** (explicit `:port` > scheme default > 443) and **protocol rows** (an explicit `http`/`https`/`ssh`/`smtp` scheme wins; otherwise it is inferred from the port — `443/8443`→HTTP+TLS+HTTPS, `80`→HTTP, `22`→SSH, `25/587`→SMTP). Hosts are validated against a strict allowlist; IPv6 literals are accepted bare (`::1`) or bracketed with a port (`[::1]:443`).

The TUI saves up to 50 recent targets between sessions in `$XDG_CONFIG_HOME/netdoc/history` (normally `~/.config/netdoc/history`) on Linux, `~/Library/Application Support/netdoc/history` on macOS, or `%AppData%\netdoc\history` on Windows. Exit `netdoc` and delete that file to clear history.

| Key | Action |
|-----|--------|
| `↑`/`↓` (`k`/`j`) | select a probe row, or a device in the network map |
| `v` | run a LAN scan and show a network map of the local private `/24` (unprivileged `nmap`) |
| `enter` | set the selected map device as the new target, or open the current tool job's output |
| `/` (viewer) | filter the viewer to matching lines (`enter` commits, `esc` clears it, a second `esc` leaves) |
| `home`/`end`, `pgup`/`pgdn` (viewer) | jump to top/bottom (`end` re-enables follow) or page through the output |
| `y` / `w` (viewer) | copy / save the viewer's retained output (up to 5,000 lines; respects its filter) |
| `r` | restart — opens a prompt to edit the `netdoc` arguments (`enter` runs, `esc` backs out) |
| `S` | SSH login — a form for username, key, and password, then hands the terminal to `ssh` (hinted only once the SSH banner check passes, but usable against any target) |
| `tab` | switch between running tool jobs |
| `esc` | cancel the focused job only (`tab` picks which); `q` is the stop-everything path |
| `y` / `w` | yank / write (copy / save locally) a reviewable report of the chain plus every tool job |
| `?` | full-screen key cheatsheet; any key closes it |
| `q` | quit (cancels running jobs first, then exits) |

## Drill-down tools

Each diagnosis row is *evidence*; when you want proof, run the real tools as cancellable streaming jobs — several run at once, and `tab` switches between the live ones. A contextual toolbox shows the tools available for the current target with their hotkeys, greying out missing binaries with an install hint. Output is bounded and sanitized (no terminal-escape injection from a hostile server); reports include version/OS metadata plus each job's command, status, duration, and last 15 output lines.
Review your local copy before sharing — tool evidence may contain sensitive data.

The same hotkeys map to each OS's built-in tools:

| Key | Linux | macOS | Windows |
|-----|-------|-------|---------|
| `i` | `ip route` | `netstat -rn` | `route print -4` |
| `s` | `ss -tunp` | `netstat -an -p tcp` | `netstat -ano` |
| `p` | `ping -c 4 -W 2` | `ping -c 4` | `ping -n 4 -w 2000` |
| `d` | `dig +time=2 +tries=1` | `dig +time=2 +tries=1` | `nslookup` |
| `c` | `curl … -w '…'` (concise summary) | same | `curl.exe` (bypasses the PowerShell 5.1 `curl` alias) |
| `c` (SSH target) | `ssh -v -o BatchMode=yes …` (bounded banner/handshake check) | same | same |
| `c` (SMTP target) | `openssl s_client -starttls smtp` | same | same |
| `t` | `traceroute -w 2 -q 1 -m 20` | same | `tracert -w 2000 -h 20` |
| `m` | `mtr --report --report-cycles 5` | same (via brew) | `pathping -h 20 -q 5 -p 100 -w 500` (own 90 s budget) |
| `n` | `nmap -sT -Pn --host-timeout 110s` (the explicit target port, else nmap's default top 1000) | same | same |

`n` and `v` are gated behind an explicit confirmation before their active probes run. `n` uses a plain connect scan with nmap's default timing and no version/OS detection. `v` runs host discovery without raw sockets or root, and caps its scope at the source address's `/24`.

The `c` slot is protocol-aware: HTTP(S) and unknown-port targets get `curl`, while SSH (port 22) and SMTP (ports 25/587) targets get a protocol-appropriate handshake probe rather than an HTTPS-oriented `curl` line. The SSH check uses a throwaway known-hosts file (no prompts, no writes) and disables authentication with `PreferredAuthentications=none`, stopping after the banner and key exchange.

The routes and sockets tools are target-independent; the rest need a host. Tools run with an argument slice (never a shell string), in their own process group on Unix (so cancelling kills descendants too), and without privilege escalation. The displayed command is copy-pasteable in a POSIX shell (Linux/macOS) or PowerShell (Windows; cmd.exe paste is not supported).

`--toolbox [<host>]` opens straight into the toolbox without auto-running the chain (press `r` to run it). With no host, only the target-independent tools are offered.

### SSH login

`S` logs in to the current target — the machine the checks are about — so it needs one (`r` sets it). The form asks for the three things that are yours rather than the target's: `tab` moves between fields, `←`/`→` picks the key, `enter` connects, and `esc` backs out.

```
╭────────────────────────────────────────────────────╮
│ SSH login to 192.168.1.50:2222                     │
│   Username  mplaczek                               │
│ ▸ Key       id_rsa  (3 of 4)  ←/→                  │
│   Password  *******                                │
╰────────────────────────────────────────────────────╯
```

Unlike the drill-down tools, this is not a bounded job: `netdoc` suspends itself and gives `ssh` the real terminal, so the session is fully interactive and anything the form left blank — a key passphrase, a host-key check, a 2FA code — is asked by `ssh` on screen. When the session ends the TUI comes back with `ssh`'s stderr in the job pane, so a `Permission denied` is still readable afterwards instead of being painted over.

The form fills in `ssh` options, nothing more:

| Field | Effect |
|-------|--------|
| (host) | the target, plus `-p` when it named a non-default port |
| Username | `-l <login>`, not `login@host`, so a name starting with `-` stays a name |
| Key | `-i <path> -o IdentitiesOnly=yes`, so a loaded agent can't spend the server's auth attempts before this key is tried |
| Password | see below |

The key list is the private keys in `~/.ssh`, each recognized by its `.pub` half sitting next to it — no key file is ever opened or parsed. `none` (the first entry) leaves key selection to `ssh`, i.e. to the agent and `ssh_config`, which is also where a key kept outside `~/.ssh` belongs.

The typed password is echoed as dots and never reaches a command line, notice, or report. It is passed to `ssh` through the environment (`SSH_ASKPASS`), where `ssh` re-executes `netdoc` as its askpass helper and reads the secret from the helper's stdout. What that buys: the secret stays out of argv, which every process on the machine can read, and out of your shell history — but not out of memory. The password field is cleared once connecting starts, but by then the value has been copied into `ssh`'s environment, where it stays until `ssh` exits and is inherited by the whole subtree `ssh` starts — `ProxyCommand`, `LocalCommand`, and `ProxyJump`'s own `ssh`. On Linux that environment is readable through `/proc` by your own processes and by root; anything your `ssh_config` runs sees it too. `-o NumberOfPasswordPrompts=1` stops `ssh` re-offering a rejected one.

Because forced askpass routes *every* prompt to the helper, the helper answers only password and passphrase prompts and refuses the rest. So the first connection to an unknown host — where `ssh` asks you to verify the host key — needs one run with the password field blank, which puts that question back on your terminal where it belongs.

The whole `ssh` subtree inherits the askpass setting, so with `ProxyJump` in your `ssh_config` the jump host's `ssh` asks the helper too. The prompt names the machine it is asking for (`user@host's password:`), and the helper answers only when that host matches the target — the jump host's prompt goes back to your terminal. Prompts naming no host (a key passphrase, a PAM keyboard-interactive `Password:`) are still answered on a direct connection, where only one machine can be asking; refusing those would break ordinary logins. Add a proxy and they name no host *and* could be either end, so the helper refuses all of them. Wording is no help there — keyboard-interactive text is written by the far end, so a jump host can call its question a passphrase — and that run wants the password field blank anyway, which puts the prompt back on your terminal. All of this depends on netdoc being able to read your resolved config (`ssh -G`); when that lookup fails there is no way to tell whose prompt is whose, so password-assisted login is refused outright and the form says so.

### JSON output

`--json` runs the same probe DAG headless — no TUI — and prints one JSON document to stdout:

```json
{
  "version": "1.2.3",
  "target": {"host": "github.com", "port": 443, "protocol": "tls+http"},
  "checks": [
    {"id": "dns", "name": "DNS github.com", "status": "PASS", "ms": 12, "detail": "github.com → 140.82.113.3", "addrs": ["140.82.113.3"]}
  ],
  "summary": "All checks passed — github.com:443 looks healthy.",
  "verdict": "ok",
  "ok": true
}
```

`status` is one of `PASS`, `WARN`, `FAIL`, `SKIP`, `N/A`. `target` is `null` in generic (no-target) mode. `ms` is the check's wall time truncated to milliseconds but floored at `1`, so `0` means the check never ran. Optional per-check fields (`cause`, `address_families`, `fix`, `addrs`, `selected_ip`, `source`, `iface`, `network`, `portal`, `attempts`) are omitted when empty. `internet_tcp.address_families` records the independently tested IPv4 and IPv6 state as `reachable` or `unreachable`; it is not inferred from a hostname dial that may fall back. A configured family whose path fails while the other succeeds warns with `ipv4_unreachable` or `ipv6_unreachable`. Failed proxy and TLS checks populate `cause` so automation can distinguish failure stages without parsing `detail`; TLS values include `certificate_expired`, `certificate_not_yet_valid`, `hostname_mismatch`, `untrusted_issuer`, `tls_handshake_failure`, `tcp_unreachable`, `timeout`, and `connection_closed`. On Linux, failed direct egress may use `no_default_route`, `gateway_unreachable`, `selected_path_failed`, or `preferred_route_failed`. The `portal` object marks detected HTTP interception and includes `redirect_url` only when the response supplied a valid HTTP(S) sign-in URL; the app displays that URL but never opens it. Field names and the status vocabulary are stable — safe to script against. Exit codes follow the table below (`ok: false` ⇒ exit `1`).

Resolver failures can add `dns_timeout` or `dns_temporary_failure`; a banner
peer that accepted TCP before resetting adds `connection_reset`. These are
optional additions to the existing check objects.

`failed_stage` names the first check that failed (`dns`, `target_tcp`, `tls`, …) and is omitted when none did — enough to route a bug report without reading any prose.

`--json --watch` prints the same document once per pass, compacted onto a single line (NDJSON), five seconds apart, until the process is interrupted — for the intermittent failure you can't sit and watch:

```sh
netdoc --json --watch github.com | jq -c 'select(.ok | not) | {ts, failed_stage, summary}'
```

Those lines add a `ts` field (RFC 3339, UTC) saying when the pass ran; nothing else changes. The exit code is the last completed pass's.

`verdict` is the summary as a machine-readable class, for the question a script actually asks: *is my network broken, or is theirs?*

| `verdict` | Meaning |
|---|---|
| `ok` | Every check passed |
| `degraded` | Everything asked for works, but some rung is impaired — high latency, a proxy-only network, direct egress blocked while the target is fine |
| `dns` | The name did not resolve |
| `network` | **The path is unavailable** — the link is down, there's no egress, or the target is unreachable with nothing else proving the network usable |
| `service` | **The path works, the far end does not** — TCP refused while the general internet is reachable, or TLS/HTTP/banner failing on top of a good connection |
| `incomplete` | A check has no result (the chain did not finish) |

The `network`/`service` split is decided by evidence, not guesswork: an unreachable target is only blamed on the service when direct egress independently succeeded. With no working egress to compare against, netdoc says `network` rather than accusing a host it never reached.

### Exit codes

| Situation | Exit |
|---|---|
| Chain completed, no failed row (Skips allowed) | `0` |
| Any failed row | `1` |
| Quit before the chain finished | `1` |
| Bad arguments / validation reject | `2` |

```sh
netdoc github.com || echo "path to github is broken"
```

## Platform support

All probes, the diagnosis engine, and the TUI are pure Go and identical on Linux, macOS, and Windows. Platform-specific garnish (the default gateway, the Wi-Fi SSID) uses the kernel directly on Linux (`/proc/net/route`, a wireless ioctl) and OS built-in commands elsewhere (`route`/`networksetup` on macOS, `route print`/`netsh wlan` on Windows); when those fail the fields degrade to empty rather than failing the probe.

Windows built-in toolbox commands are decoded from the active OEM code page before their output is sanitized. UTF-8 tools like `curl.exe` and `nmap` are left untouched.

## Feature summary

Native DAG probes + diagnosis engine + two-pane UI, concurrent cancellable streaming tool jobs (`ping`/`dig`/`curl`/`traceroute`/`mtr`/`ss`/`ip`/`nmap`) + filterable output viewer + `--toolbox` mode, `Warn` state, proxy-aware diagnosis, unprivileged path-MTU check, public-DNS second opinion, LAN network map, `S` SSH login, source-interface pinning (`--iface`), `--watch` (TUI history strip and `--json` NDJSON), `--json` output, report copy/save.

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
go test -tags netns_integration ./internal/simulation
go test -race ./...
go test -fuzz=FuzzSanitize -fuzztime=10s ./internal/textsafe
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/goreleaser/goreleaser/v2@v2.17.1 check
```

Race, fuzz, and network-namespace checks run only on Linux in CI. The
`netns_integration` tests skip themselves on a host without unprivileged user
namespaces; they never need root.

## Testing Network Doctor against broken networks

`netdoc-sim` builds a throwaway virtual network from a YAML scenario, breaks it
on purpose, runs the real netdoc binary inside it, and grades the diagnosis
against the injected fault. It is an unprivileged, Linux-only development tool
for deterministic regression testing; it does not alter the host network.

The simulator supports written scenarios, reproducible fault campaigns,
generated bug hunts, and triage of reproducible findings. Start with the
scenario catalog and run any name it prints:

```sh
go build -o netdoc . && go build -o netdoc-sim ./cmd/netdoc-sim
./netdoc-sim scenarios
./netdoc-sim run broken-dns
```

`netdoc-sim scenarios` is the source of truth for the complete set of shipped
built-in scenarios rather than the README. See the **[complete simulator
guide](docs/simulation.md)** for setup and requirements, commands and workflows,
scenario authoring, campaigns, hunt and triage usage, reports, troubleshooting,
tests, and limitations.

## Development

The code is split by responsibility:

- `main.go` owns CLI arguments, process I/O, application startup.
- `internal/diagnostic` owns target parsing, native probes, per-OS route/SSID lookups, verdict logic without depending on terminal presentation.
- `internal/ui` owns Bubble Tea state, rendering, tool jobs.
- `internal/textsafe` sanitizes untrusted remote and subprocess text shared by both layers.
- `internal/simulation` + `cmd/netdoc-sim` build virtual networks to test the diagnosis engine against; nothing here ships in the `netdoc` binary.

The UI depends on diagnostics; diagnostics do not depend on the UI. Add network semantics under `internal/diagnostic`, and interaction or rendering behavior under `internal/ui`. The simulator depends on `internal/diagnostic` for probe ids and target parsing, and on nothing in `internal/ui`.

## License

Network Doctor is free software under the [GNU General Public License](LICENSE), either version 3 of the License, or (at your option) any later version. Package metadata declares this as `GPL-3.0-or-later`.

## Star History

<a href="https://www.star-history.com/?repos=heymaikol%2Fnetwork-doctor&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=heymaikol/network-doctor&type=date&theme=dark&legend=top-left&sealed_token=4XgBnUitKav8JRmYTBIst1x9bwnwAJEe_qDlPb20W2iSTPj_FG9cXicHok2d59GSb9QcFWynwWwexSj1vBNPTojS13SGdu0UUhNb9dx930Yaj-93UZ9oVw" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=heymaikol/network-doctor&type=date&legend=top-left&sealed_token=4XgBnUitKav8JRmYTBIst1x9bwnwAJEe_qDlPb20W2iSTPj_FG9cXicHok2d59GSb9QcFWynwWwexSj1vBNPTojS13SGdu0UUhNb9dx930Yaj-93UZ9oVw" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=heymaikol/network-doctor&type=date&legend=top-left&sealed_token=4XgBnUitKav8JRmYTBIst1x9bwnwAJEe_qDlPb20W2iSTPj_FG9cXicHok2d59GSb9QcFWynwWwexSj1vBNPTojS13SGdu0UUhNb9dx930Yaj-93UZ9oVw" />
 </picture>
</a>

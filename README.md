# Network Doctor

A terminal UI that diagnoses your network connectivity and tells you **where the
connection breaks** in plain English — not just a wall of tool output.

![Network Doctor diagnosing github.com:443, running a traceroute and mtr concurrently](assets/demo.gif)

## Install

Runs on **Linux, macOS, and Windows**. The project is `network-doctor`; the
installed binary is `netdoc`.

### Arch Linux (AUR)

The [`network-doctor`](https://aur.archlinux.org/packages/network-doctor)
package builds from source:

```sh
yay -S network-doctor    # or: paru -S network-doctor
```

Or by hand, without an AUR helper:

```sh
git clone https://aur.archlinux.org/network-doctor.git
cd network-doctor
makepkg -si
```

### macOS (Homebrew)

The binary is unsigned, so the cask strips the quarantine attribute and
Gatekeeper does not prompt. That removes a warning rather than adding a check;
verifying is a separate step you run yourself ([Verify your
download](#verify-your-download)):

```sh
brew tap heymaikol/tap
brew install --cask network-doctor
```

### Everywhere else

Download a prebuilt binary from the [latest release](https://github.com/heymaikol/network-doctor/releases/latest), or install with Go 1.25+:

```sh
go install github.com/heymaikol/network-doctor@latest
```

(`go install` names the binary `network-doctor` after the module; rename it to
`netdoc` if you like.) Check what you're running with `netdoc --version`.

Or build from a clone:

```sh
git clone https://github.com/heymaikol/network-doctor
cd network-doctor
go build -o netdoc .
```

### Verify your download

Releases carry a signed attestation binding each artifact to the workflow run
that built it. v1.8.4 and earlier were published before the release workflow
attested anything and have none. With GitHub CLI installed and `gh auth login`
completed:

```sh
VERSION=X.Y.Z
gh attestation verify "./netdoc_${VERSION}_linux_amd64" \
  --repo heymaikol/network-doctor \
  --signer-workflow heymaikol/network-doctor/.github/workflows/release.yml
```

This proves the bytes were built from the tagged commit by the release workflow;
that workflow gates release on CI and an ancestor-of-`main` check. The source
tarball AUR builds from is attested too.

## How it diagnoses

Probes form a **dependency graph with independent branches**, so an unrelated
failure never hides a working one:

- **Direct-egress path** (independent of DNS): `Interface → Internet (TCP
  egress)`. Always runs, so "DNS is down but the internet is up" is diagnosable.
- **Proxy-egress path** (independent of both): `Interface → Internet (env
  proxy)`. The native probes deliberately bypass proxies, so this row reports
  the environment-configured proxy separately — a proxy-only corporate network
  reads as "online via proxy" instead of offline.
- **Public-DNS path** (independent of system DNS): `Interface → DNS (public
  1.1.1.1)`. Failure to reach the third-party resolver is N/A; differing
  answers warn about split DNS or filtering but never fail the run by themselves.
- **Wi-Fi metadata path**: `Interface → Wi-Fi network`. SSID discovery runs
  beside the network checks, so a slow OS lookup never delays them.
- **Plain HTTP path**: `Interface → DNS → HTTP :80`.
- **Selected target path**: `Interface → DNS → TCP → TLS → HTTPS` for secure
  web targets, or the applicable protocol row for other ports.

Each row is one of five states: **✓ Pass**, **! Warn** (reachable but degraded —
high latency, some addresses failing, ambiguous source interface), **✗ Fail**,
**⊘ Skip** (a prerequisite failed), or **– N/A** (doesn't apply — e.g. DNS on an
IP literal). A Warn never counts as a failure.

| Probe | Passes when | Notes |
|-------|-------------|-------|
| **Interface** | A non-loopback interface is up and running | |
| **Internet (TCP egress)** | A TCP connect to well-known anycast `:443` endpoints succeeds | IPv4 and IPv6 probed independently in parallel; either family passes, both are reported |
| **Internet (env proxy)** | The `HTTPS_PROXY`/`HTTP_PROXY` proxy grants a `CONNECT` tunnel | N/A when no proxy is configured; honors `NO_PROXY` |
| **DNS** | The host resolves to an IPv4 or IPv6 address (system resolution) | IP-literal targets are N/A; all A/AAAA records are retained |
| **DNS (public 1.1.1.1)** | A direct query to Cloudflare provides a second opinion | N/A when outbound DNS is unavailable; disagreement is Warn, not Fail |
| **TCP** | A TCP connect to the target port succeeds | races A/AAAA records Happy-Eyeballs style (RFC 8305), pins the winner |
| **TLS** | The TLS handshake (SNI + cert verification) succeeds | bad/expired cert, clock skew, or MITM → Fail |
| **HTTP** | Port 80 returns any HTTP response (incl. 3xx/4xx/5xx) | Independent HEAD after DNS, redirects off, proxy off |
| **HTTPS** | The selected TLS port returns any HTTP response | HEAD against the TLS-validated IP, redirects off, proxy off |
| **SSH/SMTP banner** | TCP connects (banner read best-effort) | bounded read; connected but silent → Warn (not a failure) |

No verdict depends on ICMP. Plenty of healthy hosts drop ping, so a failed
`ping` proves nothing and a successful one proves less than a TCP connect
does — RTT is measured from the TCP-connect handshake instead (no ICMP, no
root). `ping` is available as a drill-down tool, where it's evidence for a
human rather than input to a diagnosis. The source IP
and interface are read from the winning connection's `LocalAddr`, with a
UDP-connect fallback (sends no packets) for path identity on failure. Every probe
is bounded by a 4-second timeout.

## Usage

```sh
netdoc                  # generic local + internet diagnosis
netdoc github.com       # diagnose the path to a host (→ HTTP + TLS + HTTPS)
netdoc github.com:22    # port selects the protocol rows (→ SSH banner)
netdoc https://host:80  # explicit scheme selects the protocol (→ TLS + HTTPS on :80)
netdoc ssh://host:2222  # explicit scheme keeps SSH on a nonstandard port
netdoc --json host      # headless: one JSON report on stdout (scripts, CI, bug reports)
```

`--timeout` overrides the per-check probe timeout; see `netdoc --help`
for the default.

The target parser has two independent axes: the **port** (explicit `:port` >
scheme default > 443) and the **protocol rows** (an explicit `http`/`https`/`ssh`/`smtp`
scheme wins; otherwise inferred from the port — `443/8443`→HTTP+TLS+HTTPS, `80`→HTTP,
`22`→SSH, `25/587`→SMTP). Hosts are validated against a strict allowlist; IPv6
literals are accepted bare (`::1`) or bracketed with a port (`[::1]:443`).

The TUI saves up to 50 recent targets between sessions in
`$XDG_CONFIG_HOME/netdoc/history` (normally `~/.config/netdoc/history`) on
Linux, `~/Library/Application Support/netdoc/history` on macOS, or
`%AppData%\netdoc\history` on Windows. Exit `netdoc` and delete that file to
clear the history.

| Key | Action |
|-----|--------|
| `↑`/`↓` (`k`/`j`) | select a probe row, or a device in the network map |
| `v` | run a LAN scan and show a network map of the local private `/24` (unprivileged `nmap`) |
| `enter` | set the selected map device as the new target, or open the current tool job's output |
| `y` / `w` (viewer) | copy / save the viewer's retained output (up to 5,000 lines; respects its filter) |
| `r` | restart — opens a prompt to edit the `netdoc` arguments (`enter` runs, `esc` backs out) |
| `tab` | switch between running tool jobs |
| `y` / `w` | yank / write (copy / save locally) a reviewable report of the chain plus every tool job |
| `q` | quit |

## Drill-down tools

Each row in the diagnosis is *evidence*; when you want proof, run real tools as
cancellable streaming jobs — several can run at once, and `tab` switches
between the live ones. The contextual toolbox shows the
tools available for the current target with their hotkeys — missing binaries are
greyed out with an install hint. Output is bounded and sanitized (no
terminal-escape injection from a hostile server); reports include version/OS
metadata plus each job's command, status, duration, and last 15 output lines.
Review the local copy before sharing because tool evidence may contain sensitive data.

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

`n` and `v` are gated behind an explicit confirmation before their active probes
run. `n` uses a plain connect scan with nmap's default timing and no version/OS
detection. `v` runs host discovery without raw sockets or root and caps the scope
at the source address's `/24`.

The `c` slot is protocol-aware: HTTP(S) and unknown-port targets get `curl`,
while SSH (port 22) and SMTP (ports 25/587) targets get a protocol-appropriate
handshake probe — never an HTTPS-oriented `curl` line. The SSH check uses a
throwaway known-hosts file (no prompts, no writes) and disables authentication
with `PreferredAuthentications=none`, stopping after the banner and key exchange.

The routes/sockets tools are target-independent; the rest need a host. Tools are
run with an argument slice (never a shell string), in their own process group on
Unix (cancel kills descendants too), and without privilege escalation. The
displayed command is copy-pasteable in a POSIX shell (Linux/macOS) or PowerShell
(Windows; cmd.exe paste is not supported).

`--toolbox [<host>]` opens straight into the toolbox without auto-running the
chain (press `r` to run it). With no host, only the target-independent tools are
offered.

### JSON output

`--json` runs the same probe DAG headless — no TUI — and prints one JSON
document to stdout:

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

`status` is one of `PASS`, `WARN`, `FAIL`, `SKIP`, `N/A`. `target` is `null`
in generic (no-target) mode. `ms` is the check's wall time truncated to
milliseconds but floored at `1`, so `0` means the check never ran. Optional
per-check fields (`fix`, `addrs`, `selected_ip`, `source`, `iface`, `network`,
`portal`, `attempts`) are omitted when empty. A `portal` object marks detected
HTTP interception and includes `redirect_url` only when the response supplied
a valid HTTP(S) sign-in URL; the app displays that URL but never opens it.
Field names and the status vocabulary are stable — safe to script against.
Exit codes follow the table below (`ok: false` ⇒ exit `1`).

`failed_stage` names the first check that failed (`dns`, `target_tcp`, `tls`,
…) and is omitted when none did — enough to route a bug report without reading
the prose.

`verdict` is the summary as a machine-readable class, for the question a
script actually asks: *is my network broken, or is theirs?*

| `verdict` | Meaning |
|---|---|
| `ok` | Every check passed |
| `degraded` | Everything asked for works, but some rung is impaired — high latency, a proxy-only network, direct egress blocked while the target is fine |
| `dns` | The name did not resolve |
| `network` | **The path is unavailable** — the link is down, there's no egress, or the target is unreachable with nothing else proving the network usable |
| `service` | **The path works, the far end does not** — TCP refused while the general internet is reachable, or TLS/HTTP/banner failing on top of a good connection |
| `incomplete` | A check has no result (the chain did not finish) |

The `network`/`service` split is decided by evidence, not guesswork: an
unreachable target is only blamed on the service when direct egress
independently succeeded. With no working egress to compare against, netdoc
says `network` rather than accuse a host it never reached.

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

All probes, the diagnosis engine, and the TUI are pure Go and identical on
Linux, macOS, and Windows. The platform-specific garnish (default gateway,
Wi-Fi SSID) uses the kernel directly on Linux (`/proc/net/route`, wireless
ioctl) and the OS's built-in commands elsewhere (`route`/`networksetup` on
macOS, `route print`/`netsh wlan` on Windows); when those fail the fields
degrade to empty rather than failing a probe.

Windows' built-in toolbox commands are decoded from the active OEM code page
before their output is sanitized. UTF-8 tools such as `curl.exe` and `nmap`
remain untouched.

## Roadmap

Implemented: native DAG probes + diagnosis engine + two-pane UI, concurrent
cancellable streaming tool jobs (`ping`/`dig`/`curl`/`traceroute`/`mtr`/`ss`/`ip`/`nmap`) +
a scrollable output viewer + `--toolbox` mode, the `Warn` state, proxy-aware
diagnosis, `--json` output, and report copy/save.

## Built with

[Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles), and
[Lip Gloss](https://github.com/charmbracelet/lipgloss).

## Tests

Before submitting a change, run the complete CI gate:

```sh
go vet ./...
go build ./...
go test ./...
go test -tags integration ./internal/diagnostic
go test -race ./...
go test -fuzz=FuzzSanitize -fuzztime=10s ./internal/textsafe
golangci-lint run ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
goreleaser check
```

The race and fuzz checks run only on Linux in CI.

## Development

The code is split by responsibility:

- `main.go` owns CLI arguments, process I/O, and application startup.
- `internal/diagnostic` owns target parsing, native probes, per-OS route/SSID
  lookups, and verdict logic without depending on terminal presentation.
- `internal/ui` owns Bubble Tea state, rendering, and tool jobs.
- `internal/textsafe` sanitizes untrusted remote and subprocess text shared by
  both layers.

The UI depends on diagnostics; diagnostics do not depend on the UI. Add network
semantics under `internal/diagnostic` and interaction or rendering behavior under
`internal/ui`.

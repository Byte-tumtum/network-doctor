# Network Doctor

Terminal UI. Diagnose network connectivity, tell you **where connection breaks** in plain English — not wall of tool output.

![Network Doctor diagnosing github.com:443: the check list, a traceroute and mtr running concurrently, the filtered output viewer, a LAN scan, the SSH login form, and watch mode](assets/demo.gif)

## Install

Runs on **Linux, macOS, and Windows**. Project = `network-doctor`; installed binary = `netdoc`.

### Arch Linux (AUR)

[`network-doctor`](https://aur.archlinux.org/packages/network-doctor) package builds from source:

```sh
yay -S network-doctor    # or: paru -S network-doctor
```

By hand, no AUR helper:

```sh
git clone https://aur.archlinux.org/network-doctor.git
cd network-doctor
makepkg -si
```

### macOS (Homebrew)

Binary unsigned, so cask strips quarantine attribute and Gatekeeper no prompt. That removes warning, not adds check; verify separate step you run yourself ([Verify your download](#verify-your-download)):

```sh
brew tap heymaikol/tap
brew install --cask network-doctor
```

### Everywhere else

Grab prebuilt binary from [latest release](https://github.com/heymaikol/network-doctor/releases/latest), or install with Go 1.25+:

```sh
go install github.com/heymaikol/network-doctor@latest
```

(`go install` names binary `network-doctor` after module; rename to `netdoc` if you like.) Check what you run with `netdoc --version`.

Or build from clone:

```sh
git clone https://github.com/heymaikol/network-doctor
cd network-doctor
go build -o netdoc .
```

### Verify your download

Releases carry signed attestation binding each artifact to workflow run that built it. v1.8.4 and earlier published before release workflow attested anything — none. With GitHub CLI installed and `gh auth login` done:

```sh
VERSION=X.Y.Z
gh attestation verify "./netdoc_${VERSION}_linux_amd64" \
  --repo heymaikol/network-doctor \
  --signer-workflow heymaikol/network-doctor/.github/workflows/release.yml
```

Proves bytes built from tagged commit by release workflow; that workflow gates release on CI and ancestor-of-`main` check. Source tarball AUR builds from attested too.

## How it diagnoses

Probes form **dependency graph with independent branches**, so unrelated failure never hides working one:

- **Direct-egress path** (independent of DNS): `Interface → Internet (TCP
  egress)`. Always runs, so "DNS down but internet up" diagnosable.
- **Proxy-egress path** (independent of both): `Interface → Internet (env
  proxy)`. Native probes deliberately bypass proxies, so this row reports environment-configured proxy separately — proxy-only corporate network reads "online via proxy" not offline.
- **Public-DNS path** (independent of system DNS): `Interface → DNS (public
  8.8.8.8)`. Failure to reach third-party resolver = N/A; differing answers warn about split DNS or filtering but never fail run alone.
- **Wi-Fi metadata path**: `Interface → Wi-Fi network`. SSID discovery runs beside network checks, so slow OS lookup never delays them.
- **Plain HTTP path**: `Interface → DNS → HTTP :80`.
- **Selected target path**: `Interface → DNS → TCP → TLS → HTTPS` for secure web targets, or applicable protocol row for other ports.
- **Path-MTU branch** (hangs off connect, not off any protocol): `TCP → Path
  MTU`. Black hole breaks SSH and SMTP exactly as thoroughly as TLS.

Each row = one of five states: **✓ Pass**, **! Warn** (reachable but degraded — high latency, some addresses failing, ambiguous source interface), **✗ Fail**, **⊘ Skip** (prerequisite failed), or **– N/A** (doesn't apply — e.g. DNS on IP literal). Warn never counts as failure.

| Probe | Passes when | Notes |
|-------|-------------|-------|
| **Interface** | A non-loopback interface is up and running | |
| **Internet (TCP egress)** | A TCP connect to well-known anycast `:443` endpoints succeeds | IPv4 and IPv6 probed independently in parallel; either family passes, both are reported |
| **Internet (env proxy)** | The `HTTPS_PROXY`/`HTTP_PROXY` proxy grants a `CONNECT` tunnel | N/A when no proxy is configured; honors `NO_PROXY` |
| **DNS** | The host resolves to an IPv4 or IPv6 address (system resolution) | IP-literal targets are N/A; all A/AAAA records are retained |
| **DNS (public 8.8.8.8)** | A direct query to Google Public DNS provides a second opinion | N/A when outbound DNS is unavailable; disagreement is Warn, not Fail |
| **TCP** | A TCP connect to the target port succeeds | races A/AAAA records Happy-Eyeballs style (RFC 8305), pins the winner |
| **Path MTU** | 24 KiB reaches the target as full-size segments | confirms or clears an MTU/PMTU black hole — never a Fail, see below |
| **TLS** | The TLS handshake (SNI + cert verification) succeeds | bad/expired cert, clock skew, or MITM → Fail |
| **HTTP** | Port 80 returns any HTTP response (incl. 3xx/4xx/5xx) | Independent HEAD after DNS, redirects off, proxy off |
| **HTTPS** | The selected TLS port returns any HTTP response | HEAD against the TLS-validated IP, redirects off, proxy off |
| **SSH/SMTP banner** | TCP connects (banner read best-effort) | bounded read; connected but silent → Warn (not a failure) |

### Path MTU without root

Path MTU smaller than local interface's, on path that also filters ICMP that would say so = classic tunnel/VPN/PPPoE mystery: TCP connects, then connection dies moment either side sends real packet. `ping` and `curl` can't tell you that, and confirming normally means raw sockets and DF flag.

**Path MTU** row confirms from ordinary socket. TCP handshake = control — SYN/SYN-ACK small enough to cross narrowed link, so completed connect already proves small packets arrive. Then probe shrinks own send buffer to 4 KiB and writes 24 KiB, which must leave as full-size segments. Nothing leaves that buffer until far end acknowledges it, so:

- write drains → full-size segments arrive, **Pass** (with interface MTU path confirmed to carry),
- write stalls with buffer barely emptied → nothing acknowledged at all, **Warn** naming evidence and MSS/MTU fix,
- peer hangs up first → **N/A**, inconclusive, not guess.

Deliberately never Fail: peer that accepts connection then stops reading stalls write same way, so row states its evidence — bytes written, buffer size, and that handshake got through — and leaves judgement to you. When evidence lands *and* protocol row above it failed, verdict reclassifies from `service` to `network`: service fine, path can't carry full-size packet. 24 KiB payload = inert, self-labelling filler; TLS targets get record header in front so TLS server reads (and acknowledges) payload instead of resetting on first byte. Only probe here that sends bulk data — under `--watch` that 24 KiB per pass, once every 5 seconds.

No verdict depends on ICMP. Plenty healthy hosts drop ping, so failed `ping` proves nothing and successful one proves less than TCP connect — RTT measured from TCP-connect handshake instead (no ICMP, no root). `ping` available as drill-down tool, where it evidence for human not input to diagnosis. Source IP and interface read from winning connection's `LocalAddr`, with UDP-connect fallback (sends no packets) for path identity on failure. Every probe bounded by 4-second timeout.

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

`--timeout` overrides per-check probe timeout; see `netdoc --help` for default. `--watch` starts another pass five seconds after each run; in TUI shows last 20 states plus failure count for every check, and with `--json` streams same report on stdout, one compact JSON object per line, until process interrupted. Those lines carry extra `ts` field (RFC 3339, UTC) and otherwise = one-shot report unchanged — one-shot output stays pretty-printed, no `ts`.
`--iface` binds probe connections and DNS lookups to interface's first IPv4 address (or first IPv6 when no IPv4). Pass exact local IP instead when interface has multiple addresses.

Target parser has two independent axes: **port** (explicit `:port` > scheme default > 443) and **protocol rows** (explicit `http`/`https`/`ssh`/`smtp` scheme wins; else inferred from port — `443/8443`→HTTP+TLS+HTTPS, `80`→HTTP, `22`→SSH, `25/587`→SMTP). Hosts validated against strict allowlist; IPv6 literals accepted bare (`::1`) or bracketed with port (`[::1]:443`).

TUI saves up to 50 recent targets between sessions in `$XDG_CONFIG_HOME/netdoc/history` (normally `~/.config/netdoc/history`) on Linux, `~/Library/Application Support/netdoc/history` on macOS, or `%AppData%\netdoc\history` on Windows. Exit `netdoc` and delete that file to clear history.

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

Each diagnosis row = *evidence*; want proof, run real tools as cancellable streaming jobs — several run at once, `tab` switches between live ones. Contextual toolbox shows tools available for current target with hotkeys — missing binaries greyed out with install hint. Output bounded and sanitized (no terminal-escape injection from hostile server); reports include version/OS metadata plus each job's command, status, duration, last 15 output lines.
Review local copy before sharing — tool evidence may contain sensitive data.

Same hotkeys map to each OS's built-in tools:

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

`n` and `v` gated behind explicit confirmation before their active probes run. `n` uses plain connect scan with nmap default timing, no version/OS detection. `v` runs host discovery without raw sockets or root and caps scope at source address's `/24`.

`c` slot protocol-aware: HTTP(S) and unknown-port targets get `curl`, while SSH (port 22) and SMTP (ports 25/587) targets get protocol-appropriate handshake probe — never HTTPS-oriented `curl` line. SSH check uses throwaway known-hosts file (no prompts, no writes) and disables authentication with `PreferredAuthentications=none`, stopping after banner and key exchange.

Routes/sockets tools target-independent; rest need host. Tools run with argument slice (never shell string), in own process group on Unix (cancel kills descendants too), and without privilege escalation. Displayed command copy-pasteable in POSIX shell (Linux/macOS) or PowerShell (Windows; cmd.exe paste not supported).

`--toolbox [<host>]` opens straight into toolbox without auto-running chain (press `r` to run it). No host = only target-independent tools offered.

### SSH login

`S` logs in to current target — machine the checks about — so needs one (`r` sets it). Form asks for three things that yours, not target's: `tab` moves between fields, `←`/`→` picks key, `enter` connects, `esc` backs out.

```
╭────────────────────────────────────────────────────╮
│ SSH login to 192.168.1.50:2222                     │
│   Username  mplaczek                               │
│ ▸ Key       id_rsa  (3 of 4)  ←/→                  │
│   Password  *******                                │
╰────────────────────────────────────────────────────╯
```

Unlike drill-down tools this not bounded job: `netdoc` suspends itself and gives `ssh` real terminal, so session fully interactive and anything form left blank — key passphrase, host-key check, 2FA code — asked by `ssh` on screen. Session ends, TUI comes back with `ssh` stderr in job pane, so `Permission denied` still readable after instead of painted over.

Form fills in `ssh` options, nothing more:

| Field | Effect |
|-------|--------|
| (host) | the target, plus `-p` when it named a non-default port |
| Username | the `user@` part of the destination |
| Key | `-i <path> -o IdentitiesOnly=yes`, so a loaded agent can't spend the server's auth attempts before this key is tried |
| Password | see below |

Key list = private keys in `~/.ssh`, each recognized by its `.pub` half sitting next to it — no key file ever opened or parsed. `none` (first entry) leaves key selection to `ssh`, i.e. agent and `ssh_config`, which also where key kept outside `~/.ssh` belongs.

Typed password echoed as dots and never reaches command line, notice, or report. Passed to `ssh` through environment (`SSH_ASKPASS`), where `ssh` re-executes `netdoc` as its askpass helper and reads secret from helper's stdout — argv would be readable by every process on machine, environment variable only by you. Form rebuilt on each `S`, so password lives no longer than open form, and `-o NumberOfPasswordPrompts=1` stops `ssh` re-offering rejected one.

Because forced askpass routes *every* prompt to helper, helper answers only password and passphrase prompts; refuses rest. So first connection to unknown host — where `ssh` asks you to verify host key — needs one run with password field blank, which puts that question back on your terminal where it belongs.

### JSON output

`--json` runs same probe DAG headless — no TUI — and prints one JSON document to stdout:

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

`status` = one of `PASS`, `WARN`, `FAIL`, `SKIP`, `N/A`. `target` = `null` in generic (no-target) mode. `ms` = check's wall time truncated to milliseconds but floored at `1`, so `0` means check never ran. Optional per-check fields (`fix`, `addrs`, `selected_ip`, `source`, `iface`, `network`, `portal`, `attempts`) omitted when empty. `portal` object marks detected HTTP interception, includes `redirect_url` only when response supplied valid HTTP(S) sign-in URL; app displays that URL but never opens it. Field names and status vocabulary stable — safe to script against. Exit codes follow table below (`ok: false` ⇒ exit `1`).

`failed_stage` names first check that failed (`dns`, `target_tcp`, `tls`, …), omitted when none did — enough to route bug report without reading prose.

`--json --watch` prints same document once per pass, compacted onto single line (NDJSON), five seconds apart, until process interrupted — for intermittent failure you can't sit and watch:

```sh
netdoc --json --watch github.com | jq -c 'select(.ok | not) | {ts, failed_stage, summary}'
```

Those lines add `ts` field (RFC 3339, UTC) saying when pass ran; nothing else changes. Exit code = last completed pass's.

`verdict` = summary as machine-readable class, for question script actually asks: *is my network broken, or is theirs?*

| `verdict` | Meaning |
|---|---|
| `ok` | Every check passed |
| `degraded` | Everything asked for works, but some rung is impaired — high latency, a proxy-only network, direct egress blocked while the target is fine |
| `dns` | The name did not resolve |
| `network` | **The path is unavailable** — the link is down, there's no egress, or the target is unreachable with nothing else proving the network usable |
| `service` | **The path works, the far end does not** — TCP refused while the general internet is reachable, or TLS/HTTP/banner failing on top of a good connection |
| `incomplete` | A check has no result (the chain did not finish) |

`network`/`service` split decided by evidence, not guesswork: unreachable target only blamed on service when direct egress independently succeeded. No working egress to compare against, netdoc says `network` rather than accuse host it never reached.

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

All probes, diagnosis engine, and TUI pure Go and identical on Linux, macOS, Windows. Platform-specific garnish (default gateway, Wi-Fi SSID) uses kernel directly on Linux (`/proc/net/route`, wireless ioctl) and OS built-in commands elsewhere (`route`/`networksetup` on macOS, `route print`/`netsh wlan` on Windows); when those fail fields degrade to empty rather than failing probe.

Windows built-in toolbox commands decoded from active OEM code page before output sanitized. UTF-8 tools like `curl.exe` and `nmap` untouched.

## Roadmap

Implemented: native DAG probes + diagnosis engine + two-pane UI, concurrent cancellable streaming tool jobs (`ping`/`dig`/`curl`/`traceroute`/`mtr`/`ss`/`ip`/`nmap`) + filterable output viewer + `--toolbox` mode, `Warn` state, proxy-aware diagnosis, unprivileged path-MTU check, public-DNS second opinion, LAN network map, `S` SSH login, source-interface pinning (`--iface`), `--watch` (TUI history strip and `--json` NDJSON), `--json` output, report copy/save.

## Built with

[Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles), and
[Lip Gloss](https://github.com/charmbracelet/lipgloss).

## Tests

Before submitting change, run complete CI gate. The final check requires GoReleaser v2.17.1, matching the CI and release workflows:

```sh
go vet ./...
CGO_ENABLED=0 go build ./...
go test ./...
go test -tags integration ./internal/diagnostic
go test -race ./...
go test -fuzz=FuzzSanitize -fuzztime=10s ./internal/textsafe
golangci-lint run ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
goreleaser check
```

Race and fuzz checks run only on Linux in CI.

## Development

Code split by responsibility:

- `main.go` owns CLI arguments, process I/O, application startup.
- `internal/diagnostic` owns target parsing, native probes, per-OS route/SSID lookups, verdict logic without depending on terminal presentation.
- `internal/ui` owns Bubble Tea state, rendering, tool jobs.
- `internal/textsafe` sanitizes untrusted remote and subprocess text shared by both layers.

UI depends on diagnostics; diagnostics do not depend on UI. Add network semantics under `internal/diagnostic`, interaction or rendering behavior under `internal/ui`.
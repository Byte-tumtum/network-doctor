# Network Doctor

[![CI](https://github.com/heymaikol/network-doctor/actions/workflows/ci.yml/badge.svg)](https://github.com/heymaikol/network-doctor/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/heymaikol/network-doctor)](https://github.com/heymaikol/network-doctor/releases/latest)
[![License: GPL-3.0-or-later](https://img.shields.io/github/license/heymaikol/network-doctor)](LICENSE)

**Find exactly where your connection breaks.** Network Doctor is a
cross-platform network troubleshooting TUI that turns interface, DNS, TCP,
TLS, HTTP, proxy, and path-MTU checks into one plain-English diagnosis.

![Network Doctor diagnosing github.com:443: the check list, a traceroute and mtr running concurrently, the filtered output viewer, a LAN scan, the SSH login form, the mtr report, toolbox mode, probe selection with --check, headless --json, and watch mode](assets/demo.gif)

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

Every Linux package — COPR, `.deb`, `.rpm`, `.apk` — installs two commands at the same version: `netdoc`, and `netdoc-sim`, the simulator behind [Challenge Mode](#think-you-can-beat-network-doctor). Confirm both:

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
- **QUIC path**: `Interface → QUIC / UDP 443`. It resolves the fixed
  connectivity endpoint itself and completes a real QUIC handshake, so a
  successful local UDP send cannot masquerade as reachability.
- **Proxy-egress path** (independent of both): `Interface → Internet (env
  proxy)`. Native probes deliberately bypass proxies, so this row reports the environment-configured proxy separately — a proxy-only corporate network reads "online via proxy" rather than offline.
- **Public-DNS path** (independent of system DNS): `Interface → DNS (public
  8.8.8.8)`. Failing to reach the third-party resolver is N/A; differing answers warn about split DNS or filtering but never fail the run on their own. `--public-dns` picks a different resolver, and `--public-dns ""` removes the row.
- **Encrypted-DNS path** (independent of both plaintext DNS rows): `Interface →
  DNS (encrypted DoH/DoT)`. Plain DNS and encrypted DNS are separate network capabilities, so this row hangs off the interface rather than under the DNS row — a network can carry ordinary DNS while blocking DoH and DoT.
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
| **QUIC / UDP 443** | A certificate-validated QUIC handshake negotiates HTTP/3 with the fixed connectivity endpoint | A timeout says only that this endpoint did not complete the UDP/443 exchange; browsers and applications normally fall back to TCP |
| **Internet (env proxy)** | The `HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY` proxy grants a tunnel | HTTP `CONNECT`; `socks5` resolves locally and sends an address, while `socks5h` sends the hostname for proxy-side DNS; N/A when no proxy is configured or `NO_PROXY` exempts the probe host |
| **DNS** | The host resolves to an IPv4 or IPv6 address (system resolution) | IP-literal targets are N/A; all A/AAAA records are retained |
| **DNS (public 8.8.8.8)** | A direct query to Google Public DNS provides a second opinion | N/A when outbound DNS is unavailable; disagreement is Warn, not Fail; `--public-dns` changes the resolver or removes the row |
| **DNS (encrypted DoH/DoT)** | A correlated DNS response arrives over DoH **or** over DoT | `NOERROR` or `NXDOMAIN` passes; a standard-query resolver error warns without claiming the transport is blocked; it never falls back to port 53 |
| **TCP** | A TCP connect to the target port succeeds | races A/AAAA records Happy-Eyeballs style (RFC 8305), pins the winner |
| **Path MTU** | the peer acknowledges some of a 24 KiB write | finds evidence consistent with an MTU/PMTU black hole — never a Fail, see below |
| **TLS** | The TLS handshake (SNI + cert verification) succeeds | certificate time, hostname, issuer, protocol, timeout, early-close, and TCP failures receive stable JSON causes |
| **HTTP** | Port 80 returns any HTTP response (incl. 3xx/4xx/5xx) | Independent HEAD after DNS, redirects off, proxy off |
| **HTTPS** | The selected TLS port returns any HTTP response | HEAD against the TLS-validated IP, redirects off, proxy off |
| **SSH/SMTP banner** | TCP connects (banner read best-effort) | bounded read; connected but silent → Warn (not a failure) |

### Path MTU without root

A path MTU smaller than the local interface's, on a path that also filters the ICMP that would say so, is the classic tunnel/VPN/PPPoE mystery: TCP connects, then the connection dies the moment either side sends a real packet. `ping` and `curl` can't tell you that, and confirming it normally means raw sockets and the DF flag.

The **Path MTU** row looks for it from an ordinary socket. The TCP handshake is the control: SYN/SYN-ACK are small enough to cross a narrowed link, so a completed connect already proves that small packets arrive. The probe then writes 24 KiB and asks the kernel how much of it the peer has acknowledged. Acknowledgement is the only proof of forward progress an ordinary socket can offer, and it is a strict one: TCP fills segments from the front of the payload, so nothing can be acknowledged unless a full-size packet crossed.

- some of the payload is acknowledged → full-size packets cross, **Pass**, with the connection's TCP MSS when the OS exposes it,
- the whole payload is written and none of it is acknowledged before the deadline → **Warn**, naming the evidence and an MSS/MTU experiment,
- the peer hangs up first → **N/A**: inconclusive, and it will not guess.

A completed write is deliberately not the test. Linux treats the send-buffer size as an accounting hint rather than a ceiling, so a socket reporting an 8 KiB send buffer will still swallow a 24 KiB write whole without a byte reaching the wire — which is exactly what a black hole looks like from userspace. Linux and macOS therefore read the socket's outstanding send queue directly. Windows exposes no equivalent query, so it falls back to inferring delivery from the send buffer and says so in the row: on that platform the row can miss a black hole, though the TLS/HTTP timeouts beside it still show up.

It deliberately never Fails: a peer that accepts the connection and then stops accepting data can stall the write the same way. A normal TCP socket also cannot discover the exact path MTU when ICMP feedback is filtered. The row therefore reports the bytes written and acknowledged, the TCP MSS when available, and the local interface MTU as context—never as a measured path MTU. Only when both this write and a protocol exchange time out does the overall verdict identify a probable network-path problem; certificate and other immediate protocol failures remain service failures.

The 24 KiB payload is inert, self-labelling filler; TLS targets get a record header in front so the TLS server reads the payload instead of resetting on the first byte. This is the only probe that sends bulk data — under `--watch` that is 24 KiB per pass, once every 5 seconds.

The captive-portal check in the **Internet (TCP egress)** row reaches a fixed third-party endpoint: one small plain-HTTP `GET` with no request body to Google-operated `connectivitycheck.gstatic.com/generate_204`, on each diagnostic pass. The **QUIC / UDP 443** row reuses that hostname and sends a small QUIC handshake to UDP/443; it validates the endpoint certificate and HTTP/3 negotiation but sends no HTTP request or application data. TCP/443 can work while UDP/443 is filtered, in which case browsers and applications normally fall back to TCP and the symptom may be slower startup rather than a total outage. Both checks ignore configured proxies and use the normal per-probe timeout. Under `--watch` they repeat with every pass, once every 5 seconds. The 24 KiB path-MTU write remains the only probe that sends bulk data.

The **DNS (encrypted DoH/DoT)** row reaches a second fixed third-party endpoint: Cloudflare-operated `cloudflare-dns.com`, dialed directly at `1.1.1.1` and `2606:4700:4700::1111` so no name has to be resolved to get there, while TLS still verifies the certificate for `cloudflare-dns.com` and HTTPS still addresses that host. Each pass sends one A query for `connectivitycheck.gstatic.com` over each transport: an RFC 8484 `POST` of a wire-format query with DNS ID 0 to `https://cloudflare-dns.com/dns-query`, and an RFC 7858 length-framed query with a random DNS ID to port 853. The row proves a **correlated DNS exchange**, not a connection or a particular answer: it checks the DNS header and echoed question (except where a valid `FORMERR` or `NOTIMP` response may omit it) but does not parse answer records or test the user's configured resolver. `NOERROR` and `NXDOMAIN` pass even with no answer records; a standard-query DNS error such as `SERVFAIL` or `REFUSED` warns because the resolver was reached but did not complete the query, and is never diagnosed as a network block. A TCP connect, a TLS handshake on `:853`, or an HTTP 2xx carrying anything else all fail. Either transport succeeding is enough — the two are alternative ways to reach encrypted DNS — and both are tried concurrently so a black-holed port cannot hide a working one. It never falls back to UDP or TCP port 53; the row exists to answer whether encrypted DNS itself works. It ignores configured proxies (an environment proxy carrying the request would prove nothing about encrypted DNS) and is bounded by the normal per-probe timeout.

When neither encrypted transport completes a verified DNS exchange while the plaintext DNS rows pass, netdoc reports that the resolver may be unavailable or the network may be blocking or interfering with DoH/DoT. It says no more than that on purpose: the probes can show that one worked and the other did not, but not that an operator intended it, and not what a particular browser did about it. When plaintext DNS is failing too, or the encrypted resolver returned a valid standard-query DNS error, nothing encrypted-specific is claimed.

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
netdoc --check dns,target_tcp,tls example.com  # run only these IDs and their prerequisites
netdoc --skip internet_tcp,quic_udp_443 example.com  # omit these probe branches
netdoc --check tls --skip target_tcp example.com  # a skipped prerequisite blocks TLS
netdoc --iface wg0 host # bind probe traffic to wg0's source address
netdoc --public-dns 9.9.9.9 host  # take the second opinion from Quad9 instead
netdoc --public-dns "" host       # drop the second opinion: no third-party resolver is queried
netdoc --no-history host          # don't read or save the target history file
```

`--timeout` overrides the per-check probe timeout; see `netdoc --help` for the default. `--watch` starts another pass five seconds after each run; in the TUI it shows the last 20 states plus a failure count for every check, and with `--json` it streams the same report on stdout, one compact JSON object per line, until the process is interrupted. Those lines carry an extra `ts` field (RFC 3339, UTC) and are otherwise the one-shot report unchanged — one-shot output stays pretty-printed, with no `ts`.

`--check` accepts comma-separated stable probe IDs and limits the run to those probes plus the prerequisite closure from the existing DAG. `--skip` removes IDs and any dependent probes whose prerequisites are then unavailable. Both flags are repeatable and combine by union; their argument order never changes row order. Unknown or empty IDs are rejected with exit `2`, before diagnostics start, and the error lists every valid stable ID. A known ID that does not apply to the current target is harmless: `--check` may select no rows, while `--skip` changes nothing. Omitted probes do not run and do not appear in the TUI or JSON. The same selection policy is retained by `--watch`, TUI restarts, and target changes.

`--iface` binds IPv4 and IPv6 probe connections and DNS lookups to the interface's first usable address of the matching family. IPv4-only and IPv6-only interfaces use only their available family; pass an exact local IP to restrict probes to that address's family.

The path drill-downs follow the same selection where the native tool supports it. On Linux and macOS, `ping`, `traceroute`, `mtr`, and `curl` use the selected interface name, or the selected address when `--iface` names an exact IP. Windows tools bind by source address instead: `pathping` and `curl.exe` use the resolved address, while `ping` and `tracert` can do so only for IPv6 destinations. `curl.exe` cannot bind by interface name, but can use that interface's resolved source address.

Address-only binding follows the literal target's family first, then the address selected by the target TCP check. With neither, a single-family selection is unambiguous and may be used; a dual-stack selection is left unbound rather than guessing. A missing matching source family is always left unbound.

Left deliberately unchanged and unbound: `dig` and `nslookup`, which query the system resolver (often a loopback stub) rather than the target; the `nmap` connect scan, whose `-S` is a spoofing option rather than documented connect-scan binding; and the SSH and SMTP handshake checks. Local-state tools (`ip route`, `ss`, `netstat`, `route print`) open no sockets. The LAN scan already follows the selection through the probe source address from which it derives its subnet.

`--public-dns` sets the resolver behind the second-opinion DNS row, defaulting to `8.8.8.8`. It takes an IP literal, IPv4 or IPv6 — a hostname is rejected with exit `2`, since resolving it would go through the very resolver the row exists to cross-check. An empty value (`--public-dns ""` or `--public-dns=`) removes the check: the row is absent from the TUI and from the JSON report, and no query is sent, which is what a strict egress policy or a privacy requirement needs. The value applies to `--watch` and `--json` runs and to target switches inside the TUI. Note that this flag governs only the DNS second opinion; the direct-egress row still connects to fixed anycast `:443` endpoints, the captive-portal check still reaches `connectivitycheck.gstatic.com`, and the encrypted-DNS row still reaches `cloudflare-dns.com`. A plaintext resolver IP is not a DoH/DoT provider, so `--public-dns` is never reinterpreted as one.

The target parser has two independent axes: **port** (explicit `:port` > scheme default > 443) and **protocol rows** (an explicit `http`/`https`/`ssh`/`smtp` scheme wins; otherwise it is inferred from the port — `443/8443`→HTTP+TLS+HTTPS, `80`→HTTP, `22`→SSH, `25/587`→SMTP). Hosts are validated against a strict allowlist; IPv6 literals are accepted bare (`::1`) or bracketed with a port (`[::1]:443`).

The TUI saves up to 50 recent targets between sessions in `$XDG_CONFIG_HOME/netdoc/history` (normally `~/.config/netdoc/history`) on Linux, `~/Library/Application Support/netdoc/history` on macOS, or `%AppData%\netdoc\history` on Windows. `--no-history` turns that off for one run: the file is neither read nor written, so the targets you type stay in that session only. It leaves an existing file untouched — exit `netdoc` and delete it to clear what is already saved.

| Key | Action |
|-----|--------|
| `↑`/`↓` (`k`/`j`) | select a probe row, or a device in the network map |
| `v` | run a LAN scan and show a network map of the local private `/24` (unprivileged `nmap`) |
| `enter` | set the selected map device as the new target, or open the current tool job's output |
| `/` (viewer) | filter the viewer to matching lines (`enter` commits, `esc` clears it, a second `esc` leaves) |
| `home`/`end`, `pgup`/`pgdn` (viewer) | jump to top/bottom (`end` re-enables follow) or page through the output |
| `y` / `w` (viewer) | copy / save the viewer's retained output (up to 5,000 lines; respects its filter) |
| `r` | restart with a new target |
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

On Windows, this password handoff requires OpenSSH_for_Windows 8.6p1 or newer. Network Doctor checks `ssh -V` before connecting; an older or unrecognized client leaves the form open with an explanation instead of launching `ssh` with an ignored password. Leave the password field blank on that client to have `ssh` ask on the terminal.

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

`status` is one of `PASS`, `WARN`, `FAIL`, `SKIP`, `N/A`. `target` is `null` in generic (no-target) mode. `ms` is the check's wall time truncated to milliseconds but floored at `1`, so `0` means the check never ran. Optional per-check fields (`cause`, `address_families`, `fix`, `addrs`, `selected_ip`, `source`, `iface`, `network`, `portal`, `attempts`) are omitted when empty. `internet_tcp.address_families` records the independently tested IPv4 and IPv6 state as `reachable` or `unreachable`; it is not inferred from a hostname dial that may fall back. Under `--iface` or an explicit source address, a family the selected source has no address for is never dialed and its key is omitted — untested, not unreachable; a selection leaving no usable family at all reports the whole row as `N/A`, as the QUIC and encrypted-DNS rows already do. A configured family whose path fails while the other succeeds warns with `ipv4_unreachable` or `ipv6_unreachable`. Failed QUIC, encrypted-DNS, proxy, and TLS checks populate `cause` so automation can distinguish failure stages without parsing `detail`; QUIC uses `timeout` or `quic_handshake_failure`, encrypted DNS uses `timeout` or `encrypted_dns_unavailable`, while TLS values include `certificate_expired`, `certificate_not_yet_valid`, `hostname_mismatch`, `untrusted_issuer`, `tls_handshake_failure`, `tcp_unreachable`, `timeout`, and `connection_closed`. On Linux, failed direct egress may use `no_default_route`, `gateway_unreachable`, `selected_path_failed`, or `preferred_route_failed`. The `portal` object marks detected HTTP interception and includes `redirect_url` only when the response supplied a valid HTTP(S) sign-in URL; the app displays that URL but never opens it. Field names and the status vocabulary are stable — safe to script against. Exit codes follow the table below (`ok: false` ⇒ exit `1`).

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

`netdoc-sim` and Challenge Mode are the exception: their backend is Linux namespaces and there is no other one, so macOS and Windows run [the published image](docs/simulation.md#running-it-in-a-container) on a Linux container runtime rather than a port. `netdoc` itself needs no container anywhere — diagnosing your own machine's network from inside one would diagnose the container.

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
against the injected fault. It is an unprivileged, Linux-only development tool
for deterministic regression testing; it does not alter the host network.

The simulator supports written scenarios, reproducible fault campaigns,
generated bug hunts, and triage of reproducible findings. Start with the
scenario catalog and run any name it prints:

```sh
netdoc-sim scenarios
netdoc-sim run broken-dns
```

Any [Linux package](#linux) installs `netdoc-sim` beside `netdoc`, and
`ghcr.io/heymaikol/netdoc-sim` carries the same pair for hosts that are not Linux
— the container is packaging around the Linux backend, never a second one.
Contributors testing an unreleased change build both from the clone instead, so
the simulator runs the netdoc built next to it:

```sh
go build -o netdoc . && go build -o netdoc-sim ./cmd/netdoc-sim
./netdoc-sim run broken-dns
```

`netdoc-sim scenarios` is the source of truth for the complete set of shipped
built-in scenarios rather than the README. See the **[complete simulator
guide](docs/simulation.md)** for setup and requirements, commands and workflows,
scenario authoring, campaigns, hunt and triage usage, reports, troubleshooting,
tests, and limitations.

### Think you can beat Network Doctor?

Challenge Mode drops you into a deliberately broken network without telling you
what's wrong. Investigate it with the tools you'd normally use, commit to your
diagnosis, then let Network Doctor take a shot at the exact same problem. Both
answers are judged against the simulator's independently observed ground truth.

On **macOS, Windows or Linux**, one container image is the whole install — no
clone, no Go toolchain, no Linux namespace knowledge, and nothing on your real
network is touched:

```sh
docker run --rm -it --cap-add SYS_ADMIN ghcr.io/heymaikol/netdoc-sim:latest challenge
```

That is the real Linux namespace simulator running inside a Linux container, not
a macOS or Windows imitation of it. `podman run --rm -it` works too, and does not
need the capability. See [Running it in a
container](docs/simulation.md#running-it-in-a-container) for the specific-id,
JSON and privilege details, and for what that one flag is and is not.

On Linux, any [package](#linux) installs `netdoc-sim` and it runs natively:

```sh
netdoc-sim challenge                  # draw a challenge and play it
netdoc-sim challenge -difficulty hard
netdoc-sim challenge -id V3-8F42C1    # play the one a friend sent you
```

You land in a shell inside the broken machine. Use `ping`, `dig`, `curl`, `ip
route`, `ss`, `traceroute`, `nc` — whatever you'd reach for on a real
call-out. Type `exit` when you're ready, pick your diagnosis from the menu, and
the reveal shows the ground truth, the evidence behind it, your answer, Network
Doctor's answer, and who got it right:

```text
Result
  YOU BEAT NETWORK DOCTOR

Human investigation: 1m 47s
Network Doctor run:  3s
```

It ends with a block you can paste anywhere. It carries the challenge id and two
check marks and never names the fault, so sharing your result doesn't spoil the
puzzle for whoever plays it next:

```text
Network Doctor Challenge V3-8F42C1 (hard)
Me:             ✓
Network Doctor: ✗
YOU BEAT NETWORK DOCTOR in 1m 47s
Your turn: netdoc-sim challenge -id V3-8F42C1
```

Everything is local and reproducible: no account, no server, no leaderboard. A
challenge id is the whole puzzle, so the same id is the same broken network on
anyone's machine — natively on Linux, or through the image on any host. Same
requirements as the rest of the simulator: a Linux kernel, unprivileged, and
nothing on your real network is touched. See the [Challenge Mode
guide](docs/simulation.md#challenge-mode) for how scoring works and why Network
Doctor never gets to see the answer either.

## Development

The package layout and the dependency rules between the packages are documented
in [CONTRIBUTING.md](CONTRIBUTING.md#development).

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

# Network Doctor reference

This file is the authoritative reference for `netdoc`'s probes, flags, and
output contracts, the detail that changes in the same commit as the code
beside it. [README.md](../README.md) is the landing page; the wiki's
[How Network Doctor Works](https://github.com/heymaikol/network-doctor/wiki/How-Network-Doctor-Works)
and [Understanding Your Diagnosis](https://github.com/heymaikol/network-doctor/wiki/Understanding-Your-Diagnosis)
explain why these probes are built this way and what a result means for you.

## How it diagnoses

Probes form a **dependency graph with independent branches**, so an unrelated failure never hides a working one:

- **Direct-egress path** (independent of DNS): `Interface → Internet (TCP
  egress)`. Always runs, so "DNS down but internet up" stays diagnosable.
- **QUIC path**: `Interface → QUIC / UDP 443`. It resolves the fixed
  connectivity endpoint itself and completes a real QUIC handshake, so a
  successful local UDP send cannot masquerade as reachability.
- **Proxy-egress path** (independent of both): `Interface → Internet (env
  proxy)`. Native probes deliberately bypass proxies, so this row reports the environment-configured proxy separately: a proxy-only corporate network reads "online via proxy" rather than offline.
- **Public-DNS path** (independent of system DNS): `Interface → DNS (public
  8.8.8.8)`. Failing to reach the third-party resolver is N/A; differing answers warn about split DNS or filtering but never fail the run on their own. `--public-dns` picks a different resolver, and `--public-dns ""` removes the row.
- **Encrypted-DNS path** (independent of both plaintext DNS rows): `Interface →
  DNS (encrypted DoH/DoT)`. Plain DNS and encrypted DNS are separate network capabilities, so this row hangs off the interface rather than under the DNS row: a network can carry ordinary DNS while blocking DoH and DoT.
- **Wi-Fi metadata path**: `Interface → Wi-Fi network`. SSID discovery runs beside network checks, so slow OS lookup never delays them.
- **Plain HTTP path**: `Interface → DNS → HTTP :80`.
- **Selected target path**: `Interface → DNS → TCP → TLS → HTTPS` for secure web targets, or applicable protocol row for other ports.
- **Path-MTU branch** (hangs off connect, not off any protocol): `TCP → Path
  MTU`. Black hole breaks SSH and SMTP exactly as thoroughly as TLS.

Each row lands in one of five states: **✓ Pass**, **! Warn** (reachable but degraded: high latency, some addresses failing, an ambiguous source interface), **✗ Fail**, **⊘ Skip** (a prerequisite failed), or **– N/A** (doesn't apply, e.g. DNS on an IP literal). Warn never counts as a failure.

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
| **Path MTU** | the peer acknowledges some of a 24 KiB write | finds evidence consistent with an MTU/PMTU black hole, never a Fail, see below |
| **TLS** | The TLS handshake (SNI + cert verification) succeeds | certificate time, hostname, issuer, protocol, timeout, early-close, and TCP failures receive stable JSON causes |
| **HTTP** | Port 80 returns any HTTP response (incl. 3xx/4xx/5xx) | Independent HEAD after DNS, redirects off, proxy off |
| **HTTPS** | The selected TLS port returns any HTTP response | HEAD against the TLS-validated IP, redirects off, proxy off |
| **SSH/SMTP banner** | TCP connects (banner read best-effort) | bounded read; connected but silent → Warn (not a failure) |

### Path MTU without root

A path MTU smaller than the local interface's, on a path that also filters the ICMP that would say so, breaks TCP the moment either side sends a real packet, the classic tunnel/VPN/PPPoE mystery that `ping` and `curl` can't diagnose, and confirming it normally means raw sockets and the DF flag.

The **Path MTU** row finds it from an ordinary socket instead. The TCP handshake is the control: SYN/SYN-ACK are small enough to cross a narrowed link, so a completed connect already proves small packets arrive. The probe then writes 24 KiB and asks the kernel how much of it the peer has acknowledged, the only proof of forward progress an ordinary socket can offer, and a strict one, since TCP fills segments from the front of the payload.

- some of the payload is acknowledged → full-size packets cross, **Pass**, with the connection's TCP MSS when the OS exposes it,
- the whole payload is written and none of it is acknowledged before the deadline → **Warn**, naming the evidence and an MSS/MTU experiment,
- the peer hangs up first → **N/A**: inconclusive, and it will not guess.

A completed write is deliberately not the test: Linux treats the send-buffer size as an accounting hint rather than a ceiling, so an 8 KiB send buffer can swallow a 24 KiB write whole without a byte reaching the wire. Linux and macOS read the socket's outstanding send queue directly instead. Windows exposes no equivalent query and falls back to inferring delivery from the send buffer, so on that platform the row can miss a black hole, though the TLS/HTTP timeouts beside it still show up.

It deliberately never Fails: a peer that accepts the connection and then stops accepting data stalls the write the same way, and a normal socket cannot discover the exact path MTU when ICMP feedback is filtered. The row reports bytes written and acknowledged, the TCP MSS when available, and the local interface MTU as context, never as a measured path MTU. Only when both this write and a protocol exchange time out does the overall verdict identify a probable network-path problem; certificate and other immediate protocol failures remain service failures.

The 24 KiB payload is inert, self-labelling filler; TLS targets get a record header in front so the TLS server reads it instead of resetting on the first byte. This is the only probe that sends bulk data: under `--watch` that is 24 KiB once every 5 seconds.

Two other fixed third-party endpoints are involved, both bounded by the normal per-probe timeout and ignoring configured proxies. The **Internet (TCP egress)** row's captive-portal check sends one small plain-HTTP `GET` with no body to `connectivitycheck.gstatic.com/generate_204` on each pass; **QUIC / UDP 443** reuses that host for a certificate-validated QUIC/HTTP-3 handshake on UDP/443 with no application data, so a timeout there says only that this endpoint didn't complete the exchange, not that a firewall was responsible. **DNS (encrypted DoH/DoT)** dials `cloudflare-dns.com` directly at `1.1.1.1`/`2606:4700:4700::1111`, sending one A query for `connectivitycheck.gstatic.com` per pass over each transport: an RFC 8484 `POST` with DNS ID 0 to `/dns-query`, and an RFC 7858 length-framed query with a random DNS ID on port 853. It proves a **correlated DNS exchange**, not a particular answer: it checks the DNS header and echoed question (except where a valid `FORMERR`/`NOTIMP` response may omit it) but does not parse answer records or test the user's configured resolver. `NOERROR`/`NXDOMAIN` pass even with no answer records; a standard-query error such as `SERVFAIL` warns rather than fails, since the resolver was reached but didn't complete the query. Either transport succeeding is enough, both are tried concurrently, and it never falls back to plain DNS.

No verdict depends on ICMP: a failed `ping` proves nothing and a successful one proves less than a TCP connect, so RTT is measured from the TCP-connect handshake instead, with no ICMP and no root. `ping` remains available as a drill-down tool. The source IP and interface are read from the winning connection's `LocalAddr`, with a UDP-connect fallback (which sends no packets) for path identity on failure. Every probe is bounded by a 4-second timeout.

## Usage details

`--timeout` overrides the per-check probe timeout; see `netdoc --help` for the default. `--watch` starts another pass five seconds after each run; in the TUI it shows the last 20 states plus a failure count for every check, and with `--json` it streams the same report on stdout, one compact JSON object per line, until the process is interrupted. Those lines carry an extra `ts` field (RFC 3339, UTC) and are otherwise the one-shot report unchanged; one-shot output stays pretty-printed, with no `ts`.

`--check` accepts comma-separated stable probe IDs and limits the run to those probes plus the prerequisite closure from the existing DAG. `--skip` removes IDs and any dependent probes whose prerequisites are then unavailable. Both flags are repeatable and combine by union; their argument order never changes row order. Unknown or empty IDs are rejected with exit `2`, before diagnostics start, and the error lists every valid stable ID. A known ID that does not apply to the current target is harmless: `--check` may select no rows, while `--skip` changes nothing. Omitted probes do not run and do not appear in the TUI or JSON. The same selection policy is retained by `--watch`, TUI restarts, and target changes.

`--iface` binds IPv4 and IPv6 probe connections and DNS lookups to the interface's first usable address of the matching family. IPv4-only and IPv6-only interfaces use only their available family; pass an exact local IP to restrict probes to that address's family.

The path drill-downs follow the same selection where the native tool supports it. On Linux and macOS, `ping`, `traceroute`, `mtr`, and `curl` use the selected interface name, or the selected address when `--iface` names an exact IP. Windows tools bind by source address instead: `pathping` and `curl.exe` use the resolved address, while `ping` and `tracert` can do so only for IPv6 destinations. `curl.exe` cannot bind by interface name, but can use that interface's resolved source address.

Address-only binding follows the literal target's family first, then the address selected by the target TCP check. With neither, a single-family selection is unambiguous and may be used; a dual-stack selection is left unbound rather than guessing. A missing matching source family is always left unbound.

Left deliberately unchanged and unbound: `dig` and `nslookup`, which query the system resolver (often a loopback stub) rather than the target; the `nmap` connect scan, whose `-S` is a spoofing option rather than documented connect-scan binding; and the SSH and SMTP handshake checks. Local-state tools (`ip route`, `ss`, `netstat`, `route print`) open no sockets. The LAN scan already follows the selection through the probe source address from which it derives its subnet.

`--public-dns` sets the resolver behind the second-opinion DNS row, defaulting to `8.8.8.8`. It takes an IP literal, IPv4 or IPv6; a hostname is rejected with exit `2`, since resolving it would go through the very resolver the row exists to cross-check. An empty value (`--public-dns ""` or `--public-dns=`) removes the check: the row is absent from the TUI and from the JSON report, and no query is sent, which is what a strict egress policy or a privacy requirement needs. The value applies to `--watch` and `--json` runs and to target switches inside the TUI. Note that this flag governs only the DNS second opinion; the direct-egress row still connects to fixed anycast `:443` endpoints, the captive-portal check still reaches `connectivitycheck.gstatic.com`, and the encrypted-DNS row still reaches `cloudflare-dns.com`. A plaintext resolver IP is not a DoH/DoT provider, so `--public-dns` is never reinterpreted as one.

The target parser has two independent axes: **port** (explicit `:port` > scheme default > 443) and **protocol rows** (an explicit `http`/`https`/`ssh`/`smtp` scheme wins; otherwise it is inferred from the port: `443/8443`→HTTP+TLS+HTTPS, `80`→HTTP, `22`→SSH, `25/587`→SMTP). Hosts are validated against a strict allowlist; IPv6 literals are accepted bare (`::1`) or bracketed with a port (`[::1]:443`).

The TUI saves up to 50 recent targets between sessions in `$XDG_CONFIG_HOME/netdoc/history` (normally `~/.config/netdoc/history`) on Linux, `~/Library/Application Support/netdoc/history` on macOS, or `%AppData%\netdoc\history` on Windows. `--no-history` turns that off for one run: the file is neither read nor written, so the targets you type stay in that session only. It leaves an existing file untouched, so exit `netdoc` and delete it to clear what is already saved.

## Drill-down tools

Each diagnosis row is *evidence*; when you want proof, run the real tools as cancellable streaming jobs: several run at once, and `tab` switches between the live ones. A contextual toolbox shows the tools available for the current target with their hotkeys, greying out missing binaries with an install hint. Output is bounded and sanitized (no terminal-escape injection from a hostile server); reports include version/OS metadata plus each job's command, status, duration, and last 15 output lines.
Review your local copy before sharing, since tool evidence may contain sensitive data.

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

`S` logs in to the current target, the machine the checks are about, so it needs one (`r` sets it). The form asks for the three things that are yours rather than the target's: `tab` moves between fields, `←`/`→` picks the key, `enter` connects, and `esc` backs out.

```
╭────────────────────────────────────────────────────╮
│ SSH login to 192.168.1.50:2222                     │
│   Username  mplaczek                               │
│ ▸ Key       id_rsa  (3 of 4)  ←/→                  │
│   Password  *******                                │
╰────────────────────────────────────────────────────╯
```

Unlike the drill-down tools, this is not a bounded job: `netdoc` suspends itself and gives `ssh` the real terminal, so the session is fully interactive and anything the form left blank (a key passphrase, a host-key check, a 2FA code) is asked by `ssh` on screen. When the session ends the TUI comes back with `ssh`'s stderr in the job pane, so a `Permission denied` is still readable afterwards instead of being painted over.

The form fills in `ssh` options, nothing more:

| Field | Effect |
|-------|--------|
| (host) | the target, plus `-p` when it named a non-default port |
| Username | `-l <login>`, not `login@host`, so a name starting with `-` stays a name |
| Key | `-i <path> -o IdentitiesOnly=yes`, so a loaded agent can't spend the server's auth attempts before this key is tried |
| Password | see below |

The key list is the private keys in `~/.ssh`, each recognized by its `.pub` half sitting next to it, and no key file is ever opened or parsed. `none` (the first entry) leaves key selection to `ssh`, i.e. to the agent and `ssh_config`, which is also where a key kept outside `~/.ssh` belongs.

The typed password is echoed as dots and never reaches a command line, notice, or report. It is passed to `ssh` through the environment (`SSH_ASKPASS`), where `ssh` re-executes `netdoc` as its askpass helper and reads the secret from the helper's stdout. What that buys: the secret stays out of argv, which every process on the machine can read, and out of your shell history, though not out of memory. The password field is cleared once connecting starts, but by then the value has been copied into `ssh`'s environment, where it stays until `ssh` exits and is inherited by the whole subtree `ssh` starts: `ProxyCommand`, `LocalCommand`, and `ProxyJump`'s own `ssh`. On Linux that environment is readable through `/proc` by your own processes and by root; anything your `ssh_config` runs sees it too. `-o NumberOfPasswordPrompts=1` stops `ssh` re-offering a rejected one.

On Windows, this password handoff requires OpenSSH_for_Windows 8.6p1 or newer. Network Doctor checks `ssh -V` before connecting; an older or unrecognized client leaves the form open with an explanation instead of launching `ssh` with an ignored password. Leave the password field blank on that client to have `ssh` ask on the terminal.

Because forced askpass routes *every* prompt to the helper, the helper answers only password and passphrase prompts and refuses the rest. So the first connection to an unknown host, where `ssh` asks you to verify the host key, needs one run with the password field blank, which puts that question back on your terminal where it belongs.

The whole `ssh` subtree inherits the askpass setting, so with `ProxyJump` in your `ssh_config` the jump host's `ssh` asks the helper too. The prompt names the machine it is asking for (`user@host's password:`), and the helper answers only when that host matches the target, so the jump host's prompt goes back to your terminal. Prompts naming no host (a key passphrase, a PAM keyboard-interactive `Password:`) are still answered on a direct connection, where only one machine can be asking; refusing those would break ordinary logins. Add a proxy and they name no host *and* could be either end, so the helper refuses all of them. Wording is no help there, since keyboard-interactive text is written by the far end, so a jump host can call its question a passphrase, and that run wants the password field blank anyway, which puts the prompt back on your terminal. All of this depends on netdoc being able to read your resolved config (`ssh -G`); when that lookup fails there is no way to tell whose prompt is whose, so password-assisted login is refused outright and the form says so.

### JSON output

`--json` runs the same probe DAG headless, with no TUI, and prints one JSON document to stdout:

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

`status` is one of `PASS`, `WARN`, `FAIL`, `SKIP`, `N/A`. `target` is `null` in generic (no-target) mode. `ms` is the check's wall time truncated to milliseconds but floored at `1`, so `0` means the check never ran. Optional per-check fields (`cause`, `address_families`, `fix`, `addrs`, `selected_ip`, `source`, `iface`, `network`, `portal`, `attempts`) are omitted when empty. `internet_tcp.address_families` records the independently tested IPv4 and IPv6 state as `reachable` or `unreachable`; it is not inferred from a hostname dial that may fall back. Under `--iface` or an explicit source address, a family the selected source has no address for is never dialed and its key is omitted, meaning untested rather than unreachable; a selection leaving no usable family at all reports the whole row as `N/A`, as the QUIC and encrypted-DNS rows already do. A configured family whose path fails while the other succeeds warns with `ipv4_unreachable` or `ipv6_unreachable`. Failed QUIC, encrypted-DNS, proxy, and TLS checks populate `cause` so automation can distinguish failure stages without parsing `detail`; QUIC uses `timeout` or `quic_handshake_failure`, encrypted DNS uses `timeout` or `encrypted_dns_unavailable`, while TLS values include `certificate_expired`, `certificate_not_yet_valid`, `hostname_mismatch`, `untrusted_issuer`, `tls_handshake_failure`, `tcp_unreachable`, `timeout`, and `connection_closed`. Failed direct egress may use `no_default_route`, `gateway_unreachable`, `selected_path_failed`, or `preferred_route_failed`, read from the local routing and neighbor tables on Linux, macOS, and Windows. macOS route entries carry no preference metric, so `preferred_route_failed` comes only from Linux and Windows, and `gateway_unreachable` needs a neighbor cache entry that exists and shows the next hop unresolved, so an unseen next hop leaves the weaker `selected_path_failed` rather than a guess. The `portal` object marks detected HTTP interception and includes `redirect_url` only when the response supplied a valid HTTP(S) sign-in URL; the app displays that URL but never opens it. Field names and the status vocabulary are stable, so they are safe to script against. Exit codes follow the table in [README.md](../README.md#exit-codes) (`ok: false` ⇒ exit `1`).

Resolver failures can add `dns_timeout` or `dns_temporary_failure`; a target TCP
check whose every attempted address explicitly refuses the connection adds
`connection_refused`; a peer that accepted TCP before resetting adds
`connection_reset`. These are optional additions to the existing check objects.

`fix` is the one-line remedy netdoc prints as that row's `Fix:` line, on a Warn row as well as a failed one, and is omitted where there is nothing useful to suggest. Some hints name the thing to change by whatever the host OS calls it, so one failure reads differently per platform: name resolution failing on Linux gives

<!-- netdoc-output -->
**"name resolution failing: check /etc/resolv.conf / DNS"**

while macOS points at System Settings and Windows at `ipconfig /all`. Others, such as the certificate and routing hints, carry no OS-specific wording at all.

`failed_stage` names the first check that failed (`dns`, `target_tcp`, `tls`, …) and is omitted when none did, which is enough to route a bug report without reading any prose.

`--json --watch` prints the same document once per pass, compacted onto a single line (NDJSON), five seconds apart, until the process is interrupted, for the intermittent failure you can't sit and watch:

```sh
netdoc --json --watch github.com | jq -c 'select(.ok | not) | {ts, failed_stage, summary}'
```

Those lines add a `ts` field (RFC 3339, UTC) saying when the pass ran; nothing else changes. The exit code is the last completed pass's.

`verdict` is the summary as a machine-readable class, for the question a script actually asks: *is my network broken, or is theirs?*

| `verdict` | Meaning |
|---|---|
| `ok` | Every check passed |
| `degraded` | Everything asked for works, but some rung is impaired: high latency, a proxy-only network, direct egress blocked while the target is fine |
| `dns` | The name did not resolve |
| `network` | **The path is unavailable**: the link is down, there's no egress, or the target is unreachable with nothing else proving the network usable |
| `service` | **The path works, the far end does not**: TCP refused while the general internet is reachable, or TLS/HTTP/banner failing on top of a good connection |
| `incomplete` | A check has no result (the chain did not finish) |

The `network`/`service` split is decided by evidence, not guesswork: an unreachable target is only blamed on the service when direct egress independently succeeded. With no working egress to compare against, netdoc says `network` rather than accusing a host it never reached.

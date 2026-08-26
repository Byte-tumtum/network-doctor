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

## Peer diagnosis

Peer mode is a separate, headless two-ended diagnosis. It does not run the
ordinary target DAG twice or concatenate two reports. One authenticated control
connection coordinates independent TCP, TLS, and application-payload attempts
in both directions, exchanges those observations as data, and runs one combined
truth table on each machine.

### Pairing workflow

The listening machine binds one exact local unicast address. Port zero asks the
kernel to select a temporary port:

```sh
netdoc --peer-listen 192.168.1.20:0
```

To offer both families, repeat the flag once:

```sh
netdoc \
  --peer-listen 192.168.1.20:4242 \
  --peer-listen '[2001:db8:1234::20]:4242'
```

The listener prints each direct endpoint and one `ndp1.` pairing string. On the
other machine, run:

```sh
netdoc --peer-connect
```

Paste the string at the `Temporary pairing string:` prompt. On a terminal the
prompt disables echo. The credential is read from standard input, never argv,
the target history, a file, or an environment variable. A script may pipe one
line to the prompt, but then owns the security of that pipe.

`--iface` may accompany `--peer-connect`; it supplies the connector's source
address and reverse listener address for each available family. The listening
form already names exact bind addresses, so combining `--iface` with
`--peer-listen` is rejected. Peer mode cannot be combined with a target,
`--watch`, `--toolbox`, `--check`, `--skip`, `--public-dns`, `--no-history`,
`--keys`, or `--save`. It does not enter the TUI.

The listener waits at most five minutes for one authenticated control session.
After connection, the complete exchange is bounded to one minute. `--timeout`
sets each dial, TLS handshake, frame read, and write budget as it does for an
ordinary probe, but peer mode caps it at 30 seconds. The two control reads that
wait for peer evidence allow three times that budget because the peer must
finish its bounded connection, TLS, and application phases first; the one-minute
session limit remains the hard cap.
Ctrl+C and SIGTERM cancel pending accepts, dials, handshakes, reads, and writes
and close all listeners and connections.

### Authentication and encryption

Every listening run generates, with `crypto/rand`:

- a fresh Ed25519 self-signed server certificate,
- a fresh 256-bit session token,
- fresh request nonces and fixed-payload challenges.

The pairing string contains protocol version 1, its expiration, at most one
IPv4 and one IPv6 direct endpoint, the certificate's SHA-256 pin, and the token.
It uses only URL-safe printable characters and is easy to paste, but it is a
secret until the session ends. Do not post it in a ticket or chat archive.

TLS 1.3 encrypts every control and probe connection. The connector accepts only
the exact pinned certificate, and the listener compares the decoded token in
constant time before accepting a control or probe request. Each authenticated
request needs a fresh 128-bit nonce; repeats are rejected. One listener accepts
one control connection, no more than eight concurrent connections, and no more
than eight authenticated nonces. Excess sockets are closed without ending the
pending session. A wrong token does not consume the one valid session. A
stopped or expired listener makes an old pairing string useless.

An unauthenticated client can temporarily occupy all eight TLS handshake slots
until the per-operation timeout. A legitimate connection is rejected while all
slots are occupied, but the listener remains alive and accepts it after a slot
expires or closes. This availability limit prevents unbounded socket and
goroutine growth; authentication and diagnostic state remain unaffected.

The token and certificate pin never appear in the peer result, ordinary report,
or returned error. Listener readiness and the pairing string go to stderr in
both output modes so stdout contains only the final result. Stderr is therefore
a deliberate secret-bearing pairing channel for that invocation and must be
handled accordingly.

### Wire protocol

Protocol version 1 uses TLS-protected messages framed by a four-byte
big-endian length followed by UTF-8 JSON. JSON was chosen because it is
deterministic for the defined structs, inspectable in tests, and does not add a
serialization dependency. Go `gob` is not used.

Every message carries `version` and a fixed `type`:

| Type | Direction | Purpose |
|------|-----------|---------|
| `hello` | connector to listener | authenticate the control connection, echo a random challenge, and offer the connector's bounded reverse endpoints |
| `hello_ok` | listener to connector | prove the listener completed the authenticated application exchange and identify itself |
| `probe` / `probe_ok` | either test direction | authenticate one family-specific connection and echo a fixed 32-byte random payload |
| `evidence` | each side once | exchange at most two structural observations, one per family |
| `done` | connector to listener | confirm both sides received the evidence before closing |

Frames are limited to 16 KiB. Endpoint and peer-name fields are bounded,
endpoint offers and evidence collections contain at most two entries, each
session has a fixed message sequence, and JSON with unknown fields, trailing
values, malformed encoding, an unknown type, or a different version is
rejected. Reads allocate only after checking the frame length. Peer strings are
sanitized before display. There is no message for a filename, command, target
port range, shell input, configuration change, or arbitrary diagnostic action.

The listener makes at most one bounded TLS probe per address family to the port
offered by the authenticated connector. It replaces the offered IP with the
source IP actually observed on an authenticated connection in that family. If
it has not observed that family, it records `peer_address_unverified` and does
not dial the offered address. A peer therefore cannot select another host as a
probe target.

Version 1 is intended to remain compatible across releases. A release may add
optional result fields without changing the wire. Any incompatible message or
semantic change requires a new protocol version and pairing prefix; version 1
peers reject it instead of attempting a downgrade. There is no cross-version
negotiation in the first protocol.

### Evidence and diagnoses

The authenticated control channel is recorded separately from the diagnostic
attempts. Each independent attempt records:

- semantic direction (`listener_to_connector` or
  `connector_to_listener`),
- IPv4 or IPv6,
- actual source and destination socket addresses where known,
- `PASS`, `FAIL`, or `N/A`,
- cross-platform cause such as `connection_refused`, `timeout`, or
  `unreachable`,
- whether TCP connected,
- whether the pinned TLS peer authenticated,
- whether the 32-byte application payload completed,
- elapsed milliseconds.

This supports the following combined diagnosis IDs:

| ID | What the evidence proves |
|----|--------------------------|
| `peer_bidirectional_ok` | authenticated small application traffic passed in both directions |
| `peer_directional_failure` | at least one direction passed and the other failed; wording names the traffic direction, not a device firewall |
| `peer_symmetric_failure` | the independent bounded attempts failed in both directions while the control channel remained available |
| `peer_address_family_asymmetry` | one tested family carried peer traffic while the other tested family did not |
| `peer_listener_local_only` | the failed destination for the tested family was definitively bound only to loopback |
| `peer_application_failure` | TCP and pinned TLS succeeded but the fixed application payload did not |
| `peer_security_failure` | TCP connected but the endpoint did not authenticate as the paired TLS peer |
| `peer_incomplete_evidence` | too little independent evidence exists to distinguish a directional or endpoint failure |

`connection_refused` proves that the tested address actively refused the TCP
connection. It does not distinguish no listener from an active rejecting
filter. A timeout or unreachable result does not distinguish endpoint
filtering, routing, address translation, a stale address, or another
reachability failure. Directional and address-family diagnoses therefore set
`ambiguous: true` and list the remaining explanations. They never claim a
firewall, NAT, or routing root cause without independent proof.

Peer version 1 exchanges only the fixed 32-byte challenge plus its small
protocol messages. It does not reuse the ordinary 24 KiB Path MTU probe because
the peer protocol's own reader changes the control evidence that probe relies
on. It cannot diagnose "small succeeds, large stalls" and makes no MTU or PMTU
claim. It also does no traceroute orchestration, packet capture, raw sockets,
port scanning, remote shell, or repair.

### Direct-connect and privacy limits

There is no Network Doctor service, hosted rendezvous, relay, account,
telemetry, UPnP, NAT-PMP, or automatic firewall change. The connector must
reach at least one address printed by `--peer-listen`. If the listener is behind
NAT, its operator must already have a directly reachable address and port; peer
mode does not create one. The reverse test uses the connector's temporary
listener and the source address the listener actually observed for that family.
An unobserved family is not dialed. A NAT or stateful filter may still prevent
that new reverse connection. The result says so without pretending to know
which device or rule caused it. A relay could later carry the same evidence
messages without changing their semantics, but version 1 implements no relay
transport.

The peers exchange their sanitized host names, the temporary listener
addresses they offer, the actual local and remote socket addresses seen during
the session, address families, phase outcomes, and timing. A reverse address
that could not be verified is recorded as `N/A` with cause
`peer_address_unverified`. This can reveal
private addresses, public NAT addresses, interface choices, and host names.
Nothing is sent anywhere except the paired direct endpoints, and no telemetry
exists, but review a saved result before sharing it.

### Peer JSON output

`--json` in peer mode emits `netdoc.peer.v1`, not the ordinary report schema:

```json
{
  "schema": "netdoc.peer.v1",
  "version": "1.2.3",
  "protocol_version": 1,
  "local": {
    "role": "connector",
    "name": "machine-b",
    "listen_addresses": ["192.168.1.21:53122"],
    "observed_address": "192.168.1.21:49018"
  },
  "remote": {
    "role": "listener",
    "name": "machine-a",
    "listen_addresses": ["192.168.1.20:4242"],
    "observed_address": "192.168.1.20:4242"
  },
  "channel": {
    "established": true,
    "family": "ipv4",
    "local": "192.168.1.21:49018",
    "remote": "192.168.1.20:4242",
    "ms": 3
  },
  "observations": [
    {
      "direction": "listener_to_connector",
      "family": "ipv4",
      "source": "192.168.1.20:49410",
      "destination": "192.168.1.21:53122",
      "status": "PASS",
      "tcp_connected": true,
      "tls_authenticated": true,
      "application_traffic": true,
      "payload_bytes": 32,
      "ms": 2
    },
    {
      "direction": "listener_to_connector",
      "family": "ipv6",
      "status": "N/A",
      "cause": "family_unavailable",
      "tcp_connected": false,
      "tls_authenticated": false,
      "application_traffic": false,
      "payload_bytes": 0,
      "ms": 0
    },
    {
      "direction": "connector_to_listener",
      "family": "ipv4",
      "source": "192.168.1.21:49022",
      "destination": "192.168.1.20:4242",
      "status": "PASS",
      "tcp_connected": true,
      "tls_authenticated": true,
      "application_traffic": true,
      "payload_bytes": 32,
      "ms": 2
    },
    {
      "direction": "connector_to_listener",
      "family": "ipv6",
      "status": "N/A",
      "cause": "family_unavailable",
      "tcp_connected": false,
      "tls_authenticated": false,
      "application_traffic": false,
      "payload_bytes": 0,
      "ms": 0
    }
  ],
  "diagnosis": {
    "id": "peer_bidirectional_ok",
    "verdict": "ok",
    "summary": "Authenticated peer traffic succeeds in both directions.",
    "evidence": ["listener_to_connector/ipv4", "connector_to_listener/ipv4"],
    "ambiguous": false
  },
  "ok": true
}
```

`observations` is always ordered listener-to-connector IPv4, listener-to-
connector IPv6, connector-to-listener IPv4, connector-to-listener IPv6. An
unavailable family has an explicit `N/A` observation with
`cause: "family_unavailable"`. The two machines reverse only `local` and
`remote`; directions keep their semantic names. Field names, ordering rules,
status/cause values, diagnosis IDs, and the meaning of `ok` are stable for this
schema. `ok` is true only when small authenticated traffic passes in both
directions. A diagnostic failure exits `1`; bad peer CLI arguments or an
unreadable pairing input exit `2`. Existing ordinary exit meanings and JSON
fields are unchanged.

## Drill-down tools

Press `e` after a completed diagnosis to replace the focused Details panel with
its causal explanation. The view separates observations supporting the answer,
observations that rule out or contradict alternatives, and relevant checks that
were not evaluated. Press `e` again for the ordinary row details. The full key
cheatsheet behind `?` is generated from the same binding, so custom key presets
cannot make dispatch and help disagree.

Each diagnosis row is *evidence*; when you want proof beyond the built-in observations, run the real tools as cancellable streaming jobs: several run at once, and `tab` switches between the live ones. A contextual toolbox shows the tools available for the current target with their hotkeys, greying out missing binaries with an install hint. Output is bounded and sanitized (no terminal-escape injection from a hostile server); reports include version/OS metadata plus each job's command, status, duration, and last 15 output lines.
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

### Local devices

`v` answers "I cannot reach the printer" in two steps, so neither one has to be known in advance. The first is the map: unprivileged `nmap -sn` across the source address's `/24`, which finds a device only if it accepts or refuses a TCP connect on port 80 or 443. This machine is listed separately rather than as a device to diagnose, and an address gains a name when mDNS, reverse DNS, or an `ssh_config` alias supplies one.

`enter` on a device opens it and asks what it answers on: one round of ordinary TCP connects, run in parallel inside a single probe timeout, against a fixed list of fifteen ports (21, 22, 23, 53, 80, 443, 445, 515, 631, 2049, 3389, 5900, 8080, 8443, 9100). This is a chooser, not a scan: the list is fixed rather than a range, and nothing is sent after the connect, so the name beside a port ("IPP", "JetDirect") is that port's registered service name and not a claim about what the device is.

`enter` on a service makes it the target and runs the normal checks against it, so a printer picked off the map becomes `192.168.1.23:631` instead of an HTTPS check against a port it never had. Ports whose protocol netdoc probes add their rows (`http`, `https`, `ssh`); the rest stop at the TCP rung, which is all a connect can honestly report. `esc` returns to the device list, and `r` still accepts a port typed by hand for a service the list does not carry.

When nothing answers, the panel says which of the two things happened: refused connections show the device is on the network with nothing listening on those ports, while silence leaves powered off, gone, and dropping traffic all open. A target on this machine's own network is also diagnosed as one: a failed connect to it is never explained by the state of internet egress, which it does not use.

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

`status` is one of `PASS`, `WARN`, `FAIL`, `SKIP`, `N/A`. `target` is `null` in generic (no-target) mode. `ms` is the check's wall time truncated to milliseconds but floored at `1`, so `0` means the check never ran. Optional per-check fields (`cause`, `address_families`, `fix`, `addrs`, `selected_ip`, `source`, `iface`, `network`, `portal`, `attempts`, `routes`) are omitted when empty. `internet_tcp.address_families` records the independently tested IPv4 and IPv6 state as `reachable` or `unreachable`. `target_tcp.address_families` records the same states for a dual-stack target after comparison with the host's independent family paths. Under `--iface` or an explicit source address, a family the selected source has no address for is never dialed and its key is omitted, meaning untested rather than unreachable. A target family is also omitted when the target publishes it but the host did not prove a usable local path for it. A selection leaving no usable family at all reports the whole egress row as `N/A`, as the QUIC and encrypted-DNS rows already do. A configured egress family whose path fails while the other succeeds warns with `ipv4_unreachable` or `ipv6_unreachable`. Failed QUIC, encrypted-DNS, proxy, and TLS checks populate `cause` so automation can distinguish failure stages without parsing `detail`; QUIC uses `timeout` or `quic_handshake_failure`, encrypted DNS uses `timeout` or `encrypted_dns_unavailable`, while TLS values include `certificate_expired`, `certificate_not_yet_valid`, `hostname_mismatch`, `untrusted_issuer`, `tls_handshake_failure`, `tcp_unreachable`, `timeout`, and `connection_closed`. Failed direct egress may use `no_default_route`, `gateway_unreachable`, `selected_path_failed`, or `preferred_route_failed`, read from the local routing and neighbor tables on Linux, macOS, and Windows. macOS route entries carry no preference metric, so `preferred_route_failed` comes only from Linux and Windows, and `gateway_unreachable` needs a neighbor cache entry that exists and shows the next hop unresolved, so an unseen next hop leaves the weaker `selected_path_failed` rather than a guess. The `portal` object marks detected HTTP interception and includes `redirect_url` only when the response supplied a valid HTTP(S) sign-in URL; the app displays that URL but never opens it. Field names and the status vocabulary are stable, so they are safe to script against. Exit codes follow the table in [README.md](../README.md#exit-codes) (`ok: false` ⇒ exit `1`).

`routes` is the operating system's own route decision for each destination the check was about, one entry per address, since route selection is per address and not per hostname. netdoc asks the kernel the same question `ip route get` asks and records the answer; it does not read a routing table and re-run the selection itself, and it never dumps one. The lookups are unprivileged and bounded: the general Internet endpoints on the `iface` row, the system resolver on the `dns` row, and each resolved target address on the `target_tcp` row.

Each entry carries `destination`, `family`, and whatever the platform supplied: `interface`, `gateway` (absent when the destination is on-link), `source`, `prefix` (the route entry that matched), `metric`, `table`, `interface_mtu`, `tunnel`, `tunnel_kind`, `unreachable`, `reason`, and `competing`. An absent field means the platform did not supply it and is never to be read as zero, which is why `metric` is omitted rather than written as `0` where none was reported. `routes` itself is absent on a platform netdoc cannot ask, which is not the same as having no route: `"unreachable": true` is how "no route" is said.

`tunnel` is `tunnel` when the operating system itself names the device an encapsulating kind (`tunnel_kind` then carries which: `wireguard`, `tun`, `gre`, `ppp`, and so on), `likely` when the link only has the shape of one, and `direct` for an ordinary interface. It is absent when nothing classified the interface, which reads as unknown and not as direct. There is no list of VPN product names anywhere in netdoc; a product that installs an ordinary Ethernet device is deliberately not detected rather than guessed at.

`prefix` is the route entry the operating system said it matched, and it is present only where the platform reports one. macOS and Windows do; Linux does not. A Linux route lookup echoes the length the query asked about rather than the length of the entry it matched, so every answer to a host lookup comes back at the full address length whatever the table holds. Reading that back as a matched prefix would report every destination as a host route, so the field stays absent there rather than carrying a number the kernel never meant. Everything that follows from a named entry follows only on the platforms that name one: `competing` and a `lower_metric` reason need both the entry and a preference metric, so they come from Windows alone today, and even there the list is the other default routes that were available rather than an enumeration of every route that could have covered the destination. netdoc reads no policy rules on any platform.

`reason` is the only derived field, and it is what answers where `prefix` cannot. It is drawn from the route entry where the platform names one, and otherwise from comparing this destination's decision with the decision the same kernel gave for general Internet traffic, which is two answers from the operating system rather than a guess about its table. Those two kinds of evidence do not support the same claims, and the vocabulary keeps them apart. `default_route` (the destination matched the default route), `more_specific_than_default` (a narrower entry than a default route matched it, which is the split-tunnel case at the level of the routing table), `host_route`, and `lower_metric` are statements about which entry won, and are reported only where the platform named it. `same_path_as_default` (this destination leaves by the same interface and next hop as general Internet traffic) and `differs_from_default_path` (it leaves by a different one) are the weaker answers available where the entry is unnamed, and they say nothing about why. That distinction is not pedantry: a policy rule, a separate routing table, a VRF, source-specific routing, or a multipath route choosing another next hop each send one destination down a different path with no narrower prefix anywhere in it, and a rule pointing at a table whose winning entry is itself a default route is indistinguishable from here. `on_link` (no next hop between here and the destination) and `no_route` are read from the decision itself on every platform. `interface_mtu` is the selected link's own MTU and is never the end-to-end path MTU, which only the `path_mtu` row measures.

Resolver failures can add `dns_timeout` or `dns_temporary_failure`; a target TCP
check whose every attempted address explicitly refuses the connection adds
`connection_refused`; a peer that accepted TCP before resetting adds
`connection_reset`. Each target attempt can add the same stable connection
`cause`. `aborted: true` means cancellation or the enclosing probe deadline
ended that attempt, so it is not evidence that the individual address failed.
These are optional additions to the existing check objects.

`fix` is the one-line remedy netdoc prints as that row's `Fix:` line, on a Warn row as well as a failed one, and is omitted where there is nothing useful to suggest. Some hints name the thing to change by whatever the host OS calls it. A name-resolution failure directs Linux users to `/etc/resolv.conf` and DNS settings, while macOS points at System Settings and Windows at `ipconfig /all`. Others, such as the certificate and routing hints, carry no OS-specific wording at all.

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

### Diagnosis findings

`verdict` says what kind of problem this is. `findings` says which problem it is.

Every run is interpreted once, and that one interpretation produces the summary, the verdict, the row the app puts your cursor on, and this array. They cannot disagree with each other, because nothing reconstructs the diagnosis a second time.

```json
"findings": [
  {
    "id": "tls_certificate_expired",
    "focus": "tls",
    "evidence": ["tls", "target_tcp"],
    "causal_evidence": [
      {"kind": "support", "check": "tls", "observation": "cause"},
      {"kind": "support", "check": "target_tcp", "observation": "status_pass"},
      {"kind": "ruled_out", "check": "target_tcp", "observation": "status_pass", "candidate": "tls_tcp_unreachable"}
    ],
    "remediation": {
      "id": "renew_certificate",
      "action": "Renew the certificate, or check this machine's clock",
      "why": "The handshake was rejected because the certificate is outside its validity window. Either it really has expired, or this machine's clock runs far enough ahead to make a valid one look expired.",
      "steps": [
        "Compare the validity dates on the TLS row with today's date.",
        "Confirm this machine's clock before blaming the certificate."
      ],
      "command": ["timedatectl", "status"],
      "expect": "A certificate whose validity window covers now, on a machine whose clock is right."
    }
  }
]
```

`id` is a stable identity to branch on, and never changes when the English sentence beside it is reworded. `focus` is the check id whose `detail` and `fix` belong to this finding, so the row's own hint stays on the row that wrote it rather than being restated here. `evidence` remains the original compatibility list of observed check IDs, `focus` first. `causal_evidence` is its typed form and may also name a relevant check that was not evaluated. A `value` identifies a member of a structured observation, such as `ipv6` or one attempted address. `counterfactual` names the variable that changed and the observed alternatives, each of which references causal evidence already carried by the finding. `remediation` is what to do about the finding, described below.

Each causal item has a `kind`, a check ID in `check`, and, when an observation
exists, its typed identity in `observation`. The value itself remains on the
referenced check. This keeps a measured fact separate from what that fact means
for a diagnosis.

| `kind` | Meaning | Additional field |
| --- | --- | --- |
| `support` | The observation directly supports the selected diagnosis | none |
| `contradiction` | The observation weakens a named alternative but does not exclude it | `candidate` diagnosis ID |
| `ruled_out` | Independent positive evidence excludes a named alternative | `candidate` diagnosis ID |
| `not_evaluated` | The check supplied no observation and remains unknown for this run | `reason` |

The observation vocabulary is `status_pass`, `status_warn`, `status_fail`,
`status_skip`, `status_not_applicable`, `cause`, `dns_answers`,
`dns_not_found`, `captive_portal`, `timeout`, `clock_offset`,
`status_downgraded`, `family_reachable`, `family_failed`,
`address_succeeded`, `address_failed`, `route_tunneled`, `route_direct`,
`route_unreachable`, `route_path_differs`, `route_family_split`, and
`route_interface_mtu`. Not-evaluated reasons are `prerequisite_failed`,
`not_selected`, `not_applicable`, and `incomplete`. A skipped, not-applicable,
missing, or incomplete check is never enough to rule out a cause. An absent
`causal_evidence` field means this producer did not record an explanation; it
does not mean the listed alternatives were tested.

The six `route_*` observations describe the path a row's own traffic took, and
each is a fact about that one row: which interface it left by, whether that
interface encapsulates, and whether the operating system had a route at all.
None of them is a verdict. A tunnel is not a fault, and no diagnosis is reached
because one exists; route observations only ever attach to a conclusion the
checks had already proved on their own. Where the interesting fact is that two
rows took different paths, it is recorded as two observations, one on each row,
because a single item must be checkable against the row that carries it:
`route_path_differs` on the `dns` row names the target's interface in `value`,
and a split tunnel appears as `route_tunneled` on one row beside `route_direct`
on the other. Both of those read exactly as strongly as the row's `tunnel`
state: where the operating system named the device kind, the app says the
traffic left through that tunnel, and where only the link's shape suggested it,
the app says the link has the shape of one, since a mobile broadband modem has
the same shape. `route_direct` is the absence of any such classification and
never a proof that nothing encapsulates the path, which is why a VPN presenting
an ordinary Ethernet device is not detected. A resolver on loopback records no
path at all: a local stub is reached over `lo` by definition, so it would
always look like a different path while its own upstream path, the one that
matters, stays invisible. `route_interface_mtu` reports that the selected link
is narrower than the one general traffic uses; it is the link's own MTU, never
a measured path MTU.

The same interpretation branch creates the diagnosis and its causal items.
There is no pass that receives a final diagnosis ID and reconstructs plausible
reasons afterwards. The ordered array is deterministic, rejects duplicates in
snapshots, and keeps every relationship tied to the check observation that
actually occurred.

The array is omitted when the run reached no specific conclusion: everything passed, the run is still going, nothing was selected, or the only impairment is on a row no diagnosis is about. Those runs answer with `summary` and `verdict` alone rather than being given an identity the probes did not support. The primary finding remains first and supplies the headline. Additional counterfactual findings can preserve a separate partial impairment proved by the same run.

| `id` | What netdoc concluded |
|---|---|
| `no_usable_interface` | No interface is up, so the link is down |
| `captive_portal` | Traffic is intercepted by a sign-in portal |
| `offline` | Neither direct egress nor DNS is working |
| `direct_egress_blocked` | Direct TCP egress is dead while something else still works |
| `direct_egress_degraded` | Direct egress carries traffic but is impaired |
| `proxy_only_network` | Only the environment proxy has egress |
| `local_egress_failure` | This machine's own path is broken, so nothing beyond it was fairly tested |
| `probable_path_mtu_problem` | A protocol exchange and a bulk write both stall, which is what a path MTU black hole looks like |
| `system_dns_failure` | The configured resolver fails on a name public DNS resolves |
| `dns_name_not_found` | Neither resolver has A/AAAA records for the name |
| `dns_failure` | Name resolution is failing, with no second opinion separating the two above |
| `dns_disagreement` | System and public DNS answer with different networks |
| `encrypted_dns_unavailable` | DoH/DoT cannot complete a verified exchange while plain DNS works |
| `quic_unavailable` | UDP/443 fails while TCP/443 works, so applications fall back |
| `proxy_failure` | The configured environment proxy check failed |
| `tcp_connection_refused` | Every connection attempt was explicitly refused |
| `target_unreachable` | The target does not answer though DNS and the internet work |
| `local_device_unreachable` | A device on the local network is silent while this machine's network works |
| `reachability_untested` | The target is silent and direct egress was not checked, so neither end can be blamed |
| `ipv4_target_unreachable` | The target works over IPv6 while its IPv4 alternatives fail despite an independently proved IPv4 path |
| `ipv6_target_unreachable` | The target works over IPv4 while its IPv6 alternatives fail despite an independently proved IPv6 path |
| `partial_endpoint_reachability` | An attempted resolved address fails while another address for the same target and port succeeds |
| `tls_certificate_expired` | The certificate is outside its validity window |
| `tls_certificate_not_yet_valid` | The certificate's start date has not arrived |
| `tls_hostname_mismatch` | The certificate is for a different name |
| `tls_untrusted_issuer` | The certificate is signed by a CA this machine does not trust |
| `tls_clock_skew` | A measured clock offset explains the certificate rejection |
| `tls_timeout` | The handshake spent its whole budget without answering |
| `tls_connection_closed` | The peer closed or reset during the handshake |
| `tls_tcp_unreachable` | The TLS dial itself could not reach the port |
| `tls_handshake_failure` | The handshake failed with no cause the client could classify |
| `https_no_response` | TLS completes but no HTTPS response arrives |
| `http_no_response` | No HTTP response arrives |
| `service_banner_failure` | The port accepts TCP but the banner check failed |
| `service_banner_missing` | The port accepts TCP and sent no banner |
| `selected_dns_check_failed` | A `--check`/`--skip` selection left a failed DNS row no other case explains |
| `selected_service_check_failed` | The same, for a service row |
| `selected_network_check_failed` | The same, for a network row |

The TLS identities are drawn from the same classification the `cause` field publishes, so a finding stays as precise as the handshake was. The sentence in `summary` is deliberately more hedged than the identity for the failures a client genuinely cannot tell apart, and `tls_handshake_failure` is what an unclassifiable handshake gets rather than a specific accusation.

Nothing here claims a wrong default route, a missing subnet route, or an operator's intent, because no probe proves those. `id` values are added over time; treat an unrecognized one as "some specific problem" and fall back to `verdict`.

### Remediation

A finding says what is wrong. Its `remediation` says what to do about it, as data rather than a sentence to be parsed out of `fix`.

```json
"remediation": {
  "id": "restore_default_route",
  "action": "Restore a default route",
  "why": "Nothing in the routing table leads off this network, so traffic for the internet has nowhere to go. That is usually DHCP not completing, a VPN that dropped its route, or a static configuration with no gateway.",
  "steps": [
    "Renew the DHCP lease, or reconnect the VPN that installs the route.",
    "On a static configuration, check that the gateway is set and sits on this machine's own subnet."
  ],
  "command": ["ip", "route"],
  "expect": "A default route (0.0.0.0/0 or ::/0) pointing at the gateway."
}
```

`id` is a stable identity to branch on, from the table below. `action` is the next step in one line, `why` is what makes it the right one given the evidence this run collected, `steps` breaks it down in the order worth trying, and `expect` is what success looks like, so a script or a person knows whether a rerun is worth it yet. `why`, `steps`, `command` and `expect` are omitted when empty, as every other optional field is; `id` and `action` are always present.

`command` is an **argv array, never a shell string, and netdoc never runs it**. It is offered for you to read and run yourself. Every command in the table is a read-only inspection of local state (routes, links, neighbors, resolvers, the clock, interface MTUs), chosen per OS: Linux gets `ip route`, macOS `netstat -rn`, Windows `route print -4`. None of them changes configuration, none escalates privilege, and none carries the target inside it, because investigating the target itself is what the [drill-down tools](#drill-down-tools) are for. A remediation with nothing local worth inspecting has no `command` at all.

The advice is chosen from the finding's `id` and, where the focused check classified its failure further, that check's stable `cause`. So one conclusion can lead to several remediations: `offline` with `no_default_route` says to restore the route, with `gateway_unreachable` says to get the gateway answering, and with `selected_path_failed` says the break is past a router that looks fine from here. A cause netdoc does not have specific advice for keeps the conclusion's general answer rather than losing the remediation.

Where netdoc genuinely cannot tell two causes apart, the remediation says what to investigate instead of presenting a guess as fact: `narrow_tls_failure` lists what is still possible rather than naming one, and `rerun_with_egress_check` says the run itself was too narrow to blame either end.

In the TUI the same advice appears in the Details panel of the row the diagnosis focuses, and `R` reruns the identical checks against the same target once you have acted on it.

| `id` | Next action |
|---|---|
| `bring_up_link` | Bring a network interface up |
| `sign_in_to_portal` | Sign in to the network |
| `check_local_path` | Check this machine's own path off the network |
| `restore_default_route` | Restore a default route |
| `reach_gateway` | Get the default gateway answering |
| `check_router_uplink` | Check the router's own uplink |
| `fix_preferred_route` | Check the interface holding the preferred default route |
| `fix_local_egress_first` | Fix this machine's connection before judging the target |
| `use_proxy_path` | Send the traffic the way this network allows |
| `check_proxy_config` | Check the configured proxy |
| `check_proxy_reachable` | Check that the proxy itself is up |
| `check_proxy_resolution` | Check name resolution on the proxy |
| `check_proxy_egress` | Check what the proxy is allowed to reach |
| `restore_ipv4_egress` | Restore IPv4 egress, or accept an IPv6-only path |
| `restore_ipv6_egress` | Restore IPv6 egress, or accept an IPv4-only path |
| `read_egress_warning` | Read what the egress row is warning about |
| `fix_system_resolver` | Fix or replace the configured resolver |
| `check_the_name` | Check the name itself |
| `check_name_resolution` | Check name resolution on this machine |
| `confirm_split_dns` | Confirm which of the two DNS answers is right |
| `choose_encrypted_dns` | Decide whether encrypted DNS has to work here |
| `expect_tcp_fallback` | Expect the TCP fallback, and open UDP/443 if speed matters |
| `start_the_service` | Start the service, or check the port |
| `trace_the_path` | Work out where the packets stop |
| `check_the_device` | Check the local device itself |
| `rerun_with_egress_check` | Rerun with the general connectivity check included |
| `lower_path_mtu` | Try a lower MTU on the path |
| `renew_certificate` | Renew the certificate, or check this machine's clock |
| `await_certificate_validity` | Check the clock, then the certificate's start date |
| `set_the_clock` | Set this machine's clock |
| `match_certificate_name` | Connect with the name the certificate is for |
| `resolve_untrusted_issuer` | Find out who signed the certificate |
| `check_tls_path` | Check the path before blaming the service |
| `check_tls_termination` | Check what is terminating the handshake |
| `retry_tls_dial` | Retest, since the TLS check could not open its own connection |
| `narrow_tls_failure` | Narrow the handshake failure down by hand |
| `check_application_layer` | Check the service on top of a working connection |
| `check_banner_service` | Check the service behind the open port |
| `identify_listener` | Confirm which service is on that port |
| `rerun_full_chain` | Rerun without the check selection |

`remediation` is additive: it appears alongside the fields findings have always carried, and `fix` on each check is unchanged. A row's `fix` is still the one-line hint that row wrote about itself, with the certificate dates and measurements only that probe held; the remediation is the finished diagnosis's answer for the run. New `id` values are added over time, so treat an unrecognized one as "some specific advice" and show `action` and `steps` rather than branching on it.

## Diagnostic snapshots

`--save file` runs the checks headless and writes a **diagnostic snapshot** of the finished run, conventionally with a `.ndoc` extension:

```sh
netdoc --save incident.ndoc github.com      # just the artifact
netdoc --json --save incident.ndoc github.com  # the report on stdout too
```

A snapshot is one finished run, recorded so it can be reopened later without probing anything again: the target as you typed it and as netdoc parsed it, the run settings, every check with its status, timings and evidence, and the diagnosis that was drawn from them. It is meant for the failure you cannot reproduce on demand, for attaching to a bug report, and for [comparing two runs](#comparing-two-snapshots).

`--save` implies headless operation, so it needs no terminal, and it is independent of `--json`: give both to get the usual report on stdout as well. It cannot be combined with `--watch` (a snapshot is a finished run, and a watch never finishes) or with `--toolbox`. The snapshot never changes the diagnosis or the exit code: a run that fails still exits `1` and still saves. A destination whose directory does not exist is rejected with exit `2` before any probe runs, and a write that fails once attempted also exits `2`, since the artifact the run was for does not exist. The file is written through a temporary file in the same directory and renamed into place, so a failed write leaves any previous snapshot intact rather than a truncated one.

### Snapshot format and versioning

The file is JSON, indented and newline-terminated, so it reads in a pager and diffs line by line. Encoding is deterministic: the same run always produces the same bytes.

```json
{
  "schema": "netdoc.snapshot.v1",
  "created_at": "2026-01-02T03:04:05Z",
  "tool": {"version": "1.2.3", "os": "linux", "arch": "amd64"},
  "target": {"raw": "example.com", "host": "example.com", "port": 443, "protocol": "tls+http", "port_explicit": false},
  "options": {"probe_timeout_ms": 4000, "public_dns": "8.8.8.8"},
  "checks": [
    {
      "id": "dns", "name": "DNS example.com", "deps": ["iface"],
      "status": "PASS", "ran": true, "duration_ms": 12,
      "detail": "example.com resolved to 2 addresses",
      "observed": {"addresses": ["93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"]}
    }
  ],
  "diagnosis": {"verdict": "network", "summary": "…", "blamed": "internet_tcp", "failed_stage": "target_tcp", "findings": [{"id": "local_egress_failure", "verdict": "network", "summary": "…", "focus": "internet_tcp", "evidence": ["internet_tcp", "target_tcp", "dns"], "causal_evidence": [{"kind": "support", "check": "internet_tcp", "observation": "status_downgraded"}]}]},
  "ok": false
}
```

`schema` is the first field and the artifact's identity. A reader must check it before anything else and refuse a schema it does not know, rather than guessing at the fields underneath. The version moves only for a change an older reader would misread, such as a renamed field, a changed unit, or a changed meaning; adding an optional field is not that, so decoders ignore keys they do not recognize and treat every absent optional field as "not known" rather than as zero.

`options` records only the settings that decide what the probes did: the per-check timeout, the second-opinion resolver (empty when `--public-dns ""` switched that row off), the `--check`/`--skip` selection in the order it was given, and the `--iface` binding netdoc resolved. `check`, `skip`, and `source` are absent when the run used none of them. Flags that only affect presentation are not recorded, because a comparison never reads them.

Every check the run built appears in `checks`, including the ones that never produced an answer, and `status` says on its own which is which. It is always present and never empty. Alongside the `PASS`, `WARN`, `FAIL`, `SKIP`, and `N/A` used everywhere else, a snapshot has one more value that no probe can return:

- `SKIP` is a check deliberately left out, because a prerequisite failed.
- `INCOMPLETE` is a check that never reported at all, which is what a cancelled or interrupted run leaves behind. Nothing decided to leave it out; the run ended first.

An `INCOMPLETE` row is never a pass, and a run holding one is never `"ok": true`. That is enforced when the file is written rather than left to readers to remember, so a snapshot in which absent evidence could be read as a passing check does not exist.

`ran` answers a narrower question, and only that one: did the probe body execute. It is what `duration_ms` alone cannot say, since a check that finished in under a millisecond also reports `0`. A skipped row and an incomplete row both report `ran: false`, so `ran` is not how a reader tells them apart; `status` is.

`observed.routes` holds the same per-destination route decisions the JSON report documents above, in the same shape and with the same rule that an absent field is unknown rather than zero. It is an additive optional field: a snapshot written before it existed simply has none, and every reader treats that as "this run recorded no route decisions", never as "there was no route". Nothing stores a routing table; the entries are the answers to the bounded set of lookups the run already had to make.

Within a check, `observed` is what the probe measured and `derived` is what the cross-probe reasoning pass did to that row afterwards. Only `status_downgraded` lives under `derived` today: it marks an observed egress failure that was relaxed to a Warn because another path proved the network still carries traffic. Its presence is the signal that the row's status is a conclusion rather than a reading. `cause`, `verdict`, and the finding IDs use exactly the vocabularies documented for [JSON output](#json-output) and [diagnosis findings](#diagnosis-findings).

Findings also persist their optional `causal_evidence` and `counterfactual`
fields. They record the interpretation made during the incident rather than
asking a future Network Doctor version to reinterpret old observations.
Attempt `cause` and `aborted` fields retain whether an address supplied usable
failure evidence. These are additive v1 fields, so existing `.ndoc` files remain
loadable and the schema stays `netdoc.snapshot.v1`. Loading an older file
without the fields leaves them absent; the decoder never invents historical
relationships. New files validate that every observed relationship points to a
matching check fact, that counterfactual alternatives reference evidence the
finding carries, that not-evaluated reasons match the check state, and that no
item is duplicated.

Remediation text is deliberately not stored. Fix advice lives on the check rows as `fix`, and the structured next action is regenerable from a finding's `id` together with `tool.os`, so a snapshot does not carry a second copy to fall out of step.

### What a snapshot contains, and what it does not

A snapshot holds only what the run already gathered. Nothing is collected for the file's benefit: no packet capture, no scan of unrelated interfaces or routes, no environment variables, no configuration files, and nothing is uploaded anywhere.

Treat the file as network information about the machine that produced it. It deliberately retains, because a diagnosis is not readable without them:

- resolved IP addresses, the address netdoc selected, and per-attempt errors
- the local source address and interface name probes used
- the connected Wi-Fi network name (SSID), on the `ssid` row
- the target hostname as typed, which may be a private or internal name
- the proxy host and port from `HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY`, where one is configured
- the second-opinion resolver, and the local clock's offset from the captive-portal endpoint's
- per-destination route decisions under `observed.routes`: the interface chosen, the next hop, the local source address, the matched prefix, the routing table's name, and the interfaces of any competing default routes. These describe the local network's shape, and a tunnel's device kind says a VPN is in use

It contains no credentials. Userinfo in a target is rejected by the parser before a run starts, and the proxy rows record only the proxy's host and port, never the username or password from a proxy URL. Probe text is sanitized on the way out of the runner, so a hostile hostname or server banner lands in the file as inert bytes.

Redaction is not implemented yet. Until it is, look at a snapshot before sharing it: the fields above are grouped under `observed` on each check precisely so a later redaction pass can act on them predictably.

## Comparing two snapshots

`--compare` reads two saved snapshots and reports what changed between them. The two files are arguments, the earlier or known-good run first:

```sh
netdoc --compare good.ndoc bad.ndoc         # the table
netdoc --compare --json good.ndoc bad.ndoc  # the same comparison, machine-readable
```

It runs no probes and opens no socket. Everything reported comes out of the two artifacts, so a comparison works long after the fact and on a machine that has never seen either network. It is headless, needs no terminal, and cannot be combined with any flag that describes a run netdoc would perform (`--toolbox`, `--watch`, `--save`, `--check`, `--skip`, `--iface`, `--public-dns`, `--no-history`, `--keys`, `--timeout`, and either peer flag). Those settings are already recorded in the files.

Exit `0` means the two snapshots describe the same diagnostic state, `1` means they differ, and `2` means an argument or an artifact was unusable. A snapshot that records a failed run is not itself an error here: the question `--compare` answers is whether anything moved, not whether the network is healthy.

### What counts as a difference

The comparison is semantic, not a JSON diff. Every field it reads is named in the implementation on purpose, which is what lets it ignore the parts of a snapshot that change on their own, and what decides which collections are ordered:

| Compared | How |
| --- | --- |
| Target | `host`, `ip`, `port`, `protocol`, and `port_explicit`, then `raw` on its own, so "the same host, entered differently" and "a different host" are separate answers |
| Tool | `version`, `os`, and `arch`, because a diagnosis and its advice are chosen per platform |
| Run settings | the probe timeout, the second-opinion resolver, the `--iface` binding, and the `--check`/`--skip` selection as **sets** |
| Diagnosis | `ok`, `verdict`, `blamed`, `failed_stage`, the findings as a **set** keyed by `id`, the first finding as the primary conclusion, and each finding's ordered `evidence` and `causal_evidence` |
| Checks | membership, `status`, `cause`, `ran`, and `deps` in order |
| Reasoning | `derived.status_downgraded`, so an inferred outcome and a measured one are not read as the same state |
| Evidence | everything under `observed`: resolved addresses and connection attempts as **sets**, and the selected address, source address, interface, SSID, resolver, per-family reachability, captive-portal state, and timeout flags as values |
| Routes | `observed.routes` keyed by destination address, so a destination present on one side only is reported as such rather than as a changed path; then that entry's matched prefix, interface, next hop, source, metric, table, tunnel state and kind, selection reason, interface MTU, and competing routes |
| Paths | the derived reading of those routes: the target's interface, matched route, selection reason and tunnel state, the general Internet interface, the resolver's interface, and whether DNS and application traffic took the same path |

The paths section is derived from the routes both snapshots already carry rather than stored a second time, so the two can never describe different runs, and two files written by different netdoc versions are read the same way. It answers the question a person asks first, in normalized words rather than an operating system's route syntax:

```text
target interface changed from wlan0 to wg0
target matched route changed from 0.0.0.0/0 to 10.20.0.0/16
target route selection changed from same_path_as_default to differs_from_default_path
target tunnel state changed from direct to tunnel
DNS and application traffic changed from same path to different paths
```

The matched route is reported only where both snapshots recorded one, which on Linux is neither of them. The line above it carries the same news there, in the weaker words that platform can support: a destination that stops taking the way out general traffic takes is a changed selection whether or not the entry behind it can be named.

A split between DNS and application traffic is reported as a difference and nothing more. Split DNS is a design as often as it is a fault, and the comparison says what moved, never whose fault it is.

Absence is never collapsed into a zero. `SKIP`, `N/A`, and `INCOMPLETE` stay three different things that happened to a row. A resolver that answered with no records is not the same as one that did not answer. A captive portal that advertised no sign-in URL is not the same as no portal, so interception is its own field rather than something inferred from an empty URL. A second opinion switched off with `--public-dns ""` is reported as a removal, not as a change to nothing.

### What is intentionally ignored

These change between two runs of a machine that did not change, so treating them as differences would bury the ones that matter:

- `created_at`, the capture time. It is shown in the header of the report as context, and is never a difference.
- `duration_ms` on a check and on a connection attempt. Both are the measurement itself.
- `detail` and `fix` on a check, `summary` on the diagnosis and on a finding. All four are derived sentences that quote measurements ("in 41ms"), and the format documents them as never parsed back. `status` and `cause` are the machine-readable form of what they say, and those are compared.
- `name` on a check, which is display text built from the probe and the target.
- The order of resolved addresses, connection attempts, findings, and the `--check`/`--skip` selection. Order there is the resolver's, the dialer's, or the order you typed, and it is not the shape of anything. Order **is** kept where it carries meaning: a check's `deps`, a finding's `evidence`, and its `causal_evidence` are compared as ordered lists.
- Sub-second movement in `clock_offset_ms`. The offset is compared at whole-second resolution, which keeps the sign and the magnitude the diagnosis reasons about and drops the jitter of a clock that is fine.

An optional field added to a future snapshot version is not compared until it is named here, which is deliberate: a new field cannot start producing differences on its own.

### Ordering and determinism

The same two files always produce the same bytes and the same text. Nothing is sorted after the fact and no map decides what gets printed: differences come out in section order (target, tool, run settings, diagnosis, checks), and within the check section in the order the later run executed its probes, followed by any check only the earlier run had, in that run's order. Set membership is the one thing sorted, so that a resolver's answer order cannot reach the output.

### Version compatibility

Both files go through the same decoder every other reader uses, so the schema rule is stated once. A file whose `schema` is not the one this build reads, including a future version, is refused by name with exit `2` rather than half understood, and so is a file that is not JSON or that holds a check row with no `status`. There is one schema version today, so there is no cross-version comparison to perform; when there is one, it will be the decoder's migration, not a second copy of the rules inside `--compare`.

### Different targets

Comparing snapshots of two different endpoints is allowed. A snapshot keeps the typed spelling next to the parsed host precisely so a comparison can tell "the same host, entered differently" from "a different host", and refusing the second case would throw away an answer it is equipped to give. It is not allowed to be quiet about it: when the two runs did not observe the same endpoint, the report says so above the table, because every row underneath then describes two different things. The machine-readable form carries the same fact as `same_target`. A generic run against a targeted one is reported as one change rather than as a field-by-field list of a target only one side ever had.

### Interpretation

The report states direct facts about the two readings and stops there. "The DNS resolver changed from X to Y", "the DNS check changed from PASS to FAIL", "traffic used wg0 instead of wlan0" are all statements about what the files say. `--compare` does not claim that one of them caused another. A status move between `PASS`, `WARN`, and `FAIL` is marked `better` or `worse`, which is a comparison of two outcomes and not a cause; `SKIP`, `N/A`, and `INCOMPLETE` have no rank, so a move to or from one of them carries no direction rather than a guessed one.

### Machine-readable comparison

`--json` prints the comparison as its own versioned document, separate from the snapshot schema it reads because scripts consume it:

```json
{
  "schema": "netdoc.comparison.v1",
  "before": {"created_at": "2026-03-04T05:06:07Z", "tool": {"version": "1.2.3", "os": "linux", "arch": "amd64"},
             "target": "github.com:443 tls+http", "verdict": "ok", "summary": "…", "ok": true},
  "after":  {"created_at": "2026-03-04T18:22:41Z", "tool": {"version": "1.2.3", "os": "linux", "arch": "amd64"},
             "target": "github.com:443 tls+http", "verdict": "dns", "summary": "…", "ok": false},
  "same_target": true,
  "checks": [
    {"id": "iface", "before": "PASS", "after": "PASS", "kind": "unchanged", "differs": true},
    {"id": "dns", "before": "PASS", "after": "FAIL", "kind": "changed", "direction": "worse", "differs": true}
  ],
  "changes": [
    {"section": "diagnosis", "path": "diagnosis.verdict", "label": "verdict", "kind": "changed",
     "before": "ok", "after": "dns", "summary": "verdict changed from ok to dns"},
    {"section": "check", "check": "iface", "path": "checks.iface.observed.interface",
     "label": "iface interface", "kind": "changed", "before": "wlan0", "after": "wg0",
     "summary": "iface interface changed from wlan0 to wg0"}
  ]
}
```

`path` is a difference's stable identity, spelled as the field path inside the snapshot, so two runs name the same difference the same way and a script can key on it. `kind` is `added`, `removed`, or `changed`, and it is what says whether an empty `before` or `after` is an absent value: an `added` change always has an empty `before` and a non-empty `after`, and `changed` has neither empty. Both keys are always present, so an empty string is a value rather than a key a reader has to interpret.

`checks` is every check in either snapshot, unchanged rows included, so the same document answers "what stayed the same". Its `kind` is about the status alone, while `differs` is true whenever anything about that check moved, including evidence underneath a status that held. `direction` is `better` or `worse` where the outcomes have an ordering, and absent otherwise.

An empty `changes` array is the machine-readable form of "no meaningful differences", and it is what exit `0` means.

### What a comparison cannot tell you yet

A snapshot holds only what the run gathered, so a comparison can only report what is in there. Today that leaves some questions unanswerable from two `.ndoc` files:

- **The system resolver.** The snapshot records the second-opinion resolver netdoc queried directly (`observed.resolver`), not the resolver the system handed the probes, so a change of DNS server underneath an unchanged `--public-dns` shows up only as the DNS check's outcome and addresses moving.
- **Routes.** No route or default-route state is captured, so a route change is visible only through its effects: the source address, the selected interface, and the reachability rows.
- **The proxy.** The proxy host and port appear in the proxy row's `detail`, which is derived text and not compared. A proxy change registers as that row's status or `cause` moving, not as the proxy itself.
- **Path MTU.** The path-MTU row records its outcome, not a measured MTU number, so there is no MTU value to compare.
- **VPN and tunnel state as such.** There is no tunnel field; a VPN shows up as the interface name and source address on the rows that observed them, which is usually enough to see it, and never enough to name it.

Adding fields to the snapshot for the sake of a fuller comparison is deliberately not done here. Each one is a change to a published format, and a comparison is worth more trustworthy than complete.

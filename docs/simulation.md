# Network simulation (`netdoc-sim`)

`netdoc-sim` builds a throwaway virtual network from a YAML scenario, breaks it
on purpose, runs netdoc inside it, and reports whether netdoc's diagnosis
matched the fault that was actually injected.

It is a test harness for netdoc, not a part of it. Nothing in this document
ships in the `netdoc` binary: the release builds only the root package, and
`internal/simulation` is imported by `cmd/netdoc-sim` alone.

```sh
go build -o netdoc . && go build -o netdoc-sim ./cmd/netdoc-sim

./netdoc-sim capabilities            # what this host can do, and what a run does
./netdoc-sim scenarios               # the built-in scenario library
./netdoc-sim validate broken-dns     # parse and check, build nothing
./netdoc-sim run broken-dns          # the whole thing
./netdoc-sim run socks5-local-dns-fails
./netdoc-sim run socks5h-remote-dns-succeeds
./netdoc-sim run tls-valid
./netdoc-sim run tls-expired-certificate
./netdoc-sim run tls-hostname-mismatch
./netdoc-sim run healthy-routed-network
./netdoc-sim run dual-stack-healthy
./netdoc-sim run ipv4-works-ipv6-broken
./netdoc-sim run ipv6-works-ipv4-broken
./netdoc-sim run gateway-unreachable
./netdoc-sim run wrong-default-route
./netdoc-sim run multiple-interfaces-wrong-preferred-route
./netdoc-sim run packet-loss
./netdoc-sim run high-jitter
./netdoc-sim run intermittent-dns
./netdoc-sim run tcp-reset
./netdoc-sim campaign unstable-connectivity --runs 6 --seed 12345
./netdoc-sim campaign unstable-connectivity --seed 12345 --iteration 3
./netdoc-sim run broken-dns -json    # the same report, machine-readable
./netdoc-sim run broken-dns -dry-run # every privileged command, run none of them
./netdoc-sim run broken-dns -keep    # leave it up so you can walk around inside
```

## Why it exists

netdoc's job is to tell a DNS problem from a routing problem from a dead
service. That claim is only testable against networks that are broken in known,
specific ways — and you cannot break the machine you are working on to find out.
A simulation gives you a network whose fault is known by construction, so
netdoc's answer can be graded rather than eyeballed.

The grading is deterministic and evidence-based. No model is involved in
deciding whether netdoc was right.

## It is unprivileged, and that is a structural property

A run needs no root, no sudo, and no setuid anything.

`netdoc-sim run` re-executes itself inside a new **user namespace** with your
uid mapped to root inside it, plus a network and mount namespace. Inside that
namespace the simulator holds `CAP_NET_ADMIN` and `CAP_NET_BIND_SERVICE`, so it
can create bridges, veth pairs, routes, nftables rules, `tc` qdiscs and
listeners on port 53. Outside it, it holds nothing at all.

That is why the safety rules are not promises this code makes and could break:

- it **cannot** alter the host's routes, resolver, interfaces or firewall — the
  kernel denies it, not a check in this package;
- it registers nothing under `/run/netns` or `/etc/netns`, so there is no state
  on the host to leak;
- a killed, crashed or `kill -9`'d run leaks nothing: every namespace is held
  open by a process, and every process carries `PDEATHSIG=SIGKILL`, so the whole
  tree dies with its parent and the kernel reclaims the namespaces.

`netdoc-sim capabilities` prints the exact list of privileged operations, and
`run -dry-run` prints every command a specific scenario would run.

## ⚠ Scenarios depend on netdoc's hardcoded probe endpoints

**Read this before changing anything in `internal/diagnostic/checks.go`.**

Several netdoc probes dial fixed public addresses and names that are compiled
into the binary. A simulation has no internet, so every scenario claims those
exact addresses for a node inside the namespace — that is the only way those
probes can run their real code path offline. **If the constants drift, the
scenarios go stale silently unless the endpoints are updated to match.**

| Probe id | What it dials | Constant in `internal/diagnostic/checks.go` |
| --- | --- | --- |
| `internet_tcp` | `1.1.1.1:443`, `8.8.8.8:443` | `internetEndpoints4` |
| `internet_tcp` | `2606:4700:4700::1111`, `2001:4860:4860::8888` | `internetEndpoints6` |
| `internet_tcp` | `http://connectivitycheck.gstatic.com/generate_204` (captive-portal check) | `probeHost`, `portalProbeURL` |
| `dns_public` | `8.8.8.8:53` | `publicDNSIP`, `publicDNSServer` |
| `dns` | resolves `connectivitycheck.gstatic.com` when no target is given | `probeHost` |

Most scenarios mirror these in the `aliases` list on their simulated internet
node and the `zone` of their DNS service. The two SOCKS scenarios intentionally
omit `probeHost` from the client resolver and expose it only in the proxy
resolver; that split is the behavior they test.

The original IPv4 scenarios deliberately do **not** claim the IPv6 endpoints,
so `internet_tcp` reports "no IPv6 egress" there. The three dual-stack scenarios
claim both endpoint lists and assert their family results independently.

### The `healthy` scenario is the canary, and it fails on purpose

`healthy` asserts `internet_tcp: PASS` and `dns_public: PASS` in a network where
nothing is broken. If someone changes `internetEndpoints4` to a different
resolver, or points the portal check at a new host, the simulated internet stops
answering on the address the probe now dials — and `healthy` turns `FAIL` with a
`false_positive` suggestion naming the probe that broke.

**That failure is the feature.** It is not a flaky test and it should not be
made tolerant. When `healthy` fails after a change to `checks.go`:

1. read which probe id the report names;
2. update the `aliases` and `zone` entries in every scenario under
   `internal/simulation/scenarios/` to match the new constant;
3. re-run `./netdoc-sim run healthy` until it is `PASS` again.

There is no automatic check for this drift, because the constants are unexported
and a test that reached into them would just be a second copy of the same list.
The `healthy` scenario is the check.

## Shape of a run

```
launcher (netdoc-sim run)              host namespaces, no privileges
  └── director                         new user + net + mount namespace
        ├── bridge nb<id><n> × N       one per logical L2 segment
        ├── node holder × N            each in its own net + mount namespace
        │     └── test services        dns / http / tcp / socks5 / tls
        └── nsenter … netdoc -json     the diagnostic under test, inside a node
```

The **director** owns the segment and does all the wiring from outside the
nodes, via `ip` and `nsenter` with argv slices — no scenario string ever reaches
a shell. A **node holder** exists only to own one simulated machine's network
and mount namespaces and to run that machine's services inside them; the
director talks to it over three lines on a pipe, so no probe can race the
topology into existence.

Each logical segment gets exactly one bridge. A node interface becomes a veth
whose director end is attached to that segment bridge and whose peer is moved
into the node namespace. Router nodes are ordinary holders with two or more
such interfaces; they do not share a bridge with destinations on another
segment.

Each node gets its own `/etc/resolv.conf` by bind-mounting a generated file
inside its private mount namespace. That is what lets two simulated machines
disagree about who their resolver is, and it is invisible to the host.

`netdoc` itself runs unmodified, as `nsenter -t <pid> -n -m -- netdoc -json …`.
The simulator never reimplements a probe or a verdict.

## Scenario format

```yaml
name: broken-dns-with-working-ip-connectivity
description: The resolver is unreachable; direct IP connectivity still works.

topology:
  subnet: 10.77.0.0/24          # optional, this is the default
  nodes:
    - name: client
      role: client              # exactly one; this is where netdoc runs
      address: 10.77.0.10
      gateway: 10.77.0.1
      resolver: 10.77.0.1

    - name: internet
      address: 10.77.0.1
      aliases: [1.1.1.1, 8.8.8.8]   # extra addresses, put on this node's loopback
      services:
        - type: dns
          zone:
            example.test: 10.77.0.20
        - {type: http, port: 80}
        - {type: tcp, port: 443}

faults:
  - type: drop
    node: internet
    direction: inbound
    to: 10.77.0.1
    protocol: udp
    port: 53

tests:
  - node: client
    target: example.test:80     # omit for netdoc's generic (no-target) checks

expect:
  verdict: dns
  checks:
    - {id: dns, status: FAIL}
    - {id: internet_tcp, status: PASS}
```

Unknown keys are rejected, so a typo fails loudly. Every address, name, port and
duration is validated before anything is created.

The original `topology.subnet`, node `address`, and node `gateway` fields remain
the backward-compatible single-segment shorthand. New routed scenarios use the
explicit form:

```yaml
topology:
  segments:
    - {name: client-lan, subnet: 10.77.1.0/24}
    - {name: upstream, subnet: 10.77.2.0/24}
  nodes:
    - name: client
      role: client
      interfaces:
        - {segment: client-lan, address: 10.77.1.10/24}
    - name: gateway
      role: router
      interfaces:
        - {segment: client-lan, address: 10.77.1.1/24}
        - {segment: upstream, address: 10.77.2.1/24}
    - name: target
      interfaces:
        - {segment: upstream, address: 10.77.2.20/24}
  routes:
    - {node: client, destination: default, via: 10.77.1.1, metric: 100}
    - {node: target, destination: 10.77.1.0/24, via: 10.77.2.1}
```

Addresses and prefixes are parsed with `net/netip` and rendered canonically
before reaching `ip` argv. A gateway must be on one of that node's directly
connected segments. Conflicting routes at the same metric, duplicate or
overlapping segments, duplicate addresses, off-segment interfaces, scoped
addresses, and routers with fewer than two useful interfaces are rejected.
Scenario authors cannot provide Linux interface names or raw route expressions.

Linux interface names are generated from the validated run id and an internal
base-36 index, stay within the 15-byte kernel limit, and never appear in the
logical evidence fields. Route metrics use the kernel's normal lowest-value
preference.

A logical segment and interface may carry both families without creating a
second veth:

```yaml
topology:
  segments:
    - {name: client-lan, ipv4: 10.78.1.0/24, ipv6: 2001:db8:77:1::/64}
    - {name: upstream, ipv4: 10.78.2.0/24, ipv6: 2001:db8:77:2::/64}
  nodes:
    - name: client
      role: client
      interfaces:
        - {segment: client-lan, ipv4: 10.78.1.10/24, ipv6: 2001:db8:77:1::10/64}
  routes:
    - {node: client, destination: default, via: 10.78.1.1}
    - {node: client, destination: "::/0", via: "2001:db8:77:1::1"}
```

`subnet` and interface `address` remain IPv4 compatibility spellings; old YAML
loads unchanged. A route's gateway determines its family, and an explicit
destination must match it. Scoped, mapped, multicast, unspecified, loopback,
off-link, duplicate, and cross-family topology addresses fail validation.
Static addresses use `nodad`: validation already proves uniqueness, and this
prevents listeners racing an IPv6 address that is still tentative.

### Routed network scenarios

```sh
./netdoc-sim run healthy-routed-network
./netdoc-sim run gateway-unreachable
./netdoc-sim run wrong-default-route
./netdoc-sim run multiple-interfaces-wrong-preferred-route
```

- `healthy-routed-network` puts client and target on different bridges. The
  target, DNS, direct egress, and public-DNS checks succeed only through the
  two-interface gateway.
- `gateway-unreachable` replaces the healthy default with an on-link address
  that has no owner. `ip route get` still selects it, while neighbor discovery
  records failure. This is structurally different from `no-default-route`,
  where route selection itself fails.
- `wrong-default-route` selects a locally reachable router attached to an
  isolated upstream. A second real netdoc run reaches the target over a
  specific route through the correct router.
- `multiple-interfaces-wrong-preferred-route` gives the client two UP links and
  two reachable gateways. Linux selects the isolated default at metric 50 over
  the working default at metric 100; a source-selected netdoc run and specific
  route prove the other interface reaches the target.

The backend runs family-specific `ip route get <validated-address>` inside the
client namespace and parses only `via`, `dev`, `src`, and `metric`. It maps the generated device back to a
logical segment and rejects unknown, oversized, or unexpected output. Neighbor
state comes from `ip neigh` after the real probes. Reports therefore prove the
kernel's choice rather than repeating the YAML configuration.

Router holders write and read back IPv4 and/or IPv6 forwarding from inside
their own network namespace. The integration test also reads the host values
before and after a routed run and requires them to remain identical.
If namespaced forwarding cannot be enabled, setup fails clearly; the simulator
never retries against the host sysctl or asks for elevated execution.

### Dual-stack scenarios

```sh
./netdoc-sim run dual-stack-healthy
./netdoc-sim run ipv4-works-ipv6-broken
./netdoc-sim run ipv6-works-ipv4-broken
```

The control assigns static IPv4 plus IPv6 to both routed segments, installs
both default routes, enables both forwarding families in the router holder,
and serves the fixed netdoc endpoints on both families. The mirror failures
leave both families configured and DNS healthy but add one validated nftables
rule in the router's `postrouting` hook, restricted by `meta nfproto`, that
drops only forwarded IPv6 or only forwarded IPv4.

These built-ins use the RFC 3849 documentation prefix `2001:db8::/32`, still
entirely inside the private namespaces. Netdoc intentionally does not interpret
a ULA alone as a promise of public IPv6 egress, so a ULA would weaken the
diagnostic assertion. Neither choice creates a host or public route.

The DNS service's `records` list allows an A and AAAA address for one name:

```yaml
services:
  - name: dual-resolver
    type: dns
    records:
      - {name: dual-target.test, address: 10.78.2.20}
      - {name: dual-target.test, address: 2001:db8:77:2::20}
```

Query evidence records `A` and `AAAA` independently. DNS success is never
treated as transport success. The real `internet_tcp` probe separately dials
numeric endpoints using `tcp4` and `tcp6` and emits the additive JSON object
`address_families: {ipv4: ..., ipv6: ...}`. Thus normal hostname fallback
cannot hide a failed family. A configured family failure produces WARN with
`ipv4_unreachable` or `ipv6_unreachable`; complete failure remains FAIL.

Route evidence runs `ip -4 route get` and `ip -6 route get` in the client
namespace and maps the returned kernel device to its logical segment. Family-
specific reachability evidence comes from the actual netdoc attempts rather
than the YAML. The dual-stack integration control snapshots host IPv4 and IPv6
forwarding, both host route tables, and host interfaces before and after the
run. All must be identical.

Every IPv6 node holder first writes and reads back its namespace-local
`conf/all/disable_ipv6` and `conf/default/disable_ipv6`; routers independently
write and read back IPv4 and IPv6 forwarding only when that family is present
on two interfaces. If these operations are unavailable, setup reports the
specific namespace sysctl instead of changing the host or requesting root.

Proxy configuration is deliberately not a raw URL or environment map. The
schema accepts only `socks5` or `socks5h`, a validated node reference, and a
port that belongs to a declared `socks5` service. The runner then constructs
one canonical `ALL_PROXY` value for the real netdoc process. Scenario files
cannot name executables, paths, commands, resolver text, or arbitrary SOCKS
options. Non-empty service names are topology-wide unique; DNS names are
compared case-insensitively when duplicate records are rejected.

### `aliases`, and why the simulated internet answers on 1.1.1.1

A scenario claims netdoc's hardcoded probe endpoints for a node inside the
simulation, so those probes run their real code path against a server that is
three hops from nowhere. Nothing leaves the namespace. See
[the endpoint-drift warning above](#-scenarios-depend-on-netdocs-hardcoded-probe-endpoints)
for the full list and what to do when it changes.

### SOCKS5 versus SOCKS5h

The two built-ins use the same network and the same private destination:

```sh
./netdoc-sim run socks5-local-dns-fails
./netdoc-sim run socks5h-remote-dns-succeeds
```

`socks5://` means netdoc first resolves `connectivitycheck.gstatic.com` with
the client namespace's resolver and sends an IP-address CONNECT request.
`socks5h://` means netdoc sends that hostname in the SOCKS request and the
proxy resolves it with the resolver configured in the proxy namespace. The
private record exists only in the proxy resolver's zone.

The client is filtered from connecting directly to the private service, so a
successful tunnel cannot be an ordinary direct connection. These scenarios do
not use the public internet: `1.1.1.1`, `8.8.8.8`, the probe hostname, proxy,
resolver, and destination are all simulator-owned addresses and listeners
inside the throwaway user namespace.

The report proves DNS location with two independent structured sources:

- DNS evidence identifies the resolver node, source address, queried name,
  query type, result, and count.
- SOCKS evidence records an accepted greeting separately from CONNECT and, for
  each request, the address type (`ipv4`, `ipv6`, or `domain`), destination,
  port, result, and count.

In `socks5-local-dns-fails`, the proxy greeting is accepted, the client
resolver observes NXDOMAIN from `10.77.0.10`, and no CONNECT is sent. In
`socks5h-remote-dns-succeeds`, the client sends no matching DNS query, the
proxy receives a domain CONNECT, its resolver observes A/AAAA queries from
`10.77.0.30`, and the CONNECT result is `connected`. Success is therefore not
used as a guess that remote resolution happened.

The scenario fragment is intentionally narrow:

```yaml
- name: proxy
  address: 10.77.0.30
  resolver: 10.77.0.30
  services:
    - name: private-resolver
      type: dns
      zone:
        connectivitycheck.gstatic.com: 10.77.0.40
    - {name: socks-proxy, type: socks5, port: 1080}

tests:
  - node: client
    target: 10.77.0.20:9999
    proxy: {scheme: socks5h, node: proxy, port: 1080}
```

The SOCKS service implements no-auth CONNECT only. It supports IPv4, IPv6,
and domain-name destinations, valid RFC 1928 reply codes, bounded handshakes,
connection lifetime, and copied bytes. Authentication, BIND, UDP ASSOCIATE,
and proxy chaining are unsupported. Domain resolution is explicitly dialed to
the proxy node's validated resolver; it never falls back to the director or
host resolver. No new dependency or external daemon is used.

### Deterministic TLS failures

Three built-ins run the real netdoc TLS path without a public CA or internet
connection:

```sh
./netdoc-sim run tls-valid
./netdoc-sim run tls-expired-certificate
./netdoc-sim run tls-hostname-mismatch
```

All three resolve `secure-target.test` to a local TLS service and leave TCP,
ordinary HTTP, simulated egress, and both DNS views healthy. This isolates the
certificate property under test:

- `tls-valid` presents a currently valid leaf for `secure-target.test`, signed
  by the trusted simulator CA. TLS and the follow-up HTTPS check pass.
- `tls-expired-certificate` presents an otherwise valid, trusted,
  hostname-matching leaf whose `NotAfter` is 24 hours before service startup.
  The `tls` row fails with `certificate_expired`; HTTPS is skipped because its
  TLS prerequisite failed.
- `tls-hostname-mismatch` presents a currently valid, trusted leaf for
  `different-target.test`. The requested SNI remains `secure-target.test`, and
  the `tls` row fails with `hostname_mismatch` rather than expiry or issuer.

The node holder generates a private ECDSA P-256 CA and leaf with Go's standard
library. Serial numbers, SANs, key usages, and validity offsets are explicit;
unit tests use a fixed evaluation instant, while a live service derives its
wide valid or unambiguously expired window once at startup so the real netdoc
process can continue using its normal clock. Private keys remain in the target
holder's memory and are never written, logged, or reported.

Only the public CA certificate crosses the holder boundary. It is created with
mode `0600` under the run's mode-`0700` simulator workspace. A validated
`trust: {service: tls-target}` reference causes trusted simulator code—not
scenario YAML—to derive that path and add `SSL_CERT_FILE` to that one netdoc
process. The runner strips inherited `SSL_CERT_FILE` and `SSL_CERT_DIR` first.
It never changes `/etc/ssl`, `/etc/pki`, or the host trust store, and cleanup
removes the bundle and workspace. Scenario files cannot supply paths, PEM,
private keys, algorithms, or arbitrary environment variables.

TLS evidence records the node/service, certificate mode, requested SNI, SANs,
`NotBefore`/`NotAfter`, whether certificate selection occurred, server-side
handshake result, and count. A raw TCP control connection is distinguishable
from a certificate-bearing handshake. The report contains no key material.

The service accepts bounded concurrent TLS 1.2-or-later handshakes and answers
a successfully parsed HTTP request with a minimal HTTP/1.1 `204`. That response
exists because netdoc's HTTPS row follows a successful TLS row; it is not full
HTTPS response validation. OCSP, CRLs, mutual TLS, TLS-version matrices, cipher
testing, session resumption, and arbitrary application protocols are outside
this slice. The existing TLS scenarios remain IPv4-only; certificate validation
stays hostname-based and is not changed by dual-stack topology support.

### Faults

| type | fields | effect |
| --- | --- | --- |
| `drop` | `family`, `direction`, `to`, `protocol`, `port` | discards matching packets, optionally for one family |
| `netem` | `segment`, `delay`, `jitter`, `loss` | impairs one logical node link (`tc netem`) |
| `no_default_route` | `family` | deletes that family's default (IPv4 when omitted for compatibility) |
| `replace_default_route` | `family`, `via`, `metric` | replaces that family's defaults with one validated on-link gateway |
| `link_down` | `segment` | administratively lowers one logical interface |
| `scheduled_netem` | `segment`, `seed`, `events[]` | moves one link between netem states at timed offsets |
| `scheduled_dns` | `service`, `events[]` | moves one DNS service between response states at timed offsets |
| `scheduled_link` | `segment`, `events[]` | raises and lowers one logical interface at timed offsets |

`drop` has two directions and they are **not** interchangeable:

- `outbound` (default) drops on the way out of the node. The kernel reports this
  back to the local sender as `EPERM` — an immediate refusal, the way a host
  firewall behaves.
- `inbound` drops on arrival at the node. The sender is told nothing and waits
  out its own timeout — the way a black hole in the path behaves.

Simulating "my resolver is unreachable" needs `inbound` on the resolver's node.
Getting this wrong turns a four-second timeout into an instant error and quietly
changes what the scenario tests.

### Timed faults

The first five fault types are applied once, before the tests run, and stay put.
The `scheduled_*` types change the network **while netdoc is running**:

```yaml
faults:
  - type: scheduled_netem
    node: client
    segment: lan
    seed: 1414213562
    events:
      - {at: 0ms,    latency: 10ms, jitter: 2ms}
      - {at: 200ms,  latency: 700ms, jitter: 150ms}
      - {at: 1500ms, latency: 10ms, jitter: 2ms}

  - type: scheduled_dns
    service: spike-resolver      # the node is derived from the service
    events:
      - {at: 0ms,    outcome: delay, delay: 300ms}
      - {at: 700ms,  outcome: drop}
      - {at: 1500ms, outcome: answer}

  - type: scheduled_link
    node: target
    segment: lan
    events:
      - {at: 150ms, state: down}
      - {at: 900ms, state: up}
```

**T0 is the instant just before the first netdoc process starts.** Every offset
is measured from it, and one timeline spans the whole test phase, so a scenario
with three tests has three netdoc runs on one clock.

The simulator controls the *requested* timeline. Normal OS scheduler timing may
shift actual application by a small amount, and the report says so explicitly by
recording both numbers. Nothing here is hard real time, and no test asserts
nanosecond-perfect execution.

`scheduled_dns` outcomes:

| outcome | behaviour |
| --- | --- |
| `answer` | the ordinary static zone answer |
| `servfail` | an authoritative SERVFAIL, echoing the question |
| `drop` | no reply at all, so the client waits out its own timeout |
| `delay` | the correct answer, sent after a bounded delay |

A `delay` answer already accepted before a transition is still delivered:
changing the state changes what happens to the *next* query. Delayed answers run
on goroutines the DNS service owns and joins on shutdown, bounded by both the
5 s maximum delay and a cap on how many can be in flight.

The whole timeline is resolved and validated before any namespace exists. Events
must be non-negative, strictly increasing within a fault, at most 16 per fault
and 64 per scenario, and no later than 30 s; latency and jitter are capped at
10 s, loss is 0–100 and rejects NaN and infinity, and a delayed response is
capped at 5 s. As everywhere else in the simulator, a scenario file names
logical nodes, segments and services — never a device name, qdisc handle,
`tc` expression, or `ip` argument. The argv is generated here.

#### Scheduler lifecycle

One goroutine, one timer, sorted events, `select` on `ctx.Done()`. No
`time.After` in a loop, no goroutine per event, and no ordering derived from
wall-clock timestamps.

The runner cancels the scheduler and **joins it** after the last test and before
evidence collection or cleanup, so no scheduled change can reach an environment
that is being dismantled. Cancellation before the first event, during a sleep,
or during an application all stop the timeline; every event the run did not
reach is recorded as `skipped` rather than silently dropped. A failed
application is recorded with its error and the rest of the timeline still runs.

#### What the report shows

```text
Fault timeline   (offsets from T0, just before the first netdoc run)
  +0ms      spike-resolver answers after 300ms        applied at +0s      dns-applied
  +0ms      client/lan latency 10ms, jitter 2ms       applied at +11ms    kernel netem: delay 10ms 2ms
  +200ms    client/lan latency 700ms, jitter 150ms    applied at +215ms   kernel netem: delay 700ms 150ms
  +1500ms   client/lan latency 10ms, jitter 2ms       applied at +1.514s  kernel netem: delay 10ms 2ms
  netdoc "one run spanning the spike" ran +0s..+1.924s
```

JSON keeps the structured form under `fault_timeline`, with `fault_timeline_id`
identifying the requested timeline. Each test carries `start_offset_ms` and
`end_offset_ms`, and each DNS query carries the offset it arrived at, so the
report can answer which fault state was active when a probe ran and whether a
transition happened during it.

The timeline fingerprint is computed from requested values only — offset, fault
type, node, segment, service, and normalized parameters. Actual application
timestamps, the simulation id, kernel interface names, error text and wall-clock
time are all excluded, so two equivalent generated timelines hash identically.

### Expectations

Matching is on netdoc's stable machine-readable contract — the probe ids from
`internal/diagnostic` (`dns`, `internet_tcp`, `target_tcp`, `tls`, `http`, …),
the `PASS`/`WARN`/`FAIL`/`SKIP`/`N/A` vocabulary, and the one-word verdict.
Never on the English prose, which is free to change. A failing or warning
expected check may also name a stable `cause`; a status match with the wrong cause becomes a
`wrong_cause` comparison without being counted as a false positive or false
negative. An `internet_tcp` expectation may additionally require `ipv4` and
`ipv6` to be `reachable` or `unreachable`.

An unknown probe id is a validation error, so a scenario cannot silently expect
something that does not exist.

## The report

Both renderings carry the same data; `-json` is the one to consume from CI.
The JSON report also includes `evidence.dns`, `evidence.dns_queries`,
`evidence.socks_requests`, `evidence.tls`, `evidence.tcp_resets`,
`evidence.packet_conditions`, `evidence.links`, `evidence.routes`,
`evidence.routers`, and `evidence.reachability`; empty collections are rendered
as `[]`. Link and route
entries use logical segment names, not generated Linux device names. Netdoc's existing
`proxy_connect` row carries an optional stable `cause` when it fails:
`proxy_unreachable`, `client_dns_failure`, `proxy_side_dns_failure`,
`destination_unreachable_from_proxy`, or `proxy_protocol_failure`.
The existing `tls` row uses `certificate_expired`,
`certificate_not_yet_valid`, `hostname_mismatch`, `untrusted_issuer`,
`tls_handshake_failure`, `tcp_unreachable`, `timeout`, or `connection_closed`.
On Linux the existing `internet_tcp` row may carry `no_default_route`,
`gateway_unreachable`, `selected_path_failed`, or `preferred_route_failed`.
The last cause means only that the kernel preferred one of multiple configured
defaults and that selected path failed; it does not claim an alternate works.
For partial-family connectivity the row carries `ipv4_unreachable` or
`ipv6_unreachable` and an additive `address_families` object. Route and
reachability evidence also name their family.

```
Test: name that will not resolve — netdoc example.test:80 in client (4.016s)
  Summary: System DNS cannot resolve example.test, but public DNS can — …
  Verdict: dns   ✓
  Expected findings: 6   matched: 6   false negatives: 0   false positives: 0
  ✓ iface          PASS  interface ne93b7b80 is up
  ✓ internet_tcp   PASS  IPv4 egress via 1.1.1.1 in 1ms …
  ✓ dns            FAIL  cannot resolve example.test via 10.77.0.1: … i/o timeout
  ✓ dns_public     PASS  example.test → 10.77.0.20 (via 8.8.8.8)
  ✓ target_tcp     SKIP  skipped — a prerequisite failed
  ✓ http           SKIP  skipped — a prerequisite failed
  Timed out: dns

Suggested improvements
  [probe_timed_out] dns failed by exhausting the probe timeout rather than
      returning an answer — consider a cheaper negative signal …

Cleanup:  ok — every namespace and process released
```

## Deterministic fault campaigns

A campaign is a validated scenario with bounded variable ranges. It runs each
iteration sequentially through the ordinary `Run` lifecycle: new namespace
topology, real netdoc process, comparison, evidence, and complete cleanup.
There is no simulator-side diagnosis engine and no parallel campaign mode.

```sh
./netdoc-sim campaign unstable-connectivity --runs 6 --seed 12345
./netdoc-sim campaign unstable-connectivity --seed 12345 --iteration 3
./netdoc-sim campaign unstable-connectivity --runs 100 --seed 12345 --fail-fast
./netdoc-sim campaign unstable-connectivity --runs 6 --seed 12345 --json
```

If `--seed` is omitted, trusted CLI code chooses a signed 64-bit seed with
`crypto/rand` and prints it. Iteration seeds are the first 64 bits of SHA-256
over a versioned domain, the campaign seed's two's-complement bits, scenario
name, and iteration number. Consequently iteration N does not depend on PRNG
state from any earlier iteration. The same scenario, root seed, iteration, and
simulator version compile to the same schedule.

Compilation happens before the environment starts. One owned `math/rand.Rand`
resolves duration and percentage ranges, a nonzero netem seed, and the complete
per-family DNS response sequence. No service goroutine draws randomness, and
map iteration order is never an input. The schedule and its digest are stored
with the derived seed in every iteration result. A failure prints a direct
`--seed … --iteration …` command; JSON exposes the same fields structurally.

The aggregate counts every comparison failure, FP, FN, timeout, and structured
diagnosis fingerprint. A fingerprint contains only test name, verdict,
ProbeID, status, cause, and address-family state, in canonical order. It omits
timings, timestamps, run IDs, temporary paths, kernel device names, and raw
errors. Fingerprints are compared as instability only when their complete fault
schedule digest is identical; different injected conditions are not called a
nondeterministic diagnosis. Duration min/median/max values are descriptive,
not a claim of statistical significance.

### Fault mechanisms and fixed controls

- `packet-loss` applies 10% seeded netem loss. `tc -s qdisc` readback proves
  the qdisc is active and records the kernel's dropped-packet counter; the real
  netdoc report proves useful traffic still completed.
- `high-jitter` applies 120 ms latency with 100 ms seeded jitter and repeats the
  real probe five times. Successful attempt records supply min/max RTT and
  sample count. Netdoc currently evaluates one RTT per probe, so the report
  emits the deterministic `jitter_sampling_gap` suggestion instead of inventing
  a jitter diagnosis.
- `intermittent-dns` uses synchronized A and AAAA sequences. Each query records
  node, source, name, type, family-local sequence number, scheduled outcome, and
  actual `ANSWER`/`NODATA`/`NXDOMAIN`/`SERVFAIL` result. Exhausted schedules
  answer normally; indexing cannot run past a slice.
- `tcp-reset` accepts a TCP connection, performs one bounded read, sets
  `SO_LINGER=0`, and closes. Evidence distinguishes accepted and reset events;
  the SSH banner probe uses the stable `connection_reset` cause. The service is
  not a general TCP fault proxy.
- `unstable-connectivity` varies bounded latency, jitter, and explicit DNS
  SERVFAIL patterns. The fixed packet-loss scenario separately tests nonzero
  kernel loss because packet arrival/order can itself expose timing-sensitive
  diagnosis changes.

Netem supports fixed latency, jitter, loss from 0–100%, and an optional seed.
The seed needs iproute2 6.6 or newer, so a scenario that pins loss or jitter to
one does not run on Ubuntu 24.04 or Debian bookworm, which ship 6.1. Everything
else works there.
Durations are capped at 10 seconds. It does not accept qdisc handles, raw tc
expressions, corruption, duplication, or reordering. Campaigns are fault
injection and repeated diagnostic testing, not network-performance benchmarks.

### Timed campaigns

`flapping-connectivity` generates one bounded timeline per iteration. The shape
is fixed and three dimensions vary, so a failing iteration still reads as one
sentence:

```text
+0                             healthy
+degrade_at                    degraded (loss = degrade_loss_percent)
+degrade_at+400ms              healthy
+degrade_at+800ms              outage — 100% loss, resolver silent
+degrade_at+800ms+outage_for   healthy again
```

```yaml
campaign:
  runs: 6
  timeline:
    node: client
    segment: lan
    service: flapping-dns
    resolver_hold: 600ms
    latency: 10ms
    degrade_at: {min: 150ms, max: 350ms}
    degrade_loss_percent: {min: 10, max: 60}
    outage_for: {min: 300ms, max: 700ms}
```

`resolver_hold` is a metronome, not a fault: netdoc issues every resolver query
in the first few milliseconds of a run, so without something holding the run
there, a timeline measured in hundreds of milliseconds would be changing a
network nobody was looking at.

One run spans the whole flap, so its verdict legitimately depends on which phase
each probe landed in. **This campaign is expected to report mismatches** — its
value is the reproducibility of the timelines and the timeline-aware
suggestions, not a green exit code. What reproduces exactly from `--seed S
--iteration N` is the iteration seed, the schedule, and the timeline
fingerprint; netdoc's answer to a transition that lands on a probe boundary is
genuinely a coin flip, which is the finding.

`dns-timeout-boundary` sweeps one variable — the delay the resolver holds every
answer for — across a probe deadline, which is where off-by-one deadline
behaviour, changed error classification and cleanup races live:

```sh
./netdoc-sim campaign dns-timeout-boundary --seed 12345 --runs 6 -timeout 1s
./netdoc-sim campaign dns-timeout-boundary --seed 12345 --iteration 3 --runs 5
```

`--iteration N --runs K` repeats one iteration K times. That is the only way the
divergence check has anything to compare: every iteration otherwise draws
parameters of its own, so each schedule fingerprint is a group of one and two
runs that disagree about the same network look like two different networks.

### Timed fault scenarios

- `transient-dns-outage` — the resolver answers, goes silent, and recovers, all
  inside one diagnostic session. Recovery lands while the failing run is still
  waiting out its deadline; netdoc does not resample, so the report says so
  rather than pretending it noticed.
- `latency-spike` — a 700 ms spike opens and closes inside one run. The kernel
  qdisc is read back at each state and the run measures the same path at two
  speeds.
- `transient-connectivity-loss` — the target's link drops for 750 ms. It is the
  target's link, not the client's, so the client's addresses, routes and default
  gateway are provably untouched and only the transport went away.
- `fault-during-probe` — the resolver's state changes while one of its answers
  is still held. The query evidence records when it arrived and how long it was
  held, so the overlap is read from the service rather than assumed from a
  sleep. A probe-lifecycle regression test, not a user-facing diagnosis.

CI runs the timed scenarios with a short probe timeout; their timelines are
designed so the transitions land the same way at any timeout above a few
hundred milliseconds. Longer local runs are worth it for the campaigns:

```sh
./netdoc-sim campaign flapping-connectivity --runs 50 --seed 12345
./netdoc-sim campaign dns-timeout-boundary --runs 20 --seed 12345 -timeout 1s
./netdoc-sim campaign dns-timeout-boundary --iteration 3 --runs 20 -seed 12345 -timeout 1s
```

Campaign exit codes extend the existing simulator contract: `0` every
iteration matched, `1` at least one comparison mismatch, `2` invalid CLI or
scenario input, and `3` simulator/runtime/cancellation error. `--fail-fast`
stops after the first mismatch or error but retains that full report and runs
normal cleanup. Without it all requested iterations run. CI should use a small
5–10 iteration campaign; larger local campaigns remain capped at 1000 runs.

### Suggestion rules

Each is a plain function of the report, with a stable code so CI can allow-list
the ones a scenario is known to trip.

| code | fires when |
| --- | --- |
| `missed_finding` | the scenario broke something and netdoc did not say so |
| `wrong_severity` | the finding was made, at the wrong level |
| `wrong_cause` | the failure status matched but its structured cause did not |
| `false_positive` | netdoc flagged something the scenario expects to be fine |
| `wrong_verdict` | the headline classification does not match the injected cause |
| `probe_timed_out` | a probe failed by exhausting its deadline rather than answering |
| `no_fix_hint` | a `FAIL` or `WARN` row told the user what broke but not what to do |
| `nondeterministic` | `-repeat` runs of an unchanged network disagreed |
| `no_diagnosis` | netdoc produced no report at all |
| `gateway_unreachable` | kernel route selection succeeded but neighbor resolution for its next hop failed |
| `wrong_default_route_evidence` | selected default failed while a controlled specific route reached the target |
| `alternate_route_available` | preferred path failed while another live interface and controlled route reached the target |
| `transient_fault_not_resampled` | the resolver recovered while the run was still going and netdoc never asked it again |
| `transient_fault_reported_permanent` | a failure that had already healed is described without any hint that it was a moment rather than a state |
| `transient_fault_missed` | an impairment opened and closed entirely inside one run and nothing was flagged |
| `timeline_inconsistent` | a probe succeeded while a probe it depends on failed, and no fault transition during that run explains it |

`transient_fault_reported_permanent` inspects the wording on purpose. A
diagnosis that says it is describing a moment — "at the time of this check",
"transient", "retry" — is making a point-in-time claim and is not wrong merely
because the network recovered afterwards, so it does not fire.

`timeline_inconsistent` is where timing earns its keep. The same impossible pair
of rows is *temporally explainable* when a fault transition happened during that
run, and *internally inconsistent* when the network did not change at all. Only
the second is reported.

Exit codes: `0` the diagnosis matched, `1` it did not, `2` bad arguments, `3` the
simulation could not run.

## Tests

```sh
go test ./internal/simulation                              # parsing, comparison, DNS wire format — rootless, offline
go test -tags netns_integration ./internal/simulation      # builds real namespaces; skips itself if unavailable
```

The integration tests need unprivileged user namespaces but not root. They build
both binaries from the tree under test, run scenarios end to end, and assert
that no simulation interface is visible on the host afterwards.

Set `NETDOC_SIM_REQUIRE_NETNS=1` when a machine or CI job is required to run
them. Without it, unavailable user namespaces produce a skip; with it, the
same condition is a failure. `netdoc-sim capabilities` names the relevant
kernel or AppArmor setting and required `ip`, `nsenter`, `nft`, and `tc` tools.
Dual-stack setup tests IPv6 in the created node namespaces themselves; a host
knob alone is not treated as proof that child namespaces can use it.

## Limitations

- **Linux only.** `Backend` is an interface and `DefaultBackend` returns a
  clear capability message elsewhere, but the only implementation is
  `linux-netns`. Podman, libvirt/QEMU, Hyper-V network compartments and macOS
  VMs would slot in behind the same two interfaces.
- **Static IPv4/IPv6 unicast routing only.** Multiple L2 segments, dual-stack
  interfaces, router nodes, default/specific routes, and metrics are supported.
  NAT/NAT66, SLAAC, router advertisements, DHCPv6, IPv6 policy routing, ECMP,
  link-local routing, routing loops, dynamic routing protocols, VPN/tunnel
  devices, and bridge VLAN filtering are not.
- **Six service types.** `dns`, `http`, `tcp`, `tcp_reset`, and the deliberately
  limited `socks5` and `tls` services described above. None is a general-purpose
  daemon or protocol conformance suite.
- **The DNS server is deliberately tiny.** Static A/AAAA from a compatibility
  zone map or records list, `NODATA` for a family it does not have, `NXDOMAIN` for
  everything else. No PTR, no CNAME, no delegation, no TCP.
- **Timed faults are best-effort in time, exact in content.** The requested
  timeline is deterministic and reproducible from a seed; when each event
  actually lands is subject to ordinary OS scheduling, and the report records
  both numbers rather than claiming they are equal. There is no hard real-time
  guarantee and none is asserted.
- **Probe-level timing is inferred, not read.** netdoc's JSON exposes per-probe
  durations but not start times, so the simulator correlates its own
  observations — the netdoc process window and the arrival offset of each DNS
  query — rather than parsing human-readable output or changing the public JSON
  contract. Where exact probe timing is unavailable, the report says only what
  the simulator can prove.
- **Campaigns are sequential.** Parallel execution, cross-host coordination,
  and statistical significance analysis are intentionally absent.

## Roadmap

`netdoc-sim scenarios` lists what the library ships today. In rough order of
what each additional scenario costs:

1. Free with what exists — `dns` returns NXDOMAIN (leave the name out of the
   zone), TCP port blocked, connection refused, packet loss, DNS is slow,
   HTTP returns an error, or a missing route to a specific subnet.
2. Extends the TLS service — an untrusted-issuer scenario, client certificates,
   revocation, or version/cipher policy.
3. Extends the SOCKS service/scenarios — authentication policy, proxy reachable
   but destination unreachable, and deliberately broken proxy-side DNS.
4. Extends multi-segment topology — routing loops, policy routing, tunnels,
   NAT/NAT66, and split DNS across routed segments.

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

The IPv6 endpoints are deliberately **not** simulated. The scenarios are
IPv4-only, so `internet_tcp` reports "no IPv6 egress", which is correct for the
network being simulated and is what the expectations assume.

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
        ├── bridge nb<id>              the shared L2 segment
        ├── node holder × N            each in its own net + mount namespace
        │     └── test services        dns / http / tcp / socks5
        └── nsenter … netdoc -json     the diagnostic under test, inside a node
```

The **director** owns the segment and does all the wiring from outside the
nodes, via `ip` and `nsenter` with argv slices — no scenario string ever reaches
a shell. A **node holder** exists only to own one simulated machine's network
and mount namespaces and to run that machine's services inside them; the
director talks to it over three lines on a pipe, so no probe can race the
topology into existence.

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

### Faults

| type | fields | effect |
| --- | --- | --- |
| `drop` | `direction`, `to`, `protocol`, `port` | discards matching packets |
| `netem` | `delay`, `jitter`, `loss` | impairs the node's link (`tc netem`) |
| `no_default_route` | — | deletes the node's default route |

`drop` has two directions and they are **not** interchangeable:

- `outbound` (default) drops on the way out of the node. The kernel reports this
  back to the local sender as `EPERM` — an immediate refusal, the way a host
  firewall behaves.
- `inbound` drops on arrival at the node. The sender is told nothing and waits
  out its own timeout — the way a black hole in the path behaves.

Simulating "my resolver is unreachable" needs `inbound` on the resolver's node.
Getting this wrong turns a four-second timeout into an instant error and quietly
changes what the scenario tests.

### Expectations

Matching is on netdoc's stable machine-readable contract — the probe ids from
`internal/diagnostic` (`dns`, `internet_tcp`, `target_tcp`, `tls`, `http`, …),
the `PASS`/`WARN`/`FAIL`/`SKIP`/`N/A` vocabulary, and the one-word verdict.
Never on the English prose, which is free to change.

An unknown probe id is a validation error, so a scenario cannot silently expect
something that does not exist.

## The report

Both renderings carry the same data; `-json` is the one to consume from CI.
The JSON report also includes `evidence.dns` and
`evidence.socks_requests`; empty collections are rendered as `[]`. Netdoc's
existing `proxy_connect` row carries an optional stable `cause` when it fails:
`proxy_unreachable`, `client_dns_failure`, `proxy_side_dns_failure`,
`destination_unreachable_from_proxy`, or `proxy_protocol_failure`.

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

### Suggestion rules

Each is a plain function of the report, with a stable code so CI can allow-list
the ones a scenario is known to trip.

| code | fires when |
| --- | --- |
| `missed_finding` | the scenario broke something and netdoc did not say so |
| `wrong_severity` | the finding was made, at the wrong level |
| `false_positive` | netdoc flagged something the scenario expects to be fine |
| `wrong_verdict` | the headline classification does not match the injected cause |
| `probe_timed_out` | a probe failed by exhausting its deadline rather than answering |
| `no_fix_hint` | a `FAIL` or `WARN` row told the user what broke but not what to do |
| `nondeterministic` | `-repeat` runs of an unchanged network disagreed |
| `no_diagnosis` | netdoc produced no report at all |

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

## Limitations

- **Linux only.** `Backend` is an interface and `DefaultBackend` returns a
  clear capability message elsewhere, but the only implementation is
  `linux-netns`. Podman, libvirt/QEMU, Hyper-V network compartments and macOS
  VMs would slot in behind the same two interfaces.
- **One L2 segment, IPv4 only.** Routing loops, misconfigured subnets, multiple
  interfaces with a wrong preferred route, and IPv4-vs-IPv6 asymmetry all need a
  second link type and per-node routing tables. The scenario schema has room for
  it; the backend does not do it yet.
- **Four service types.** `dns`, `http`, `tcp`, and the deliberately limited
  `socks5` CONNECT service described above. A TLS service for expired and
  invalid-certificate scenarios is not implemented.
- **The DNS server is deliberately tiny.** Static A/AAAA from a zone map, one
  address per name, `NODATA` for a family it does not have, `NXDOMAIN` for
  everything else. No PTR, no CNAME, no delegation, no TCP.
- **Faults are static.** Injected once, after setup. Intermittent failures need
  a fault schedule.
- **`-repeat` is the only nondeterminism check**, and it compares verdicts only.

## Roadmap

The scenario library ships six: `healthy`, `broken-dns`, `no-default-route`,
`high-latency`, `socks5-local-dns-fails`, and
`socks5h-remote-dns-succeeds`. In rough order of what each additional scenario
costs:

1. Free with what exists — `dns` returns NXDOMAIN (leave the name out of the
   zone), TCP port blocked, connection refused, packet loss, DNS is slow,
   gateway unreachable, HTTP returns an error.
2. Needs a TLS service — expired/invalid certificate, MITM.
3. Extends the SOCKS service/scenarios — authentication policy, proxy reachable
   but destination unreachable, and deliberately broken proxy-side DNS.
4. Needs multi-segment topology — IPv6-broken-while-IPv4-works and its mirror,
   routing loops, multiple interfaces with a wrong preferred route, split DNS.

# Scenario authoring reference

Authoritative reference for `netdoc-sim` scenario YAML: the schema, service
and fault semantics, and how to add or change one. See
[docs/simulation.md](simulation.md) for setup and the guide index.

## Scenario authoring

Built-ins live in `internal/simulation/scenarios/` and are embedded by
`internal/simulation/library.go`. Start from the closest existing scenario;
the YAML itself is the authoritative example for specialized features such as
routed and dual-stack networks, proxies, TLS, timed faults, and campaigns.

A scenario has four operational parts:

- `topology`: logical segments, nodes, interfaces, routes, resolvers, aliases,
  and bounded test services;
- `faults`: impairments applied before or during the diagnostic;
- `tests`: one or more real netdoc invocations in the client node;
- `expect`: the verdict and probe results that should follow.

This fragment shows the common shape without duplicating a complete scenario:

```yaml
name: broken-dns-with-working-ip-connectivity
topology:
  nodes:
    - {name: client, role: client, address: 10.77.0.10, resolver: 10.77.0.1}
    - {name: resolver, address: 10.77.0.1, services: [{type: dns}]}
faults:
  - {type: drop, node: resolver, direction: inbound, protocol: udp, port: 53}
tests:
  - {node: client, target: example.test:80}
expect:
  verdict: dns
  checks:
    - {id: dns, status: FAIL}
```

Use
[`broken-dns.yaml`](../internal/simulation/scenarios/broken-dns.yaml) as the
complete, validated single-segment example.

The original `topology.subnet`, node `address`, and node `gateway` fields are
single-segment compatibility shorthand. Routed scenarios use named `segments`,
node `interfaces`, and validated `routes`; see
[`healthy-routed-network.yaml`](../internal/simulation/scenarios/healthy-routed-network.yaml).
[`two-path-healthy.yaml`](../internal/simulation/scenarios/two-path-healthy.yaml)
is the focused hunt control for two IPv4 defaults: ordinary traffic selects the
lower-metric preferred router, while a controlled literal target independently
proves the higher-metric alternate path.
[`two-router-healthy.yaml`](../internal/simulation/scenarios/two-router-healthy.yaml)
is the control for the single-default route faults. Its client LAN carries two
routers with different jobs — one holds the default out to the internet, the
other a specific route to the target's subnet — which is what makes "the
default points at the wrong on-link router" and "the specific route is missing"
expressible at all, and distinguishable from each other. Its second client test
is a controlled literal behind the internet router, reached over its own
specific route: that endpoint keeps answering when the default is repointed,
which is how a wrong default route is told apart from the network beyond the
gateway simply being down.
Dual-stack scenarios use `ipv4` and `ipv6` on the same segment and interface;
see
[`dual-stack-healthy.yaml`](../internal/simulation/scenarios/dual-stack-healthy.yaml).

Addresses are parsed and rendered canonically with `net/netip`. Routes accept a
prefix or `default`, never free-form `ip` syntax. Scenario authors cannot
provide kernel interface names, commands, executable paths, arbitrary proxy
URLs or environment variables, certificate keys or paths, qdisc handles, or
raw firewall expressions. The schema accepts logical intent and trusted code
derives the operating-system arguments.

### Services and faults

Do not maintain a second inventory here. The current service and fault types,
their fields, and validation limits are defined in
`internal/simulation/scenario.go` and `internal/simulation/timeline.go`; shipped
examples are discoverable with:

```sh
rg -n 'type:' internal/simulation/scenarios
rg -l 'campaign:' internal/simulation/scenarios
```

Some semantics are easy to get wrong even after reading a scenario:

- A `drop` with `direction: outbound` acts like a local firewall and can return
  an immediate refusal. `direction: inbound` silently discards arrival at the
  destination, so the sender waits for its timeout. Use inbound drops for a
  black-holed remote resolver or service.
- A `pmtu_blackhole` narrows one router interface and drops the ICMP
  fragmentation-needed replies that router would send about it. Both halves are
  required and neither is useful alone: narrowing a hop that still reports the
  smaller MTU is discovered and worked around, and narrowing an endpoint makes
  the local kernel refuse the send instead of losing the packet silently. It is
  rejected on a node that is not a router for that reason. Both endpoints must
  keep the default MTU, so the narrow hop has to be transit between two
  routers; `pmtu-blackhole.yaml` is the worked example.
- Public-looking aliases, including documentation-prefix IPv6 addresses, exist
  only in the private namespace. They do not create host or public routes.
- `socks5` resolves on the client before CONNECT; `socks5h` sends the hostname
  to the proxy's resolver. The paired SOCKS scenarios prove the location with
  DNS and CONNECT evidence rather than inferring it from success.
- TLS, QUIC, and encrypted-DNS fixtures generate private keys and certificates
  in memory. Only public CA certificates are written inside the mode-`0700` run
  workspace; trusted code points the netdoc process at the selected TLS CA and
  at the isolated fixed-endpoint trust directory the QUIC and encrypted-DNS
  fixtures share, without modifying a host trust store.
- The `encrypted_dns` fixture answers netdoc's encrypted-DNS row over both
  transports from one static zone — RFC 8484 DoH on its port and RFC 7858 DoT
  on 853 — and accepts the plain TCP connect the direct-egress row makes, which
  is why the simulated internet serves it on 443 instead of a `tcp` sink. A node
  that claims `1.1.1.1` without it makes every scenario report a blocked
  encrypted resolver; a library test enforces that pairing.

### Timed faults

The `scheduled_netem`, `scheduled_dns`, and `scheduled_link` examples in the
scenario directory change the network while netdoc is running. T0 is the
instant just before the first netdoc process starts, and one timeline spans all
tests in that scenario.

The complete requested timeline is resolved and validated before a namespace
exists. Timing is best-effort: normal scheduling may delay application, so the
report records requested and actual offsets and marks events that were applied,
failed, or skipped. The requested timeline and its fingerprint are
deterministic; wall-clock application is not a hard real-time guarantee.

The runner cancels and joins the scheduler before evidence collection and
cleanup. Keep that lifecycle when extending timed behavior so no event can
reach a topology being dismantled. `internal/simulation/timeline_test.go` owns
the detailed ordering, cancellation, and shutdown contract.

### Expectations and reports

Expectations match netdoc's stable machine-readable contract: probe id,
`PASS`/`WARN`/`FAIL`/`SKIP`/`N/A` status, verdict, and optional structured cause
or address-family state. They never match English diagnosis text. A test may
override the scenario-level expectation when several network phases are
exercised in one file.

Naming `ipv4` or `ipv6` on an expected check asserts that netdoc published that
family's verdict, so a run that omitted the family fails the expectation rather
than passing it by default. Leave the field out for families the scenario makes
no claim about — netdoc omits a family it never dialed, and the report keeps it
omitted rather than reporting an empty verdict.

Unknown YAML keys, probe ids, statuses, causes, node references, addresses, and
unsupported combinations fail validation before setup:

```sh
./netdoc-sim validate path/to/scenario.yaml
./netdoc-sim run path/to/scenario.yaml -dry-run
./netdoc-sim run path/to/scenario.yaml -json
```

Text and JSON reports carry comparison results, suggestions, cleanup state,
and evidence observed from services and the kernel. Use `-json` for automation;
the report types in `internal/simulation/report.go`, campaign/hunt report code,
and their tests are authoritative for fields and stable suggestion codes.
Empty evidence collections remain `[]`, and logical evidence never exposes
generated kernel device names.

## Adding or changing a scenario

1. Copy the closest YAML under `internal/simulation/scenarios/` and change only
   the topology, fault, test, and expectation needed for the behavior.
2. Validate the file by path. If it is built in, also rebuild and validate its
   filename stem through the embedded library.
3. Run the scenario with `-dry-run`, then normally on a supported Linux host.
   Use `-json` to inspect structured evidence rather than matching report prose.
4. Add or adjust the smallest unit test for new parsing, scheduling,
   comparison, or evidence logic. Add a focused `netns_integration` test when
   the behavior must be proved through real namespaces.
5. If diagnostic endpoints changed, update every affected alias/zone and run
   the `healthy` canary — see [Probe endpoint drift](simulation.md#probe-endpoint-drift).

Keep ordinary tests deterministic, rootless, and offline. Real-socket tests
retain the `integration` tag and loopback-only scope; real namespace tests
retain the `netns_integration` tag.

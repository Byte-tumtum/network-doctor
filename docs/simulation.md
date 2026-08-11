# Network simulation (`netdoc-sim`)

`netdoc-sim` builds a throwaway virtual network from a YAML scenario, breaks it
on purpose, runs the real netdoc binary inside it, and reports whether netdoc's
diagnosis matched the injected fault.

It is permanent development and regression-testing infrastructure, not an
end-user product surface. `internal/simulation` is imported by
`cmd/netdoc-sim` alone and does not ship in the `netdoc` binary.

Use the commands themselves for current inventory and flag details:

```sh
CGO_ENABLED=0 go build -o netdoc .
CGO_ENABLED=0 go build -o netdoc-sim ./cmd/netdoc-sim

./netdoc-sim help
./netdoc-sim capabilities
./netdoc-sim scenarios
./netdoc-sim validate broken-dns
./netdoc-sim run broken-dns
```

`netdoc-sim scenarios` is the complete list of built-in scenarios. A scenario
argument may also be a path to a YAML file.

## Purpose and maintenance scope

netdoc must distinguish DNS, routing, transport, proxy, TLS, and service
failures. A simulation makes the fault known by construction, so netdoc's
machine-readable diagnosis can be graded instead of eyeballed. No model decides
whether it was right.

The simulator, including campaigns, hunts, and triage, is maintained for bug,
safety, correctness, determinism, compatibility, and regression work. Add a
fault model or scenario only for a real bug, diagnostic blind spot, reproducible
field condition, regression, or identified missing network behavior relevant to
Network Doctor. General simulator expansion is not a project goal.

## Requirements and safety

The backend is Linux-only. It needs unprivileged user namespaces and the `ip`,
`nsenter`, `nft`, and `tc` tools; `netdoc-sim capabilities` reports the current
host's support and the privileged operations a run would perform. Seeded netem
loss or jitter additionally needs iproute2 6.6 or newer.

Build netdoc with `CGO_ENABLED=0`. A cgo build may resolve through the host's
glibc/system resolver rather than the node's private `/etc/resolv.conf`, which
would test the host instead of the simulation. The namespace integration tests
build both binaries this way.

A run needs no root, sudo, or setuid helper. The launcher re-executes a director
inside a new user, network, and mount namespace, with the caller's uid mapped to
root only there. The director can create bridges, veth pairs, routes, nftables
rules, qdiscs, and low-port listeners inside its owned namespaces, but the
kernel gives it no authority over the host network.

The simulator registers nothing under `/run/netns` or `/etc/netns`. Node
processes carry `PDEATHSIG=SIGKILL`; when the owning process exits, the kernel
reclaims their namespaces and network objects. `-keep` deliberately keeps that
process tree alive for inspection until interrupted or released with
`netdoc-sim cleanup`.

Use these before debugging backend operations:

```sh
./netdoc-sim run broken-dns -dry-run # print generated commands, execute none
./netdoc-sim run broken-dns -v       # log commands as they run
./netdoc-sim run broken-dns -keep    # retain the isolated network
./netdoc-sim list
./netdoc-sim inspect <id>
./netdoc-sim cleanup <id>            # or: ./netdoc-sim cleanup -all
```

The generated `ip`, `nft`, `tc`, and `nsenter` commands are safe because the
director executes them after isolation. Do not copy them into a host shell as a
substitute for running the simulator. Scenario values never become shell
strings; the backend constructs argument slices from validated logical names
and addresses.

## How a run is built

```text
launcher                         host namespaces, no privileges
  └── director                   new user + network + mount namespace
        ├── bridge × segment
        ├── node holder × node   private network + mount namespace
        │     └── test services
        └── nsenter … netdoc     unmodified binary under test
```

Each logical segment is one bridge. A node interface is a veth peer attached to
that bridge; a router is an ordinary node attached to multiple segments. Each
node gets a private generated `/etc/resolv.conf`. `netdoc` runs unmodified in
the selected client node, so the simulator does not reimplement probes or
verdict logic.

Route and neighbor evidence comes from the kernel after the real probes run,
not from repeating the YAML. Generated device names are mapped back to logical
segment names in reports. IPv4 and IPv6 can share one logical interface, while
family-specific routes and reachability remain separate evidence.

Per-family internet reachability is observed the same way: after the probes,
the node holder tries controlled endpoints from inside its own namespace until
one answers. It reads no diagnosis, verdict, or scenario expectation, so this
evidence can contradict netdoc — which is the point of having it. It is a
point-in-time observation of the state the run finished in, so under a timed
fault it describes that instant, not the whole run.

It lands in `family_reachability`, separately from the controlled-target
records below, and carries one of three states per family: `reachable`, `unreachable`,
or `unavailable`. A family the client carries no address for is `unavailable` —
nothing was dialed, so no target or path is named, and untested is not the same
as unreachable. Both families always get a record, so a family with no record
means the measurement never ran; readers treat that as unknown rather than as
an absent family. `unavailable` is also a word netdoc never uses, which is what
keeps the two sides distinguishable in a report.

Multipath scenarios also probe literal test targets when the address and TCP
service are both simulator-owned, providing an independent alternate-path
control. Those records land in `controlled_targets`, use the kernel-selected
route, and are dials the simulator performed itself; single-path, hostname, and
arbitrary external targets are not simulator reachability evidence, and
netdoc's `target_tcp` verdict is never one of these records. Route selection
alone still does not prove reachability.

Both are evidence in the one direction that keeps the simulator honest:
observations independent of the diagnosis establish truth, and the diagnosis is
graded against that truth. Nothing derived from netdoc's report is stored as
simulator evidence, which is why no evidence field carries a diagnosis verdict.

## Probe endpoint drift

**Check this section before changing fixed probe endpoints in
`internal/diagnostic/checks.go` or `internal/diagnostic/encrypteddns.go`.**

The simulation has no internet. Scenarios claim netdoc's compiled-in public
addresses as node aliases and serve its fixed probe names from simulator DNS.
If an internet, captive-portal, default public-DNS, or probe-host constant
changes, update the corresponding `aliases`, DNS records, and expectations
under `internal/simulation/scenarios/`, and the internet endpoint list in
`internal/simulation/runner.go` that the simulator dials for its own
reachability evidence.

`healthy` is the canary. It expects the fixed internet, public-DNS, and
encrypted-DNS probes to pass; endpoint drift makes it fail with a
`false_positive` suggestion naming the stale probe. That failure is
intentional. Update the affected scenarios and rerun:

```sh
./netdoc-sim run healthy
```

Do not make the control tolerant. A second manually maintained endpoint table
would drift for the same reason, so the authoritative values remain in the
production probe files and the scenario files that claim them.

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

## Deterministic campaigns and reproduction

A campaign resolves bounded ranges in a scenario and runs each iteration
sequentially through a fresh ordinary simulation lifecycle. If `--seed` is
omitted, the CLI chooses and prints one. Each iteration seed is derived
independently from the root seed, scenario name, and iteration number; it does
not depend on earlier PRNG state. Compilation fixes the complete fault schedule
before setup, and reports store the seed, schedule, and digest.

```sh
./netdoc-sim campaign unstable-connectivity --runs 6 --seed 12345
./netdoc-sim campaign unstable-connectivity --seed 12345 --iteration 3
./netdoc-sim campaign unstable-connectivity --seed 12345 --iteration 3 --runs 5
./netdoc-sim campaign flapping-connectivity --runs 6 --seed 12345
./netdoc-sim campaign dns-timeout-boundary --runs 6 --seed 12345 -timeout 1s
```

The same scenario, seed, iteration, and simulator version reproduce the same
injected schedule. `--iteration N --runs K` repeats that one schedule, which is
the meaningful way to look for diagnosis divergence. A timed transition can
still land on a probe boundary differently; in that case the deterministic
artifact is the requested schedule and timeline fingerprint, not a promise
that OS scheduling or netdoc's answer is identical.

Campaign scenario definitions document the variables they exercise. In
particular, `dns-timeout-boundary` draws its delay only during campaign
compilation, so `netdoc-sim run dns-timeout-boundary` is not the boundary test.
Use the campaign command and pin the printed seed and iteration to reproduce a
failure.

## Deterministic bug hunts

`netdoc-sim hunt` mutates a known-good control scenario. Case N is derived from
the hunt seed, base scenario, and case number, so `--case N --seed S`
regenerates the case without first running cases 0 through N-1. The accepted
bases and mutation registry live in `internal/simulation/hunt_generate.go`.

```sh
./netdoc-sim hunt healthy --seed 20260101 --cases 20
./netdoc-sim hunt healthy --seed 20260101 --case 4 --json
./netdoc-sim hunt healthy --seed 20260101 --case 4 --dry-run --json
```

Every case report includes generator version, root and case seeds, materialized
mutations, and a case fingerprint. Findings use semantic diagnosis
fingerprints, excluding prose and incidental timing, paths, process ids, and
kernel names. Keep the seed, case, generator version, and reproduction command
with any failure report.

A generated mutation records intent; it is not automatically observed truth.
`observed_faults` contains a mutation only when service, event, kernel-fault, or
independent reachability evidence from the executed simulation supports it.
Persistent netem mutations require matching kernel qdisc state on the intended
logical node and segment. Timed DNS, netem, and link mutations require the
specific impairment event to have applied successfully; initialization,
restoration, failed, and skipped events do not qualify. These timed entries
prove successful simulator state changes, not that netdoc sampled the affected
window or observed an end-to-end consequence.

Hunt analysis compares final family truth with the final client diagnosis only
on stable paths. Unknown or unavailable families, persistent netem, and actual
timed path impairments are not treated as a final-state diagnosis oracle.

`routing.preferred_path_failure` uses `two-path-healthy`. It lowers the
preferred router's upstream interface, beyond the client-visible gateway, so
the lower-metric client route remains selected. Observation requires the
selected preferred family path to be unreachable and the controlled target on
the distinct higher-metric alternate path to remain reachable; successful
link-down application alone is insufficient.

## Triage and nightly automation

`netdoc-sim triage` hunts the fixed baselines, re-runs each candidate's exact
case, and requires both its case fingerprint and finding fingerprint to match.
An unreproduced candidate is reported but never filed. The fixed baselines and
seeds are authoritative in `internal/simulation/triage.go`.

```sh
./netdoc-sim triage                               # observe; file nothing
./netdoc-sim triage --scenarios healthy --cases 5
./netdoc-sim triage --json
./netdoc-sim triage --create                      # create issues through gh
```

`--create` is the only mode that writes to GitHub. It uses the configured `gh`
client, suppresses duplicates by stable fingerprint, and treats a failed hunt,
reproduction, parse, or `gh` call as an error rather than as a clean result.

`.github/workflows/hunt.yml` is authoritative for the nightly schedule, runner,
permissions, case count, and issue-creation opt-in. Scheduled issue creation
requires the `NETDOC_HUNT_CREATE` repository variable; manual dispatch requires
its `create` input. Observation-only runs withhold `GH_TOKEN`, even though the
job declares the permission needed by an opted-in run. Keep the workflow's
explicit Bash/`pipefail` behavior and seeded-netem-compatible runner when
changing it.

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
   the `healthy` canary.

Keep ordinary tests deterministic, rootless, and offline. Real-socket tests
retain the `integration` tag and loopback-only scope; real namespace tests
retain the `netns_integration` tag.

## Tests and CI

The rootless simulator tests and CLI dispatch tests need no namespaces:

```sh
go test ./internal/simulation ./cmd/netdoc-sim
go test -tags integration ./internal/diagnostic ./internal/simulation
```

The end-to-end suite builds real namespaces and skips when the backend is not
available. `-v` is what makes that skip and its reason visible, and `-count=1`
keeps a cached result from standing in for a run:

```sh
go test -tags netns_integration -count=1 -v ./internal/simulation
```

Set `NETDOC_SIM_REQUIRE_NETNS=1` only on a machine or CI job that is required to
exercise the backend; it turns an unavailable backend from a skip into a
failure. The tests themselves remain rootless. CI's throwaway Linux runner
adjusts its host AppArmor setting before this command; `netdoc-sim` never makes
that change or escalates privileges.

See the repository's [Tests section](../README.md#tests) for the complete
validation gate. A documentation-only change does not require running namespace
integration tests unless it exposes a reason to verify namespace behavior.

## Limitations

- Linux is the only maintained backend.
- Topology is static unicast IPv4/IPv6 over simulator-owned bridges: no NAT,
  address autoconfiguration, dynamic routing, tunnels, ECMP, or VLAN model.
- Simulator services are deliberately narrow probe fixtures, not general DNS,
  HTTP, proxy, TLS, QUIC, encrypted-DNS, or TCP implementations.
- Timed faults reproduce requested content and ordering, not hard real-time
  application.
- Campaigns are sequential fault-injection runs, not network-performance or
  statistical-significance tooling.

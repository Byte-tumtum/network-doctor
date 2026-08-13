# Network simulation (`netdoc-sim`)

`netdoc-sim` builds a throwaway virtual network from a YAML scenario, breaks it
on purpose, runs the real netdoc binary inside it, and reports whether netdoc's
diagnosis matched the injected fault.

It is permanent development and regression-testing infrastructure, not an
end-user product surface. `internal/simulation` is imported by
`cmd/netdoc-sim` alone and does not ship in the `netdoc` binary. The one part
meant to be played with rather than scripted is [Challenge
Mode](#challenge-mode), which is still a `netdoc-sim` command and still ships
nowhere near the `netdoc` binary.

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

### Which netdoc gets run

Every command that runs netdoc picks the binary once, in the launcher, where
`$PATH` and the working directory still mean what the user meant by them, and
forwards the resolved absolute path into the namespaces. The order is:

1. `-netdoc` when given. A path (`./netdoc`, `/opt/builds/netdoc`) names that
   file; a bare name (`netdoc`) is looked up on `$PATH`, exactly as a shell
   would. A relative path is resolved against the working directory. An explicit
   binary that does not exist or cannot be executed is an error — nothing falls
   back to a different netdoc, because a run that quietly measured some other
   build is worse than a run that did not happen.
2. A `netdoc` sitting next to the `netdoc-sim` binary. This is what makes
   `./netdoc-sim run healthy` use the `./netdoc` built beside it. A file with the
   right name that this OS will not execute is skipped rather than preferred.
3. A `netdoc` on `$PATH`.

`go run ./cmd/netdoc-sim` puts the binary in a build cache, so step 2 finds
nothing there; build `netdoc-sim` or pass `-netdoc`.

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

### What a hunt false negative means

A hunt false negative means the simulator independently established a network
condition whose diagnostic meaning Network Doctor failed to recognize. It does
not mean a mutation expected probe X to fail and probe X did not fail.

The oracle in `internal/simulation/hunt_oracle.go` is that contract in code. It
runs on a vocabulary of `NetworkCondition` values — domain facts such as IPv4
internet reachability lost, a target serving an expired TLS certificate, a proxy
refusing its CONNECT destination, QUIC datagrams dropped on UDP/443 — and keeps
two halves apart:

- **observed**: reads simulator evidence and derived simulator truth only, never
  `report.tests`. A mutation that was generated or applied establishes nothing;
  only the certificate a client actually refused, the CONNECT a proxy actually
  declined, the kernel counter that actually matched a packet, or the client's
  own dial of a controlled endpoint does.
- **recognized**: reads one diagnosis only, never simulator evidence.

Recognition is expressed over netdoc's cause vocabulary and its structured
`address_families` verdicts, not over probe ids, so a probe that is renamed,
split, or merged without changing what the user is told leaves the oracle
correct. One exception is annotated in the table: `timeout` is not unique in
netdoc's cause vocabulary, so the QUIC entry scopes it to the QUIC row.

Recognition is deliberately specific. An expired certificate reported as a
generic handshake failure, a refused destination reported as an unreachable
proxy, and any unrelated failing row are all misses, because each sends the user
somewhere else. A cause on a passing row is context, not recognition.

Reconciliation runs on the final client diagnosis and only on stable paths.
Unknown or unavailable families, persistent netem, and actual timed path
impairments are not treated as a final-state diagnosis oracle. The opposite
direction — the simulator reached a family the diagnosis calls unreachable — is
reported as `family_reachability_mismatch` with category
`diagnostic_contradiction` rather than as a false negative.

Two observed faults deliberately imply no condition, because netdoc reports no
failure for either by design and the `http-error` control pins that down: an
HTTP error status is a working service answering, and an invalid DoH response
while DoT still resolves is encrypted DNS working. Adding an expectation there
would invent a contract the probes never made.

`routing.preferred_path_failure` uses `two-path-healthy`. It lowers the
preferred router's upstream interface, beyond the client-visible gateway, so
the lower-metric client route remains selected. Observation requires the
selected preferred family path to be unreachable and the controlled target on
the distinct higher-metric alternate path to remain reachable; successful
link-down application alone is insufficient.

### Generator versions

The mutation registry is versioned, because adding an operator to it changes
which mutation every existing case number lands on: selection draws from a
permutation of the applicable operators, so a longer or reordered list repoints
cases that published artifacts already name. `HuntGeneratorVersion` is the
current one and `huntGeneratorVersions` lists every version this build can
still materialize. Each operator carries the version it first appeared in, and
an older generator is simply the registry truncated there — which is why new
operators are appended and never interleaved. Case seeds are not versioned:
`v3` and `v4` draw the same numbers and differ exactly where the operator list
does. `TestHuntGeneratorVersion3Reproduction` pins the older generator against
a fixed manifest.

### Route tables, and telling absences apart

Three families are about a route that is not there, and none of them can be
established from reachability. `RouteEvidence` answers "where does this
destination go"; `RouteTableEvidence` is the different reading of "what routes
exist at all", taken with `ip route show` from inside the node at the end of
the run and recorded for every family the node has an address in. An empty
`routes` list is therefore the positive statement that the table was read and
held nothing, which is what `routing.no_default_route` needs — and a record
being absent means nobody looked, which never establishes anything.

`routing.wrong_default_route` needs one thing more, because a default that goes
nowhere and a network that is broken past the gateway look identical from the
client: the control endpoint behind the original next hop, reached over its own
specific route, has to still answer. That is what says the old gateway still
forwards and only the choice of default changed.

Two more families are about a port rather than a route, and they are each
other's negative. `ControlledTargetEvidence` now carries the outcome of the
simulator's own dial rather than only whether it worked, because a reset and a
timeout are different faults with different fixes and the dialing end is the
only place the difference is visible. `service.connection_refused` requires the
dial to have been refused and no drop counter to have matched; `service.tcp_port_blocked` requires the opposite of both. Neither can be established by
a dial that merely failed, and a run cannot satisfy both.

## Challenge Mode

`netdoc-sim challenge` is the hunt with the contestants swapped: instead of the
oracle grading netdoc alone, a person diagnoses the network first and both
answers are graded against the same independently observed truth.

```sh
./netdoc-sim challenge                       # draw one and play it
./netdoc-sim challenge -difficulty hard
./netdoc-sim challenge -id V3-8F42C1          # replay someone else's
./netdoc-sim challenge -id V3-8F42C1 -answer dns_failure -json
```

One command runs the whole session: it builds the network, opens a shell in the
client node, takes a structured answer, runs the real netdoc in the same live
network, and prints the reveal. It is one foreground process because a
simulated network only exists while the process holding its namespaces does —
`start`/`shell`/`diagnose` subcommands would need a daemon and a state file
pointing at it, and would make it possible for the player and netdoc to be
handed different networks.

### Challenge ids

A challenge id is `V3-8F42C1`: a generator version and six hex digits. It
resolves with no state on disk and no network, so the same id is the same
puzzle on anyone's machine. A bare `8F42C1` is accepted and always means `V1`,
which was the only form the first release printed.

The version is part of the id because it has to be. What an id means depends on
this file's selection rules, on the hunt generator behind them, and on the base
scenario YAML they draw from — so a change to any of the three would repoint
every id already shared. Such a change adds an entry to `challengeGenerators`
instead, leaving old ids resolving through the rules they were minted under.
`TestChallengeIDsResolveToTheSameCaseForever` pins ids of both versions and
fails if the chain moves under them.

`V2` exists because the contract below admitted `netem.loss`, a condition V1 had
excluded. `V3` exists because six more conditions were added to the hunt
registry — a refused port, a filtered one, a certificate name mismatch, and the
three single-default route faults — along with the `two-router-healthy` control
two of them need. Each earlier version keeps its own frozen condition list and
its own hunt generator version, so an id someone shared before a change still
sets the puzzle they played.

### The challenge contract

Challenge Mode asks one question: can the simulator produce a real, verified
network fault that Network Doctor fails to diagnose? That only means anything if
the set of possible challenges is decided without reference to what Network
Doctor can already do. The pipeline is one-directional and never loops back:

```text
simulator injects a fault
        ↓
independent evidence proves it reached live traffic
        ↓
observed truth names the real condition
        ↓
Network Doctor produces its diagnosis
        ↓
the judge compares the diagnosis with the truth
```

A hunt mutation becomes challengeable when it passes four tests, listed in
full next to `challengeConditions` in `internal/simulation/challenge.go`:

1. **independently observable** — the executed run left evidence, read off the
   wire or off the kernel, that the fault met live traffic. `mutationObserved`
   is that check; a condition may narrow it further with its own `requires`, and
   may never widen it.
2. **deterministic and replayable** — the same id sets the same puzzle, and the
   condition holds for the whole run or not at all.
3. **the same network for both contestants** — a person in the shell and the
   netdoc process must be able to see the same thing.
4. **inside the diagnostic scope** — the condition is a fault of the network
   itself rather than of an application the network delivered correctly.

"Network Doctor already recognizes it" is deliberately not on that list, and
nothing in the generation path can reach `challengeRecognition` or a diagnosis.
A condition netdoc has no vocabulary for is still eligible; it is a loss for
netdoc, not a challenge that could never be set. `netem.loss` is the current
example: the simulator can prove the shaper discarded packets, while netdoc's
cause vocabulary carries no impairment verdict at all.

Excluded mutations, and which of the four tests each one fails: every timed
family and `netem.latency`/`netem.jitter` (1 or 2 — a scheduled fault would be
over while the person investigated, and delay leaves no counter of its own);
`http.status_503` and `encrypted_dns.doh_invalid` (4 — the network carried the
request and carried the answer back, and the `http-error` control pins down that
netdoc reports that as working on purpose); `proxy.connect_refused` and
`quic.udp_443_block` (3 — only the netdoc process is handed the proxy, and a
filtered UDP port is indistinguishable from a silent one in the shell).

### What is shared with the hunt, and what is not

Challenge Mode adds no fault model. A challenge id resolves, deterministically
and with no state on disk, to a hunt base scenario and a hunt case number, and
the case is materialized by `GenerateHuntCase` with a maximum of one mutation.
Truth comes from `collectObservedTruth`, so a mutation counts only when the
executed run left independent evidence for it — the same `observed_faults` rule
the hunt uses. Recognition of a condition the hunt oracle already grades reuses
that oracle's `recognized` half rather than restating it, and the shared wire
predicates in `hunt_oracle.go` are the one place either side reads evidence.

What is challenge-specific is the answer vocabulary, the eligibility contract
above, the difficulty metadata, and the matchup.
`internal/simulation/challenge.go` is authoritative for all four, and keeps them
in two tables that never read each other: `challengeConditions` is what the
simulator can prove, `challengeRecognition` is what netdoc's report has to say.
A version's whole meaning — controls, hunt generator, admitted conditions —
lives in one `challengeSelection`, so nothing a version resolves through can
drift out from under an id that was already shared.
Protocol meaning stays where it belongs — TCP reset is recognized by netdoc's
own `connection_reset` cause and nothing looser, because a generic "the run
failed somehow" comparison would score netdoc correct for naming a different
fault with a different fix.

### Scoring

Each contestant is graded separately against the observed truth, and the
matchup is derived from the two scores. There is no partial credit and no
scoring on prose: the human picks from a fixed menu, and netdoc is read through
its own cause vocabulary, structured per-family verdicts and verdict class.

Network Doctor's score is one of three:

| score | meaning |
| --- | --- |
| `correct` | its own report states the condition the simulator observed. Netdoc wins the round unless the human also named it, which is a draw. |
| `incorrect` | it has a verdict for this condition and produced a different one. |
| `unrecognized` | its vocabulary has no verdict that could state this condition, so no report it could have written would have won. |

`incorrect` and `unrecognized` both lose the round; they are separate because
the difference is the whole point of the contract. A challenger who names the
condition beats netdoc in either case — **"Network Doctor did not recognize this
fault" is a challenger victory, not a reason the challenge could not be set.**

A challenge is scoreable only when the run completed and cleaned up, netdoc
produced a diagnosis for the primary test, and the answer is established
independently. For an injected fault that means the mutation is in
`observed_faults`, and that the condition's own `requires` is satisfied where it
demands more — `netem.loss` wants the qdisc's kernel drop counter, because a
shaper installed with exactly the requested parameters still impaired nobody if
it matched no traffic. For a challenge that injected nothing it means the
simulator positively measured health along every dimension a challenge is able
to break: a reachable family at the client node and no unreachable one, every
controlled target reached, every selected gateway answering, no failing DNS
answers, no reset connections, no expired certificate a client refused, no
downed link, and no shaper counting drops. An empty mutation list is not
evidence of anything, the mutation manifest is not evidence of anything, and
neither is netdoc's verdict — the healthy oracle reads none of them. Anything
else is `no_result`, for both contestants at once. A mutation that failed to
take effect therefore cannot beat Network Doctor: with no independent evidence
there is no truth to grade against, and the round is void rather than won.

The fault also has to sit where the player was pointed. A base with more than
one client test can have the generator place a service fault on a target the
briefing never names, which would be a clue nobody was shown and a question the
graded netdoc run was never asked; those cases are not challenge-capable.

Elapsed human time is recorded because it is fun to compare, and is deliberately
not part of the matchup: netdoc is automated and a person is not. In `-json` it
is the only thing under `timing`, which is also the only part of a result a
replay of the same id will not reproduce.

### Which Network Doctor a result was scored against

The id makes the puzzle reproducible. The `netdoc` object makes the other half
reproducible — which build answered it:

```json
"netdoc": {
  "path": "/home/you/network-doctor/netdoc",
  "version": "netdoc v1.11.2"
}
```

`path` is the absolute path the run launched, selected by the rules in [Which
netdoc gets run](#which-netdoc-gets-run) and resolved once, before the network
exists. `version` is the line that same executable printed for `netdoc
-version`, recorded verbatim: a local build says `netdoc dev`, and that is the
truthful identity of what ran rather than something inferred from the checkout.
The checkout and the binary are routinely different builds, which is the whole
reason the binary is asked instead.

A binary that cannot be executed, or that answers `-version` with nothing, ends
the challenge before it starts: a result nobody can attribute to a build is not
worth playing for. The same two values appear in the reveal under `Network
Doctor under test`, and deliberately not in the share block, which stays a
spoiler-free postable summary.

### Not spoiling it

The briefing prints the id and the difficulty. The base scenario, seed, case
number and mutation are the answer, so they appear only in the reveal, and a
test asserts the briefing contains none of them. The share block carries two
check marks and the id but never names the fault, so posting a result does not
spoil the challenge for whoever reads it.

`-v`, a JSON report, or reading the source obviously defeats all of this. It is
a game, not a security boundary.

### Requirements and cleanup

Challenge Mode needs exactly what any other run needs — the Linux namespace
backend, no root — plus a terminal, because a person is being asked a question.
The shell enters the node through the same `nsenter` argument slice the netdoc
run uses, gains no privilege the simulator did not already have, and is given
the simulator's trust anchors so a generated certificate verifies for the
player exactly as it does for netdoc.

Nothing survives the command. A challenge never keeps its simulation: the
namespaces go when the director exits, the workspace is removed on every exit
path including an abandoned session, and no state record is written at all.
Editing the network from inside the challenge shell is possible and is its own
punishment — truth is collected after netdoc runs, so a repaired fault stops
being observed and the challenge scores `no_result`.

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

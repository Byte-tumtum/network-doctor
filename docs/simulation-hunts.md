# Hunts and triage reference

Authoritative reference for reproducible fault campaigns, generated bug
hunts, and nightly triage automation. See
[docs/simulation.md](simulation.md) for setup and the guide index, and the
wiki's [Hunts and Triage](https://github.com/heymaikol/network-doctor/wiki/Hunts-and-Triage)
guide for an orientation to what these are for.

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
the hunt seed, base scenario, case number, fault ceiling, and generator version,
so an exact reproduction names all five without first running cases 0 through
N-1. The accepted bases and mutation registry live in
`internal/simulation/hunt_generate.go`.

```sh
./netdoc-sim hunt healthy --seed 20260101 --cases 20
./netdoc-sim hunt healthy --seed 20260101 --case 4 --max-faults 2 --generator-version v3 --json
./netdoc-sim hunt healthy --seed 20260101 --case 4 --max-faults 2 --generator-version v3 --dry-run --json
```

`--cases N` means the first N unique mutation fingerprints in the global
candidate sequence. Candidate numbers can have gaps when a semantic duplicate
is skipped. `--shard i/N` is a zero-based filter over those already accepted
global cases: case C belongs to shard i when `C % N == i`. Every shard walks
the same cheap deterministic candidate sequence and only creates namespaces
for its own cases. It does not draw a new stream or renumber cases.

`--case` and `--shard` are mutually exclusive. Exact reproduction remains the
unsharded command printed in findings, including `--case`, `--seed`,
`--max-faults`, and `--generator-version`, so a case number and fingerprint mean
the same thing before and after sharding. `--fail-fast` stops only the current
shard; independent processes do not cancel or coordinate one another.

Run and merge all four shards locally like this:

```sh
for shard in 0 1 2 3; do
  ./netdoc-sim hunt healthy --seed 20260101 --cases 60 \
    --shard "$shard/4" --max-faults 2 --generator-version v5 --json > "shard-$shard.json"
done
./netdoc-sim hunt merge shard-3.json shard-1.json shard-0.json shard-2.json > hunt.json
```

`hunt merge` accepts shard files in any order and writes the ordinary canonical
hunt JSON shape in global case order. It rejects duplicate or missing shards,
duplicate or missing global cases, and incompatible base, seed, generator,
case-count, fault-ceiling, shard-count, dry-run, or fail-fast settings. It
regenerates the expected manifests, so file names are never treated as
metadata. An empty shard is valid when no accepted global case belongs to it.

Shard reports add top-level `max_faults`, optional `fail_fast` and `dry_run`,
and `shard` with `index` and `count`. `requested_cases` remains the logical
global unique-case total while generated and executed counters describe that
shard. A merged report omits `shard` because it represents the logical hunt,
not one execution partition. Existing JSON fields are unchanged.

`--max-faults` belongs in a reproduction command rather than being left to the
flag default. It is one of the inputs the case is drawn from: the first number
taken from the case seed is how many mutations to apply, bounded by the ceiling,
so the same base, seed and case under a different ceiling is a different
network. The manifest records it, every finding's reproduction carries it, and
the command a filed issue prints names it.

`--generator-version` selects one retained generator from the registry. It
defaults to `HuntGeneratorVersion` for a new hunt, but every printed single-case
reproduction names the resolved version. A saved artifact is replayed with its
recorded version and never with the replaying binary's default.

Every case report includes generator version, root and case seeds, the fault
ceiling, materialized mutations, and a case fingerprint. Findings use semantic
diagnosis fingerprints, excluding prose and incidental timing, paths, process
ids, and kernel names. Keep the seed, case, fault ceiling, generator version,
and reproduction command with any failure report.

### A hunt makes no reproducibility claim

Each case runs netdoc exactly once, and the hunt therefore never reports a
diagnosis as unstable. Neither comparison available to it is between two runs
of the same experiment:

- Two runs inside one live topology are not independent. The second inherits
  the neighbour, route and resolver caches the first warmed, so a verdict that
  changed between them says the first probe paid for a cold path, not that
  netdoc drifted. `two-path-ipv6-healthy` documents exactly this: its first
  test exists to warm both forwarding paths, and a repeated run of it reports
  `ok` where the cold one reported `degraded`.
- Two different cases are two different networks. The observed-truth vocabulary
  deliberately records that a path was impaired without recording by how much,
  so a 79 ms path and a 730 ms one share a truth fingerprint while netdoc
  correctly describes them differently. An accusation built on that equivalence
  is a false positive, and because it needs sibling cases to exist at all, no
  single-case replay could ever reproduce it.

Determinism is campaign mode's question. `--iteration N --runs K` repeats one
fixed schedule through a whole fresh topology per run, which is the comparison
this one cannot make, and it already reports divergence as `nondeterministic`.

### Route findings need routing evidence

`alternate_route_available` and `wrong_default_route_evidence` are the two
findings about a path the diagnosis did not describe, and both are gated twice.
A warning on the egress row is not a routing fact, so the client's own dial has
to have found an address family with nothing answering; latency, a cold first
packet, or one address of several failing all raise that row while the path is
fine, and on a two-test scenario the control keeps passing regardless. And a
diagnosis that already named a route cause has told the user which route failed,
so what is left is a wish about how much route detail to print, not a gap in
what was communicated.

A generated mutation records intent; it is not automatically observed truth.
`observed_faults` contains a mutation only when service, event, kernel-fault, or
independent reachability evidence from the executed simulation supports it.
Persistent netem mutations require matching kernel qdisc state on the intended
logical node and segment. A path-MTU black hole requires both halves of the
size asymmetry read back off the kernel: the forwarding hop really carrying the
narrowed MTU, and the client still carrying more. A path that narrowed
everywhere carries small packets by agreement, and a hop that was never
narrowed carries everything, so neither half alone is the fault. Timed DNS,
netem, and link mutations require the specific impairment event to have applied
successfully; initialization, restoration, failed, and skipped events do not
qualify. These timed entries
prove successful simulator state changes, not that netdoc sampled the affected
window or observed an end-to-end consequence.

### What a hunt false negative means

A hunt false negative means the simulator independently established a network
condition whose diagnostic meaning Network Doctor failed to recognize. It does
not mean a mutation expected probe X to fail and probe X did not fail.

The oracle in `internal/simulation/hunt_oracle.go` is that contract in code. It
runs on a vocabulary of `NetworkCondition` values, domain facts such as IPv4
internet reachability lost, a target serving an expired TLS certificate, a proxy
refusing its CONNECT destination, QUIC datagrams dropped on UDP/443, a client
left with no default route for a family it can no longer reach, and it keeps
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
proxy, a deleted default route reported only as an unreachable internet, and any
unrelated failing row are all misses, because each sends the user somewhere
else. A cause on a passing row is context, not recognition.

Reconciliation runs on the final client diagnosis and only on stable paths.
Unknown or unavailable families, persistent netem, and actual timed path
impairments are not treated as a final-state diagnosis oracle. The opposite
direction, where the simulator reached a family the diagnosis calls unreachable, is
reported as `family_reachability_mismatch` with category
`diagnostic_contradiction` rather than as a false negative.

Two observed faults deliberately imply no condition, because netdoc reports no
failure for either by design and the `http-error` control pins that down: an
HTTP error status is a working service answering, and an invalid DoH response
while DoT still resolves is encrypted DNS working. Adding an expectation there
would invent a contract the probes never made.

`pmtu.blackhole` narrows the forwarding hop the client's own route to the
briefed endpoint leads to, and only that hop: the path-MTU probe writes to that
endpoint, so a narrowed interface anywhere else leaves the write untouched. It
needs a router with exactly one interface off the client's link, so which way
that router forwards is read rather than guessed, and that interface must carry
no IPv6, because `minIPv6MTU` is the floor IPv6 requires of a link and a hop
that cannot be narrowed below it black-holes no IPv6 sender.

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
an older generator is simply the registry truncated there, which is why new
operators are appended and never interleaved. Case seeds are not versioned:
every version draws the same numbers and they differ exactly where the operator
list does. `TestHuntGeneratorVersion3Reproduction` pins the older generator
against a fixed manifest. New hunts default to `HuntGeneratorVersion`; use
`--generator-version` to select any retained version explicitly.

### Route tables, and telling absences apart

Three families are about a route that is not there, and none of them can be
established from reachability. `RouteEvidence` answers "where does this
destination go"; `RouteTableEvidence` is the different reading of "what routes
exist at all", taken with `ip route show` from inside the node at the end of
the run and recorded for every family the node has an address in. An empty
`routes` list is therefore the positive statement that the table was read and
held nothing, which is what `routing.no_default_route` needs, and a record
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

## Triage and nightly automation

`netdoc-sim triage` hunts the fixed baselines, re-runs each candidate's exact
case, and requires both its case fingerprint and finding fingerprint to match.
An unreproduced candidate is reported but never filed. The baseline list and
fixed regression seeds are authoritative in `internal/simulation/triage.go`.

The baseline set is chosen so that every operator in the registry is applicable
to at least one of them, which `TestEveryHuntOperatorReachesABaseline` holds it
to. A single-path base cannot host a route-choice fault, so the multi-path bases
are what let the routing families run at all; without them those operators exist
but the nightly hunt can never generate one, and the family is unfalsifiable
however good its oracle is. Their second test is also the control that the route
coverage findings are built from.

```sh
./netdoc-sim triage                               # observe; file nothing
./netdoc-sim triage --scenarios healthy --cases 5
./netdoc-sim triage --json
./netdoc-sim triage --create                      # create issues through gh
./netdoc-sim triage --hunt-results merged-hunts   # reuse merged full hunts
```

`--hunt-results` requires one canonical merged report for every selected
baseline and validates its content rather than its file name. It replaces only
the initial full hunts. Candidate findings are still re-run as exact cases in
fresh namespaces before they can be filed, using the generator version recorded
in each candidate's reproduction metadata. Artifacts with a missing or unknown
generator version are rejected instead of being assigned the current default.

`--create` is the only mode that writes to GitHub. It uses the configured `gh`
client and suppresses duplicates by a stable identity derived from the
reproduction coordinates, exact case fingerprint, and finding fingerprint. It
treats a failed hunt, reproduction, parse, or `gh` call as an error rather than
as a clean result.

`.github/workflows/hunt.yml` is authoritative for the nightly schedule, runner,
permissions, case count, and issue-creation opt-in. Scheduled issue creation
requires the `NETDOC_HUNT_CREATE` repository variable; manual dispatch requires
its `create` input. Observation-only runs withhold `GH_TOKEN`, even though the
job declares the permission needed by an opted-in run. Keep the workflow's
explicit Bash/`pipefail` behavior and seeded-netem-compatible runner when
changing it. One job resolves the exploration seed as the UTC date in
`YYYYMMDD` form, then every baseline and all four shards receive that exact
numeric value. The nightly runs 60 logical unique cases per baseline, merges
the shard JSON, and gives the canonical reports and the same seed to triage.
Each report and case manifest records the resolved seed, so a later replay uses
the artifact's number rather than deriving a seed from the replay date.

The fixed-seed regression lane remains separate in `.github/workflows/ci.yml`.
`TestGeneratedHuntPMTUBlackholeCaseReachesThePathMTUProbe` runs one routed case
at seed `20260102`, case `39`, and checks that its known generated path-MTU fault
still reaches the intended probe. It is small and stable while the larger
nightly workflow explores a new date seed.

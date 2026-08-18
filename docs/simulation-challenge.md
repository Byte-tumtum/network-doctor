# Challenge Mode reference

Authoritative reference for `netdoc-sim challenge`: the answer vocabulary,
the eligibility contract, scoring, and how to add a new playable diagnosis.
See [docs/simulation.md](simulation.md) for setup and the guide index, and
the wiki's [Challenge Mode](https://github.com/heymaikol/network-doctor/wiki/Challenge-Mode)
guide for a walkthrough, the daily challenge, and starter packs.

## Challenge Mode

`netdoc-sim challenge` is the hunt with the contestants swapped: instead of the
oracle grading netdoc alone, a person diagnoses the network first and both
answers are graded against the same independently observed truth.

```sh
./netdoc-sim challenge                       # draw one and play it
./netdoc-sim challenge -daily                # today's, the same one for everybody
./netdoc-sim challenge -starter fundamentals # a curated one to learn on
./netdoc-sim challenge -authored no-default-route  # a hand-written case
./netdoc-sim challenge -id V4-8F42C1         # replay someone else's
./netdoc-sim challenge -difficulty hard
./netdoc-sim challenge -id V4-8F42C1 -answer connection_refused -json
```

One command runs the whole session: it builds the network, opens a shell in the
client node, takes a structured answer, runs the real netdoc in the same live
network, and prints the reveal. It is one foreground process because a
simulated network only exists while the process holding its namespaces does, and
`start`/`shell`/`diagnose` subcommands would need a daemon and a state file
pointing at it, and would make it possible for the player and netdoc to be
handed different networks.

### Playing one

The briefing names the machine you are standing on and the host you were asked
about, which is the challenge's own, e.g. `invoices-8f42c1.test`, derived from the id
and nothing else, so replaying an id presents the same host and the name says
nothing about which fault was set. It is a real part of the simulated network:
the node's resolver answers it, whatever serves it presents a certificate for
it, and it is the target the graded netdoc run is handed. `.test` is reserved
by [RFC 6761](https://www.rfc-editor.org/rfc/rfc6761), so nothing about it can
reach public DNS or the internet. In the shell it is also in the environment,
as `$NETDOC_CHALLENGE_TARGET` and `$NETDOC_CHALLENGE_HOST`. The target carries
no `challenge` label: it is passed to netdoc as an ordinary argument, and the
simulator hands that process no token saying a challenge is happening.

Type `exit` in the shell to reach the menu: `b` reprints the briefing, `s`
returns to the shell, `q` gives up.

Answers are picked by number or typed by name from the menu, so nothing has to
be memorized: `netdoc-sim challenge -answer "TCP port blocked"`,
`-answer tcp_port_blocked`, and `-answer blocked` are one answer, since matching
folds case, spaces, hyphens, and underscores together, with no fuzzy matching.
A machine-readable result keeps the stable identifier and the display name side
by side, so a script keys on `answer` and a human reads `label`.

`netdoc-sim challenge` with no arguments is the whole of what a first-time
player needs; the answer vocabulary is printed by the menu and the packs by
`netdoc-sim starters`.

### Elapsed human time

The result reports how long you took, on a monotonic clock: from the briefing
being printed to your answer being accepted. Building the namespaces, injecting
the fault, netdoc's own run, and teardown are outside that window; rereading
the briefing and retyping a mistaken answer are inside it. It lives in `timing`
under `-json`, plays no part in the matchup, and a submission handed in with
`-answer` records zero, the honest answer for a session no human played.

### The daily challenge

`netdoc-sim challenge -daily` plays today's, and everybody who plays today gets
the same one:

```sh
./netdoc-sim challenge -daily                # today, UTC
./netdoc-sim challenge -daily=2026-03-04     # a particular day
```

The date is the **UTC** calendar date, not the local one, so two people whose
clocks disagree about what day it is still get the same challenge. The mapping
is a pure function of that date, with no server, no account, no network and no
filesystem, so it is derivable offline on any machine. It resolves to an
ordinary challenge id, which is the artifact: it replays forever with no
`-daily` and no dependence on today still being that day. A result played as
the daily carries `daily` in its JSON and a `Daily <date>` line in the share
block, which is what makes two people's results comparable as the same day's
puzzle.

Which id version a date maps through is frozen per date, in `dailyEpochs`
alongside the id versions themselves, so a future generator version cannot
redefine a day somebody has already played and posted.

`-daily` refuses to be combined with anything that would pick a different
challenge (an explicit id, `-difficulty`, `-starter`) by name rather than by
inventing a precedence.

Because a daily is the result people post, playing one also puts its
[share block](#the-share-block) on the clipboard once the matchup is decided,
using the terminal's own OSC 52 clipboard request. That is a best-effort output
side effect and nothing more: it happens only after the result exists, only for
`-daily`, and a terminal that does not honour the request (or a run that was
never attached to one) changes neither the result, the exit status, nor what is
printed. The block is printed either way, so copying it by hand always works.

### Starter packs

```sh
./netdoc-sim starters                        # the packs, and what each teaches
./netdoc-sim starters routing                # its challenge ids, in order
./netdoc-sim challenge -starter routing      # draw one from it
```

A pack is a curated list of ordinary challenge ids and nothing else: every
entry resolves through the same `BuildChallenge`, plays on the same namespaces,
is graded by the same oracle, and is replayable by its own id. `starters <pack>`
prints a pack's ids in order; `-starter` draws one at random, since the command
keeps no record of what you have played.

Every pack holds at least two possible answers, and one entry per pack is a
network with nothing wrong with it, because a single-answer pack would be an answer
key rather than a hint. That is why there is no DNS pack: the answer vocabulary
has exactly one DNS entry, so "DNS" would be the whole answer. DNS is practised
inside `fundamentals` instead, as one of four possibilities.

The ids are written down in `starterPacks` rather than searched for at run time,
so a generation change cannot quietly re-point a pack at a different lesson.
`TestStarterPacksStayPlayable` checks that every entry still sets the condition
it was curated for and that no pack has collapsed to one answer.

### Challenge ids

A challenge id is `V4-8F42C1`: a generator version and six hex digits. It
resolves with no state on disk and no network, so the same id is the same
puzzle on anyone's machine. A bare `8F42C1` is accepted and always means `V1`,
which was the only form the first release printed.

The version is part of the id because what an id means depends on this file's
selection rules, the hunt generator behind them, and the base scenario YAML
they draw from, so a change to any of the three adds an entry to
`challengeGenerators` instead of repointing ids already shared.
`TestChallengeIDsResolveToTheSameCaseForever` pins ids of both versions and
fails if the chain moves under them.

`V2` admitted `netem.loss`, a condition V1 had excluded. `V3` added six more
conditions to the hunt registry (a refused port, a filtered one, a certificate
name mismatch, and the three single-default route faults) plus the
`two-router-healthy` control two of them need. `V4` changes *how* those
versions choose, not what they may choose (below). Each earlier version keeps
its own frozen condition list and generator version, so an id shared before a
change still sets the puzzle it did then.

`A1` is the authored namespace: an id version like any other, whose digits name
a case somebody wrote rather than a seed to search from.

#### V1 to V3, and why V4 chooses differently

V1 to V3 draw a base scenario and case number, let the hunt generator produce
whatever mutation it produces, and retry until one is challenge-capable. That
quietly lets three implementation details decide what the game is about: how
many mutation variants a family has (`dns.servfail` and `dns.drop` are one
diagnosis to a player but two draws), how many base scenarios its operator
applies to, and which case the scan reaches first. Measured across 4000 V3 ids,
that put DNS at 23.4% and a missing subnet route at 1.6%, a sixteenfold spread
nobody chose, in a game whose value is practising the rare ones.

V4 picks the answer first, uniformly over the playable vocabulary, and only
then searches for a base and case number that express it, so rejection changes
how long an id takes to resolve, never which diagnosis it resolves to. The same
4000-id measurement puts every family between 5.9% and 7.1%, with healthy at
one draw in six as before. `TestV4DistributionIsUniformOverAnswers` guards it
with a deliberately wide band: the invariant worth protecting is "every family
is reachable and none dominates", not a particular percentage.

#### When a new id version is justified

A published id is a permanent promise: whoever types `V1-8F42C1` gets the puzzle
the person who shared it played, on any machine, forever. Every version in
`challengeGenerators` stays there for that reason, and
`TestChallengeIDsResolveToTheSameCaseForever` pins at least one id per version so
the promise fails the build rather than a player. That test now also refuses a
version carrying no rows, so a new one cannot ship unpinned.

A version is the cost of keeping that promise. It is not a place to put new
content, and most new content needs no version at all:

| Change | New id version? |
| --- | --- |
| A new playable diagnosis | No. The `challengeConditions` row reaches V4 generation at once, and earlier versions skip it through their frozen lists. |
| A new hand-picked case | No. Append to `authoredChallenges`. A1 digits derive from the slug, so the table is append-safe and adds no frozen code. |
| A new base scenario for hunts | No, as long as it is not added to `challengeBasesV3`. |
| Better answers, reveals, scoring prose, UX, validation | No. None of it is an input to id resolution. |
| Bumping `HuntGeneratorVersion` | No, and it does not reach Challenge Mode either. V3, V4 and A1 name the hunt version they shipped with as a literal, so a bump leaves every published id where it is. Add the bumped version to `huntGeneratorVersions` rather than replacing the entry there, so old manifests stay resolvable. |
| Putting a newer hunt generator in front of players | Yes. That is a deliberate choice, not a consequence of the bump, and it repoints nothing until the new version exists. |
| Changing how selection itself behaves | Yes. This is the only reason V4 exists. |

So the policy is: **V1 to V4 and A1 are permanent compatibility implementations,
and Challenge Mode does not receive another generator version unless a
user-facing capability cannot be expressed correctly through the current
contract.** Wanting the next number is not a reason. There is no V5 pending, and
its absence is the design working rather than a gap.

The reason this stays cheap is that a version is a selection, not an
implementation. V1, V2 and V3 are six lines each, parameterizing one shared
`buildChallengeCase`, and the hunt registry behind them is append-only: an older
generator is the same list truncated at its own version, so no operator is ever
copied. V4 is longer only because it changed the algorithm, which is exactly the
case that earns a version.

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

1. **independently observable**: the executed run left evidence, read off the
   wire or off the kernel, that the fault met live traffic. `mutationObserved`
   is that check; a condition may narrow it further with its own `requires`, and
   may never widen it.
2. **deterministic and replayable**: the same id sets the same puzzle, and the
   condition holds for the whole run or not at all.
3. **the same network for both contestants**: a person in the shell and the
   netdoc process must be able to see the same thing.
4. **inside the diagnostic scope**: the condition is a fault of the network
   itself rather than of an application the network delivered correctly.

"Network Doctor already recognizes it" is deliberately not on that list, and
nothing in the generation path can reach `challengeRecognition` or a diagnosis.
A condition netdoc has no vocabulary for is still eligible; it is a loss for
netdoc, not a challenge that could never be set. `netem.loss` is the current
example: the simulator can prove the shaper discarded packets, while netdoc's
cause vocabulary carries no impairment verdict at all.

Excluded mutations, and which of the four tests each one fails: every timed
family and `netem.latency`/`netem.jitter` (1 or 2: a scheduled fault would be
over while the person investigated, and delay leaves no counter of its own);
`http.status_503` and `encrypted_dns.doh_invalid` (4: the network carried the
request and carried the answer back, and the `http-error` control pins down that
netdoc reports that as working on purpose); `proxy.connect_refused` and
`quic.udp_443_block` (3: only the netdoc process is handed the proxy, and a
filtered UDP port is indistinguishable from a silent one in the shell).

### What is shared with the hunt, and what is not

Challenge Mode adds no fault model. A challenge id resolves, deterministically
and with no state on disk, to a hunt base scenario and a hunt case number, and
the case is materialized by `GenerateHuntCase` with a maximum of one mutation.
Truth comes from `collectObservedTruth`, so a mutation counts only when the
executed run left independent evidence for it, the same `observed_faults` rule
the hunt uses. Recognition of a condition the hunt oracle already grades reuses
that oracle's `recognized` half rather than restating it, and the shared wire
predicates in `hunt_oracle.go` are the one place either side reads evidence.

What is challenge-specific is the answer vocabulary, the eligibility contract
above, the difficulty metadata, and the matchup.
`internal/simulation/challenge.go` is authoritative for all four, and keeps them
in two tables that never read each other: `challengeConditions` is what the
simulator can prove, `challengeRecognition` is what netdoc's report has to say.
The evidence predicates each condition is proved by live next to the first, in
`challenge_truth.go`; the healthy oracle and the playable answer set are both
derived from them rather than listed separately.
A version's whole meaning, from controls to hunt generator to admitted
conditions, lives in one `challengeSelection`, so nothing a version resolves through can
drift out from under an id that was already shared.
Protocol meaning stays where it belongs: TCP reset is recognized by netdoc's
own `connection_reset` cause and nothing looser, because a generic "the run
failed somehow" comparison would score netdoc correct for naming a different
fault with a different fix.

### What defines ground truth

Nothing Network Doctor produces. Truth is established from the scenario state,
the applied mutation, and observations the simulator collected for itself from
inside the node namespaces: service records, kernel counters, routing tables
read back with `ip route show`, and the simulator's own TCP dials. The whole
truth path is `challengeTruth`, which reads `report.Evidence` and the mutation
manifest and never touches `report.Tests`, so a diagnosis cannot reach it even
by accident.

A mutation having been generated, applied, or expected to break something
establishes nothing on its own. It becomes truth only when the executed run left
independent evidence that it met live traffic.

### Evidence predicates

Every condition Challenge Mode can set carries two predicates over that
observation, and neither of them can see a diagnosis.

**The scoped predicate** answers "did *this* mutation, on this node and this
port, meet live traffic". It is `mutationObserved` plus the condition's own
optional `requires`, and it is what establishes the answer for a challenge that
injected a fault. It is deliberately narrow: it knows which node was mutated,
which is what lets a refused port be told apart from a filtered one on the same
host.

**The evidence signature** answers the different question "does this run show a
fault of this class *anywhere*, with no mutation to scope it by". It lives on
the condition row as `signature` and is defined in
`internal/simulation/challenge_truth.go`. It exists for the healthy challenge,
where there is no mutation to scope by and the only honest test is that no
condition's trace is present.

The split is what removed the last hand-maintained list. The healthy verdict
used to restate every condition's negation in prose, kept in step with the
condition table by a test that spelled the mapping out a third time; adding a
playable diagnosis meant editing all three and remembering to. Now
`healthyObserved` walks `challengeConditions` and consults each signature, so a
condition added without one fails in `TestEveryChallengeConditionCarriesASignature`
rather than quietly widening what counts as a healthy network.

A signature may be shared by several conditions, and several are. It names a
class of trace rather than fingerprinting one mutation, since telling neighbouring
families apart is the scoped predicate's job, and asking the unscoped one to do
it too would be asking it to name a fault from evidence that does not identify
one. `TestEvidenceSignaturesDiscriminate` is the table that holds each signature
to its neighbours: refused against timed-out, expired against name-mismatched, a
counted drop against an installed rule that matched nothing.

The playable set is derived from the same table. `challengePlayableAnswers`
reads the conditions rather than listing the answers again, so there is no
second taxonomy for generation to drift out of sync with.

### Authored and generated challenges

Both kinds are the same challenge. They differ only in how the case is chosen.

A **generated** challenge is a draw: an id seeds a search over base scenarios
and case numbers. To get one that sets a particular fault you scan ids until one
falls out, which is how the starter packs were built, and is a poor way to
write a lesson, because the case you end up teaching is whichever one the search
reached first.

An **authored** challenge names its base scenario and its fault directly.
`internal/simulation/challenge_authored.go` holds the table. Everything after
the choice is shared: the fault is produced by the ordinary hunt mutation
operator against the ordinary base scenario, so nothing hand-writes a node name,
a port or an address that could drift out of step with the topology; truth is
established by the same scoped predicate; and scoring is the same
`ScoreChallenge`. There is no second correctness path.

Authored cases are deterministic, since the fault's parameters come from a seed
derived from the slug, and they are validated by
`TestAuthoredChallengesAreValid`, which builds each one, proves the mutation
applies to the declared base, proves the declared diagnosis is the one the
condition table says that mutation establishes, and proves the fault lands on
the target the briefing names. `TestAuthoredChallengesScoreThroughTheSharedEngine`
additionally proves no *other* playable answer scores correct, so an authored
case cannot be accidentally ambiguous. A case with an inconsistent declaration
fails the build rather than reaching a player.

```sh
./netdoc-sim authored                                   # the cases and their ids
./netdoc-sim challenge -authored missing-subnet-route   # play one
./netdoc-sim challenge -id A1-48CFF9                    # the same case, by id
```

### Adding a new playable diagnosis

There is one workflow, and no hidden allowlists to remember.

1. **Confirm simulator truth.** The fault needs a hunt mutation operator in
   `huntMutationRegistry` that a control scenario can express, and the run has to
   leave independent evidence it met live traffic. If `mutationObserved` has no
   case for it, add one: that is the scoped predicate.
2. **Add the condition.** One row in `challengeConditions`: the mutation id, the
   answer, a difficulty, the reveal explanation, and the evidence `signature`.
   Add `requires` if the shared per-mutation check would settle for less than
   proof that live traffic met the fault, and `briefed` if the fault could land
   on a target the briefing never names. Check the four contract tests above
   before adding it at all.
3. **Prove discrimination.** Add the signature to the table in
   `TestEvidenceSignaturesDiscriminate` with its canonical positive evidence
   *and* the traces of its nearest neighbours, which must not fire it.
4. **Decide what netdoc may say.** If its vocabulary can state the condition, add
   an entry to `challengeRecognition`. If it cannot, add nothing: the condition
   is still playable and scores `unrecognized`, which is a challenger win and the
   finding worth having.

Nothing else needs updating. The answer menu entry is the one remaining piece of
prose, and `TestAdvertisedChallengeAnswersAreProducible` will tell you if you
forgot it. The healthy oracle, the playable set and V4 generation all derive
from the row you added. Generation reaches it from V4 onwards; earlier versions
keep their frozen lists, which is what the id version is for.

### Adding an authored challenge

Append a row to `authoredChallenges` with a stable slug, a name, the base
scenario, the mutation id, the diagnosis it teaches, and one sentence saying
what telling it apart requires. Then run the tests: they will build it, check
the declaration against the condition table, and reject it if the mutation does
not apply to that base or the fault lands off the briefed target.

The slug derives both the id and the generation seed, so reordering the table is
safe and renaming a slug mints a different challenge. Once a case is published,
pin its id in `TestAuthoredChallengeIDsAreFrozen`.

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

`incorrect` and `unrecognized` both lose the round, kept separate because the
difference is the whole point of the contract: **"Network Doctor did not
recognize this fault" is a challenger victory, not a reason the challenge could
not be set.**

A challenge is scoreable only when the run completed and cleaned up, netdoc
produced a diagnosis for the primary test, and the answer is established
independently. For an injected fault that means the mutation is in
`observed_faults` and the condition's own `requires` is satisfied where it
demands more. `netem.loss` wants the qdisc's kernel drop counter, since a
shaper installed with the requested parameters still impaired nobody if it
matched no traffic. For a challenge that injected nothing, `healthyObserved`
walks `challengeConditions` and asks each condition whether its own evidence
signature appears in this run, so "healthy" means every fault the game can set
was looked for with that fault's own predicate and not found, on top of a
floor of measurements that have to have been taken at all (a reachable family
at the client node, every selected gateway answering, no downed link). An empty
mutation list proves nothing on its own, and neither does netdoc's verdict; the
healthy oracle reads neither. Anything else is `no_result` for both
contestants: a mutation that failed to take effect cannot beat Network Doctor,
because there is no independent evidence to grade against.

The fault also has to sit where the player was pointed: a base with more than
one client test can place a service fault on a target the briefing never
names, which is not challenge-capable.

Elapsed human time (see [above](#elapsed-human-time)) plays no part in the
matchup. Three parts of a result describe the session rather than the
challenge and will not reproduce on replay: `timing`, `netdoc` (which build
answered it), and `daily` (the date, if played as one). Everything else is
determined by the id and the network.

### Which Network Doctor a result was scored against

The id makes the puzzle reproducible. The `netdoc` object makes the other half
reproducible, by recording which build answered it:

```json
"netdoc": {
  "path": "/home/you/network-doctor/netdoc",
  "version": "netdoc v1.11.2"
}
```

`path` is the absolute path the run launched, selected by the rules in
[Which netdoc gets run](simulation.md#which-netdoc-gets-run) and resolved once,
before the network exists. `version` is the line that same executable printed
for `netdoc -version`, recorded verbatim: a local build says `netdoc dev`, and
that is the truthful identity of what ran rather than something inferred from
the checkout. The checkout and the binary are routinely different builds,
which is the whole reason the binary is asked instead.

A binary that cannot be executed, or that answers `-version` with nothing, ends
the challenge before it starts: a result nobody can attribute to a build is not
worth playing for. The same two values appear in the reveal under `Network
Doctor under test`, and deliberately not in the share block, which stays a
spoiler-free postable summary.

### Not spoiling it

The briefing prints the id, the difficulty, the node and the host. The base
scenario, seed, case number and mutation are the answer, so they appear only in
the reveal. A test asserts the briefing contains none of them, and `b`
re-reading it goes through the same renderer, so there is no second, more
generous version to drift.

The host name is a function of the id alone (not the base scenario's own YAML
name, which used to leak the answer for two conditions), and a test checks that
no name fingerprints the base, case, or fault.

The [share block](#the-share-block) carries two marks, the id, and the date if
it was a daily, never the fault, and never a starter pack's name, since a pack
names a layer that would narrow the answer for the next player.

`-v`, a JSON report, or reading the source obviously defeats all of this. It is
a game, not a security boundary.

### The share block

The reveal ends with the one part of a result written for somebody who was not
there:

```
🩺 Network Doctor Challenge V4-8F42C1 (easy)
📅 Daily 2026-03-04
🧑 Me ✅   🤖 Network Doctor ❌
🏆 I beat Network Doctor in 3m 20s
🔁 Your turn: netdoc-sim challenge -id V4-8F42C1
```

It is rendered by `Share` in `internal/simulation/challenge_share.go`, which is
a separate renderer from the reveal on purpose: the reveal explains the fault to
the person who just played, and one function licensed to do both would
eventually do it in the wrong place. The block is plain UTF-8 with no ANSI, no
column alignment a proportional font would break, and no table, because it is
pasted into a chat client rather than printed to a terminal.

What it carries is the identity (the id, the difficulty, the date if it was a
daily, and the command that replays it) plus the two verdicts. Both losses
render the same mark: "not recognized" would tell a reader that the fault is one
netdoc has no words for, which narrows the answer for whoever plays the id next.
A submission with no session behind it (`-answer`) posts no time rather than
`0s`. The same completed result renders the same bytes every time, and
`TestChallengeShareBlockIsAPostableResult` freezes them, because a block people
compare cannot depend on where it was rendered.

There is exactly one share payload: what `-daily` copies to the clipboard is the
string this prints, not a variant of it.

### Requirements and cleanup

Challenge Mode needs exactly what any other run needs, the Linux namespace
backend and no root, plus a terminal, since a person is being asked a question.
On macOS or Windows the Linux part comes from a container runtime; see
[Running it in a container](simulation.md#running-it-in-a-container). The shell
enters the node through the same `nsenter` argument slice the netdoc run uses,
gains no privilege the simulator did not already have, and gets the simulator's
trust anchors so a generated certificate verifies for the player exactly as it
does for netdoc.

Nothing survives the command: namespaces go when the director exits, the
workspace is removed on every exit path including an abandoned session, and no
state record is written. Editing the network from inside the challenge shell is
its own punishment: truth is collected after netdoc runs, so a repaired fault
stops being observed and the challenge scores `no_result`.

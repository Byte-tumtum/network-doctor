package simulation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mathrand "math/rand"
	"slices"
	"sort"
	"strconv"
	"time"
)

const campaignSeedDomain = "netdoc-sim-campaign-v1"

// CampaignOptions controls a sequential campaign. Iteration, when non-nil,
// runs exactly that independently derived iteration even when it exceeds Runs.
type CampaignOptions struct {
	Run       Options
	Runs      int
	Seed      int64
	Iteration *int
	FailFast  bool
}

// FaultEvent is one fully resolved impairment. No random choice remains when
// execution starts; scheduled DNS events use Sequence rather than wall time.
type FaultEvent struct {
	Offset          time.Duration `json:"offset_ms"`
	Type            string        `json:"type"`
	Node            string        `json:"node,omitempty"`
	Segment         string        `json:"segment,omitempty"`
	Family          string        `json:"family,omitempty"`
	Latency         time.Duration `json:"latency_ms,omitempty"`
	Jitter          time.Duration `json:"jitter_ms,omitempty"`
	LossPercent     float64       `json:"loss_percent,omitempty"`
	NetemSeed       uint32        `json:"netem_seed,omitempty"`
	Service         string        `json:"service,omitempty"`
	QueryType       string        `json:"query_type,omitempty"`
	Sequence        int           `json:"sequence,omitempty"`
	ScheduledResult string        `json:"scheduled_result,omitempty"`
	// Delay and State carry the scheduled DNS and link states. Both are
	// omitempty, so a campaign that generates neither keeps the schedule
	// fingerprint it had before timed faults existed.
	Delay time.Duration `json:"delay_ms,omitempty"`
	State string        `json:"state,omitempty"`
}

type ProbeFingerprint struct {
	Test   string `json:"test"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Cause  string `json:"cause,omitempty"`
	IPv4   string `json:"ipv4,omitempty"`
	IPv6   string `json:"ipv6,omitempty"`
}

type DiagnosisFingerprint struct {
	ID       string             `json:"id"`
	Verdicts []string           `json:"verdicts"`
	Probes   []ProbeFingerprint `json:"probes"`
}

type IterationResult struct {
	Iteration     int                  `json:"iteration"`
	IterationSeed int64                `json:"iteration_seed"`
	Schedule      []FaultEvent         `json:"fault_schedule"`
	ScheduleID    string               `json:"schedule_id"`
	Fingerprint   DiagnosisFingerprint `json:"diagnosis_fingerprint"`
	Report        *Report              `json:"report"`
	Reproduce     Reproduction         `json:"reproduce"`
}

type Reproduction struct {
	Scenario  string `json:"scenario"`
	Seed      int64  `json:"seed"`
	Iteration int    `json:"iteration"`
}

type FingerprintCount struct {
	Fingerprint DiagnosisFingerprint `json:"fingerprint"`
	Count       int                  `json:"count"`
}

type CampaignResult struct {
	Scenario       string             `json:"scenario"`
	Seed           int64              `json:"seed"`
	Runs           int                `json:"runs"`
	Passed         int                `json:"passed"`
	Failed         int                `json:"failed"`
	Errors         int                `json:"errors"`
	FalsePositives int                `json:"false_positives"`
	FalseNegatives int                `json:"false_negatives"`
	Timeouts       int                `json:"timeouts"`
	StableRuns     int                `json:"stable_runs"`
	DivergentRuns  int                `json:"divergent_runs"`
	FailurePercent float64            `json:"failure_percent"`
	MinDuration    time.Duration      `json:"min_duration_ms"`
	MaxDuration    time.Duration      `json:"max_duration_ms"`
	MedianDuration time.Duration      `json:"median_duration_ms"`
	FirstFailure   *Reproduction      `json:"first_failure,omitempty"`
	Fingerprints   []FingerprintCount `json:"fingerprints"`
	Outcomes       []IterationResult  `json:"outcomes"`
	Cancelled      bool               `json:"cancelled"`
	Result         string             `json:"result"`
	Error          string             `json:"error,omitempty"`
	Suggestions    []Suggestion       `json:"suggestions"`
}

// RandomSeed chooses only the campaign's visible root seed. All behavior after
// this call is derived deterministically from it.
func RandomSeed() (int64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	// #nosec G115 -- all 64 random bits are intentionally preserved in the signed seed.
	return int64(binary.BigEndian.Uint64(raw[:])), nil
}

// DeriveIterationSeed is independent of PRNG history, so iteration 37 can be
// reproduced without constructing iterations 0 through 36.
func DeriveIterationSeed(seed int64, scenario string, iteration int) int64 {
	h := sha256.New()
	h.Write([]byte(campaignSeedDomain))
	h.Write([]byte{0})
	var raw [8]byte
	// #nosec G115 -- two's-complement seed bits are part of the reproduction contract.
	binary.BigEndian.PutUint64(raw[:], uint64(seed))
	h.Write(raw[:])
	h.Write([]byte{0})
	h.Write([]byte(scenario))
	h.Write([]byte{0})
	// #nosec G115 -- deterministic seed derivation hashes the stable two's-complement bits of every iteration, including negatives; no numeric value is narrowed.
	binary.BigEndian.PutUint64(raw[:], uint64(iteration))
	h.Write(raw[:])
	sum := h.Sum(nil)
	// #nosec G115 -- all 64 hash bits are intentionally preserved in the signed seed.
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

// RunCampaign executes iterations sequentially and delegates each one to Run,
// retaining its normal report and cleanup result.
func RunCampaign(ctx context.Context, scenario *Scenario, backend func() Backend, opts CampaignOptions) *CampaignResult {
	result := &CampaignResult{Scenario: scenario.Name, Seed: opts.Seed}
	if scenario.Campaign == nil {
		result.Result, result.Error = ResultError, "scenario has no campaign definition"
		result.finish()
		return result
	}
	runs := opts.Runs
	if runs == 0 {
		runs = scenario.Campaign.Runs
	}
	if runs < 1 || runs > 1000 {
		result.Result, result.Error = ResultError, "runs must be between 1 and 1000"
		result.finish()
		return result
	}
	iterations := make([]int, runs)
	for i := range iterations {
		iterations[i] = i
	}
	if opts.Iteration != nil {
		if *opts.Iteration < 0 || *opts.Iteration > 999999 {
			result.Result, result.Error = ResultError, "iteration must be between 0 and 999999"
			result.finish()
			return result
		}
		// One iteration, repeated as many times as Runs was explicitly asked
		// for. Repeating one schedule is the only way the divergence check has
		// anything to compare: every iteration otherwise draws parameters of its
		// own, so each schedule fingerprint is a group of one and two runs that
		// disagree on the same network look like two different networks.
		repeats := 1
		if opts.Runs > 0 {
			repeats = opts.Runs
		}
		iterations = make([]int, repeats)
		for i := range iterations {
			iterations[i] = *opts.Iteration
		}
	}

	for _, iteration := range iterations {
		if err := ctx.Err(); err != nil {
			result.Cancelled, result.Error = true, err.Error()
			break
		}
		iterationSeed := DeriveIterationSeed(opts.Seed, scenario.Name, iteration)
		compiled, schedule, err := compileCampaignIteration(scenario, iterationSeed)
		if err != nil {
			result.Error = err.Error()
			break
		}
		rep := Run(ctx, compiled, backend(), opts.Run)
		fingerprint := diagnosisFingerprint(rep)
		repro := Reproduction{Scenario: scenario.Name, Seed: opts.Seed, Iteration: iteration}
		outcome := IterationResult{Iteration: iteration, IterationSeed: iterationSeed, Schedule: schedule,
			ScheduleID: scheduleFingerprint(schedule), Fingerprint: fingerprint, Report: rep, Reproduce: repro}
		result.Outcomes = append(result.Outcomes, outcome)
		if rep.Result == ResultPass {
			result.Passed++
		} else {
			result.Failed++
			if result.FirstFailure == nil {
				copy := repro
				result.FirstFailure = &copy
			}
		}
		if rep.Result == ResultError {
			result.Errors++
		}
		for _, test := range rep.Tests {
			result.FalsePositives += test.FalsePositives
			result.FalseNegatives += test.FalseNegatives
			if len(test.TimedOut) > 0 {
				result.Timeouts++
			}
		}
		if opts.FailFast && rep.Result != ResultPass {
			break
		}
	}
	result.Runs = len(result.Outcomes)
	result.finish()
	return result
}

func compileCampaignIteration(base *Scenario, seed int64) (*Scenario, []FaultEvent, error) {
	scenario := cloneScenario(base)
	// The base was already validated. The compiled fields below are validated
	// again before execution.
	// #nosec G404 -- deterministic scenario generation must be reproducible from seed.
	rng := mathrand.New(mathrand.NewSource(seed))
	var schedule []FaultEvent
	if c := base.Campaign.Netem; c != nil {
		latency := sampleDuration(rng, c.Latency)
		jitter := sampleDuration(rng, c.Jitter)
		loss := sampleNumber(rng, c.LossPercent)
		tcSeed := rng.Uint32()
		if tcSeed == 0 {
			tcSeed = 1
		}
		fault := Fault{Type: FaultNetem, Node: c.Node, Segment: c.Segment,
			Delay: latency.String(), Jitter: jitter.String(), Loss: formatPercent(loss), Seed: tcSeed}
		replaced := false
		for i := range scenario.Faults {
			if scenario.Faults[i].Type == FaultNetem && scenario.Faults[i].Node == c.Node && scenario.Faults[i].Segment == c.Segment {
				scenario.Faults[i], replaced = fault, true
				break
			}
		}
		if !replaced {
			scenario.Faults = append(scenario.Faults, fault)
		}
		schedule = append(schedule, FaultEvent{Type: FaultNetem, Node: c.Node, Segment: c.Segment,
			Latency: latency, Jitter: jitter, LossPercent: loss, NetemSeed: tcSeed})
	}
	if c := base.Campaign.DNS; c != nil {
		failure := sampleNumber(rng, c.FailurePercent)
		makeOutcomes := func(queryType string) []string {
			out := make([]string, c.QueriesPerType)
			for i := range out {
				out[i] = DNSOutcomeAnswer
				if rng.Intn(10000) < int(failure*100) {
					out[i] = DNSOutcomeSERVFAIL
				}
				schedule = append(schedule, FaultEvent{Type: "dns_response", Service: c.Service,
					QueryType: queryType, Sequence: i + 1, ScheduledResult: out[i]})
			}
			return out
		}
		a, aaaa := makeOutcomes("A"), makeOutcomes("AAAA")
		found := false
		for ni := range scenario.Topology.Nodes {
			for si := range scenario.Topology.Nodes[ni].Services {
				svc := &scenario.Topology.Nodes[ni].Services[si]
				if svc.Name == c.Service && svc.Type == ServiceDNS {
					svc.DNSFault, found = &DNSFault{A: a, AAAA: aaaa}, true
				}
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("campaign DNS service %q disappeared", c.Service)
		}
	}
	if c := base.Campaign.Timeline; c != nil {
		scenario.Faults = append(scenario.Faults, compileFlappingTimeline(rng, c, &schedule)...)
	}
	if c := base.Campaign.DNSDelay; c != nil {
		// One dimension only: the delay a probe deadline is being walked across.
		delay := sampleDuration(rng, c.Delay)
		scenario.Faults = append(scenario.Faults, Fault{Type: FaultScheduledDNS, Service: c.Service,
			Events: []ScheduledEvent{{At: "0s", Outcome: DNSOutcomeDelay, Delay: delay.String()}}})
		schedule = append(schedule, FaultEvent{Type: FaultScheduledDNS, Service: c.Service,
			ScheduledResult: DNSOutcomeDelay, Delay: delay})
	}
	nodes := make(map[string]bool, len(scenario.Topology.Nodes))
	for i := range scenario.Topology.Nodes {
		nodes[scenario.Topology.Nodes[i].Name] = true
	}
	for i := range scenario.Faults {
		if err := scenario.Faults[i].validate(&scenario.Topology, nodes); err != nil {
			return nil, nil, fmt.Errorf("compiled campaign fault %d: %w", i, err)
		}
	}
	for _, node := range scenario.Topology.Nodes {
		for _, svc := range node.Services {
			if err := validateDNSFault(svc.DNSFault); err != nil {
				return nil, nil, fmt.Errorf("compiled campaign DNS service %q: %w", svc.Name, err)
			}
		}
	}
	return scenario, schedule, nil
}

// compileFlappingTimeline resolves the whole flapping shape up front. Three
// draws, in a fixed order, and every offset derived from them arithmetically —
// so iteration N is reproducible from its seed alone and nothing is decided
// once the scheduler starts.
func compileFlappingTimeline(rng *mathrand.Rand, c *CampaignTimeline, schedule *[]FaultEvent) []Fault {
	degradeAt := sampleDuration(rng, c.DegradeAt)
	degradeLoss := sampleNumber(rng, c.DegradeLoss)
	outageFor := sampleDuration(rng, c.OutageFor)
	tcSeed := rng.Uint32()
	if tcSeed == 0 {
		tcSeed = 1
	}
	latency, _ := time.ParseDuration(c.Latency)
	outageAt := degradeAt + campaignDegradedFor + campaignHealthyGap

	netem := Fault{Type: FaultScheduledNetem, Node: c.Node, Segment: c.Segment, Seed: tcSeed}
	for _, phase := range []struct {
		at   time.Duration
		loss float64
	}{
		{0, 0},
		{degradeAt, degradeLoss},
		{degradeAt + campaignDegradedFor, 0},
		{outageAt, 100},
		{outageAt + outageFor, 0},
	} {
		netem.Events = append(netem.Events, ScheduledEvent{At: phase.at.String(), Latency: c.Latency, LossPercent: phase.loss})
		*schedule = append(*schedule, FaultEvent{Offset: phase.at, Type: FaultScheduledNetem, Node: c.Node,
			Segment: c.Segment, Latency: latency, LossPercent: phase.loss, NetemSeed: tcSeed})
	}
	faults := []Fault{netem}
	if c.Service == "" {
		return faults
	}
	hold, _ := time.ParseDuration(c.ResolverHold)
	dns := Fault{Type: FaultScheduledDNS, Service: c.Service}
	for _, phase := range []struct {
		at      time.Duration
		outcome string
		delay   time.Duration
	}{
		{0, DNSOutcomeDelay, hold},
		{outageAt, DNSOutcomeDrop, 0},
		{outageAt + outageFor, DNSOutcomeAnswer, 0},
	} {
		event := ScheduledEvent{At: phase.at.String(), Outcome: phase.outcome}
		if phase.delay > 0 {
			event.Delay = phase.delay.String()
		}
		dns.Events = append(dns.Events, event)
		*schedule = append(*schedule, FaultEvent{Offset: phase.at, Type: FaultScheduledDNS,
			Service: c.Service, ScheduledResult: phase.outcome, Delay: phase.delay})
	}
	return append(faults, dns)
}

func cloneScenario(base *Scenario) *Scenario {
	out := *base
	out.Topology = base.Topology
	out.Topology.Segments = append([]Segment(nil), base.Topology.Segments...)
	out.Topology.Routes = append([]Route(nil), base.Topology.Routes...)
	out.Topology.Nodes = make([]Node, len(base.Topology.Nodes))
	for i, node := range base.Topology.Nodes {
		out.Topology.Nodes[i] = node
		out.Topology.Nodes[i].Interfaces = append([]Interface(nil), node.Interfaces...)
		out.Topology.Nodes[i].Aliases = append([]string(nil), node.Aliases...)
		out.Topology.Nodes[i].Services = make([]Service, len(node.Services))
		for j, svc := range node.Services {
			out.Topology.Nodes[i].Services[j] = svc
			out.Topology.Nodes[i].Services[j].Records = append([]DNSRecord(nil), svc.Records...)
			if svc.Certificate != nil {
				certificate := *svc.Certificate
				certificate.DNSNames = append([]string(nil), svc.Certificate.DNSNames...)
				out.Topology.Nodes[i].Services[j].Certificate = &certificate
			}
			if svc.Zone != nil {
				out.Topology.Nodes[i].Services[j].Zone = make(map[string]string, len(svc.Zone))
				for name, addr := range svc.Zone {
					out.Topology.Nodes[i].Services[j].Zone[name] = addr
				}
			}
			if svc.DNSFault != nil {
				out.Topology.Nodes[i].Services[j].DNSFault = &DNSFault{
					A: append([]string(nil), svc.DNSFault.A...), AAAA: append([]string(nil), svc.DNSFault.AAAA...)}
			}
		}
	}
	out.Faults = make([]Fault, len(base.Faults))
	for i := range base.Faults {
		out.Faults[i] = base.Faults[i]
		out.Faults[i].Events = append([]ScheduledEvent(nil), base.Faults[i].Events...)
	}
	out.Tests = make([]Test, len(base.Tests))
	for i := range base.Tests {
		out.Tests[i] = cloneTest(base.Tests[i])
	}
	out.Expect.Checks = append([]ExpectedCheck(nil), base.Expect.Checks...)
	if base.Campaign != nil {
		campaign := *base.Campaign
		if base.Campaign.Netem != nil {
			copy := *base.Campaign.Netem
			campaign.Netem = &copy
		}
		if base.Campaign.DNS != nil {
			copy := *base.Campaign.DNS
			campaign.DNS = &copy
		}
		if base.Campaign.Timeline != nil {
			copy := *base.Campaign.Timeline
			campaign.Timeline = &copy
		}
		if base.Campaign.DNSDelay != nil {
			copy := *base.Campaign.DNSDelay
			campaign.DNSDelay = &copy
		}
		out.Campaign = &campaign
	}
	return &out
}

func sampleDuration(rng *mathrand.Rand, r DurationRange) time.Duration {
	min, _ := time.ParseDuration(r.Min)
	max, _ := time.ParseDuration(r.Max)
	minMicros, maxMicros := min.Microseconds(), max.Microseconds()
	if minMicros == maxMicros {
		return time.Duration(minMicros) * time.Microsecond
	}
	return time.Duration(minMicros+rng.Int63n(maxMicros-minMicros+1)) * time.Microsecond
}

func sampleNumber(rng *mathrand.Rand, r NumberRange) float64 {
	min, max := int64(r.Min*100), int64(r.Max*100)
	if min == max {
		return float64(min) / 100
	}
	return float64(min+rng.Int63n(max-min+1)) / 100
}

func formatPercent(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64) + "%"
}

func scheduleFingerprint(schedule []FaultEvent) string {
	blob, _ := json.Marshal(schedule)
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}

func diagnosisFingerprint(rep *Report) DiagnosisFingerprint {
	fp := DiagnosisFingerprint{}
	for _, test := range rep.Tests {
		verdict := "<no-report>"
		if test.Diagnosis != nil {
			verdict = test.Diagnosis.Verdict
			for _, check := range test.Diagnosis.Checks {
				probe := ProbeFingerprint{Test: test.Name, ID: check.ID, Status: check.Status, Cause: check.Cause}
				if check.Families != nil {
					probe.IPv4, probe.IPv6 = check.Families.IPv4, check.Families.IPv6
				}
				fp.Probes = append(fp.Probes, probe)
			}
		}
		fp.Verdicts = append(fp.Verdicts, test.Name+"="+verdict)
	}
	sort.Strings(fp.Verdicts)
	sort.Slice(fp.Probes, func(i, j int) bool {
		a, b := fp.Probes[i], fp.Probes[j]
		return a.Test+"\x00"+a.ID+"\x00"+a.Status+"\x00"+a.Cause+"\x00"+a.IPv4+"\x00"+a.IPv6 <
			b.Test+"\x00"+b.ID+"\x00"+b.Status+"\x00"+b.Cause+"\x00"+b.IPv4+"\x00"+b.IPv6
	})
	blob, _ := json.Marshal(struct {
		Verdicts []string           `json:"verdicts"`
		Probes   []ProbeFingerprint `json:"probes"`
	}{fp.Verdicts, fp.Probes})
	sum := sha256.Sum256(blob)
	fp.ID = hex.EncodeToString(sum[:8])
	return fp
}

func (r *CampaignResult) finish() {
	if r.Outcomes == nil {
		r.Outcomes = []IterationResult{}
	}
	counts := make(map[string]*FingerprintCount)
	schedules := make(map[string]map[string]int)
	var durations []time.Duration
	for _, outcome := range r.Outcomes {
		item := counts[outcome.Fingerprint.ID]
		if item == nil {
			copy := outcome.Fingerprint
			item = &FingerprintCount{Fingerprint: copy}
			counts[outcome.Fingerprint.ID] = item
		}
		item.Count++
		if schedules[outcome.ScheduleID] == nil {
			schedules[outcome.ScheduleID] = make(map[string]int)
		}
		schedules[outcome.ScheduleID][outcome.Fingerprint.ID]++
		if outcome.Report != nil {
			durations = append(durations, outcome.Report.Duration)
		}
	}
	for _, item := range counts {
		r.Fingerprints = append(r.Fingerprints, *item)
	}
	sort.Slice(r.Fingerprints, func(i, j int) bool { return r.Fingerprints[i].Fingerprint.ID < r.Fingerprints[j].Fingerprint.ID })
	for _, outcome := range r.Outcomes {
		if len(schedules[outcome.ScheduleID]) > 1 {
			r.DivergentRuns++
		} else {
			r.StableRuns++
		}
	}
	if len(durations) > 0 {
		slices.Sort(durations)
		r.MinDuration, r.MaxDuration = durations[0], durations[len(durations)-1]
		r.MedianDuration = durations[len(durations)/2]
	}
	if r.Runs > 0 {
		r.FailurePercent = 100 * float64(r.Failed) / float64(r.Runs)
	}
	if r.DivergentRuns > 0 {
		r.Suggestions = append(r.Suggestions, Suggestion{Code: SuggestNondeterministic,
			Message:  "Identical fault schedules produced different structured diagnosis fingerprints; inspect timeout boundaries and probe ordering.",
			Evidence: fmt.Sprintf("%d of %d runs shared a schedule with a run that reached a different diagnosis", r.DivergentRuns, r.Runs)})
	}
	if r.Error != "" || r.Cancelled || r.Errors > 0 {
		r.Result = ResultError
	} else if r.Failed > 0 {
		r.Result = ResultFail
	} else {
		r.Result = ResultPass
	}
	if r.Fingerprints == nil {
		r.Fingerprints = []FingerprintCount{}
	}
	if r.Suggestions == nil {
		r.Suggestions = []Suggestion{}
	}
}

func (r *CampaignResult) WriteJSON(w io.Writer) error { return writeJSON(w, r) }

func (r *CampaignResult) WriteText(w io.Writer) {
	fmt.Fprintf(w, "Campaign: %s\nSeed:     %d\nResult:   %s\n", r.Scenario, r.Seed, r.Result)
	fmt.Fprintf(w, "Runs:     %d  passed %d  failed %d  errors %d\n", r.Runs, r.Passed, r.Failed, r.Errors)
	fmt.Fprintf(w, "Findings: false positives %d  false negatives %d  timeouts %d\n", r.FalsePositives, r.FalseNegatives, r.Timeouts)
	fmt.Fprintf(w, "Stability: stable runs %d  divergent runs %d  fingerprints %d\n", r.StableRuns, r.DivergentRuns, len(r.Fingerprints))
	if r.Runs > 0 {
		fmt.Fprintf(w, "Duration: min %s  median %s  max %s\n", r.MinDuration.Round(time.Millisecond), r.MedianDuration.Round(time.Millisecond), r.MaxDuration.Round(time.Millisecond))
	}
	for _, outcome := range r.Outcomes {
		status := ResultError
		if outcome.Report != nil {
			status = outcome.Report.Result
		}
		fmt.Fprintf(w, "  iteration %-4d seed %-20d schedule %s diagnosis %s  %s\n",
			outcome.Iteration, outcome.IterationSeed, outcome.ScheduleID, outcome.Fingerprint.ID, status)
	}
	if r.FirstFailure != nil {
		fmt.Fprintf(w, "\nFailure first observed:\n  campaign: %s\n  seed: %d\n  iteration: %d\n\nReproduce:\n  netdoc-sim campaign %s --seed %d --iteration %d\n",
			r.FirstFailure.Scenario, r.FirstFailure.Seed, r.FirstFailure.Iteration,
			r.FirstFailure.Scenario, r.FirstFailure.Seed, r.FirstFailure.Iteration)
	}
	if r.Error != "" {
		fmt.Fprintf(w, "\nError: %s\n", r.Error)
	}
}

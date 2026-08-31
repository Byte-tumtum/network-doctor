package diagnostic

import (
	"go/ast"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// confidenceOf runs one matrix case and returns the primary finding's
// confidence, failing the test when the case produces no finding at all.
func confidenceOf(t *testing.T, name string) Confidence {
	t.Helper()
	c := matrixCaseNamed(t, name)
	d := Interpret(c.target, c.order, c.res)
	if len(d.Findings) == 0 {
		t.Fatalf("case %q produced no finding", name)
	}
	return d.Findings[0].Confidence
}

// TestConfidenceOfRepresentativeFindings pins one run per level against the
// current truth table, choosing the arms where the reason for the level is the
// point rather than an accident of the fixture.
func TestConfidenceOfRepresentativeFindings(t *testing.T) {
	tests := []struct {
		matrixCase string
		want       Confidence
		why        string
	}{
		{
			matrixCase: "generic captive portal", want: ConfidenceHigh,
			why: "the egress probe observed the interception itself, and nothing else explains a portal redirect",
		},
		{
			matrixCase: "target refuses the connection", want: ConfidenceHigh,
			why: "a peer sent a refusal, which is a packet rather than an inference, and both silence explanations are ruled out",
		},
		{
			matrixCase: "generic name has no records", want: ConfidenceHigh,
			why: "two independent resolvers were asked the same question and agreed, which is the differential observation itself",
		},
		{
			matrixCase: "TLS expiry explained by a fast clock", want: ConfidenceHigh,
			why: "an independently measured offset points the same way as the certificate rejection",
		},
		{
			matrixCase: "generic system resolver failing", want: ConfidenceMedium,
			why: "the resolver comparison is observed, but the branch records that a general DNS failure is only weakened and never excluded",
		},
		{
			matrixCase: "path MTU black hole", want: ConfidenceMedium,
			why: "two stalls that correlate are evidence for a black hole and equally consistent with a slow server, which is why the identity says probable",
		},
		{
			matrixCase: "generic resolvers disagree", want: ConfidenceMedium,
			why: "a difference between resolvers is as often a deliberate split as a fault, and no observation here separates the two",
		},
		{
			matrixCase: "local device silent while this machine works", want: ConfidenceMedium,
			why: "a silent device is powered off, moved, or filtering, and none of those is distinguishable from this machine",
		},
		{
			matrixCase: "targeted DNS failure", want: ConfidenceLow,
			why: "resolution failed with no second opinion to separate a broken resolver from a name that does not exist",
		},
		{
			matrixCase: "TLS handshake failure", want: ConfidenceLow,
			why: "the handshake failed with no cause the client could classify, so a bad certificate, a wrong clock and interception all remain",
		},
		{
			matrixCase: "target unreachable with egress unchecked", want: ConfidenceInsufficientEvidence,
			why: "the finding's own meaning is that the observation separating a local path problem from a remote one was never made",
		},
		{
			matrixCase: "selected service check failed", want: ConfidenceInsufficientEvidence,
			why: "a selection removed the rungs that would have explained the failure, so there is no causal claim to be confident about",
		},
	}
	for _, test := range tests {
		t.Run(test.matrixCase, func(t *testing.T) {
			if got := confidenceOf(t, test.matrixCase); got != test.want {
				t.Errorf("confidence = %q, want %q: %s", got, test.want, test.why)
			}
		})
	}
}

// TestEveryCompletedFindingHasValidConfidence holds the two halves of the
// contract at once: a finished finding always carries one of the four values,
// and a run that reached no finding invents nothing to be confident about.
func TestEveryCompletedFindingHasValidConfidence(t *testing.T) {
	valid := map[Confidence]bool{
		ConfidenceHigh: true, ConfidenceMedium: true,
		ConfidenceLow: true, ConfidenceInsufficientEvidence: true,
	}
	for _, c := range diagnosisMatrix() {
		t.Run(c.name, func(t *testing.T) {
			d := Interpret(c.target, c.order, c.res)
			if d.Verdict == VerdictIncomplete && len(d.Findings) > 0 {
				t.Fatalf("an unfinished run produced findings: %+v", d.Findings)
			}
			for _, f := range d.Findings {
				if !valid[f.Confidence] {
					t.Errorf("finding %q has confidence %q, which is outside the vocabulary", f.ID, f.Confidence)
				}
			}
		})
	}
}

// TestConfidenceIsDeterministic reads the same run repeatedly. Confidence is
// meant to be measured against the field corpus later, and a value that moves
// between identical interpretations could not be calibrated against anything.
func TestConfidenceIsDeterministic(t *testing.T) {
	for _, c := range diagnosisMatrix() {
		first := Interpret(c.target, c.order, c.res)
		for range 5 {
			again := Interpret(c.target, c.order, c.res)
			if len(again.Findings) != len(first.Findings) {
				t.Fatalf("case %q returned a different number of findings", c.name)
			}
			for i, f := range again.Findings {
				if f.Confidence != first.Findings[i].Confidence {
					t.Errorf("case %q finding %q: confidence %q then %q", c.name, f.ID, first.Findings[i].Confidence, f.Confidence)
				}
			}
		}
	}
}

// TestConfidenceIsDescriptiveOnly is the invariant that makes the field safe to
// add. Remediate is the only exported function that takes a whole Diagnosis, so
// it is the one that could quietly start branching on strength of evidence:
// forcing every level in turn onto the same findings must not move its answer.
// The matrix test beside this one already pins the identity, sentence, verdict
// and blamed row of every arm, so those cannot have moved either.
func TestConfidenceIsDescriptiveOnly(t *testing.T) {
	levels := []Confidence{"", ConfidenceHigh, ConfidenceMedium, ConfidenceLow, ConfidenceInsufficientEvidence}
	for _, c := range diagnosisMatrix() {
		t.Run(c.name, func(t *testing.T) {
			d := Interpret(c.target, c.order, c.res)
			if len(d.Findings) == 0 {
				return
			}
			base, baseOK := Remediate(d, c.res, "linux")
			for _, level := range levels {
				forced := d
				forced.Findings = append([]DiagnosisFinding(nil), d.Findings...)
				for i := range forced.Findings {
					forced.Findings[i].Confidence = level
				}
				got, ok := Remediate(forced, c.res, "linux")
				if ok != baseOK || !reflect.DeepEqual(got, base) {
					t.Errorf("remediation moved when confidence was forced to %q: %+v/%v, want %+v/%v",
						level, got, ok, base, baseOK)
				}
			}
		})
	}
}

// TestMaterialContradictionPreventsHighConfidence states the contradiction rule
// on its own. A contradiction in this model weakens a named alternative without
// excluding it, so that alternative is still standing; the same evidence with
// the alternative ruled out instead is what earns the strongest claim.
func TestMaterialContradictionPreventsHighConfidence(t *testing.T) {
	base := DiagnosisFinding{
		ID:    DiagnosisTCPConnectionRefused,
		Focus: ProbeTargetTCP,
		Evidence: []CausalEvidence{
			{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationCause},
			{Kind: EvidenceRuledOut, Check: ProbeInternet, Observation: ObservationStatusPass, Candidate: DiagnosisLocalEgressFailure},
		},
	}
	if got := confidenceFor(base); got != ConfidenceHigh {
		t.Fatalf("observed conclusion with a finished differential = %q, want %q", got, ConfidenceHigh)
	}

	contradicted := base
	contradicted.Evidence = append(append([]CausalEvidence(nil), base.Evidence...),
		CausalEvidence{Kind: EvidenceContradiction, Check: ProbeInternet, Observation: ObservationStatusPass, Candidate: DiagnosisTargetUnreachable})
	if got := confidenceFor(contradicted); got != ConfidenceMedium {
		t.Errorf("surviving alternative = %q, want %q", got, ConfidenceMedium)
	}

	// The same candidate excluded elsewhere in the list settles it, which is
	// the difference between an alternative that is merely weakened and one
	// that is gone.
	resolved := contradicted
	resolved.Evidence = append(append([]CausalEvidence(nil), contradicted.Evidence...),
		CausalEvidence{Kind: EvidenceRuledOut, Check: ProbeTargetTCP, Observation: ObservationCause, Candidate: DiagnosisTargetUnreachable})
	if got := confidenceFor(resolved); got != ConfidenceHigh {
		t.Errorf("excluded alternative = %q, want %q", got, ConfidenceHigh)
	}

	// Repetition is not strength. Three copies of the same finished differential
	// say exactly what one of them said.
	repeated := base
	for range 3 {
		repeated.Evidence = append(repeated.Evidence, base.Evidence[1])
	}
	if got := confidenceFor(repeated); got != ConfidenceHigh {
		t.Errorf("repeated evidence = %q, want %q", got, ConfidenceHigh)
	}
}

// TestNotEvaluatedIsReadByReason is the other half of the evidence rule, and
// the one that is easy to get crudely wrong: a check skipped behind the very
// failure being diagnosed is a consequence of the conclusion, and treating it
// as a gap would lower confidence precisely because the diagnosis was right.
func TestNotEvaluatedIsReadByReason(t *testing.T) {
	base := DiagnosisFinding{
		ID:    DiagnosisDNSNameNotFound,
		Focus: ProbeDNS,
		Evidence: []CausalEvidence{
			{Kind: EvidenceSupport, Check: ProbeDNS, Observation: ObservationDNSNotFound},
			{Kind: EvidenceRuledOut, Check: ProbeDNSPublic, Observation: ObservationDNSNotFound, Candidate: DiagnosisSystemDNSFailure},
		},
	}
	consequences := []NotEvaluatedReason{NotEvaluatedPrerequisite, NotEvaluatedNotApplicable}
	for _, reason := range consequences {
		f := base
		f.Evidence = append(append([]CausalEvidence(nil), base.Evidence...),
			CausalEvidence{Kind: EvidenceNotEvaluated, Check: ProbeTargetTCP, Reason: reason})
		if got := confidenceFor(f); got != ConfidenceHigh {
			t.Errorf("downstream %s lowered confidence to %q", reason, got)
		}
	}
	gaps := []NotEvaluatedReason{NotEvaluatedNotSelected, NotEvaluatedIncomplete}
	for _, reason := range gaps {
		f := base
		f.Evidence = append(append([]CausalEvidence(nil), base.Evidence...),
			CausalEvidence{Kind: EvidenceNotEvaluated, Check: ProbeInternet, Reason: reason})
		if got := confidenceFor(f); got != ConfidenceMedium {
			t.Errorf("missing observation %s left confidence at %q, want %q", reason, got, ConfidenceMedium)
		}
	}
}

// TestSkippedDownstreamRungsKeepDNSConfidence is the same rule read off a real
// run rather than a hand-built finding: a DNS conclusion with the whole target
// path skipped behind it must not be punished for the skips it caused.
func TestSkippedDownstreamRungsKeepDNSConfidence(t *testing.T) {
	target := &Target{Host: "example.com", Port: 443, Proto: ProtoTLSHTTP}
	order := []ProbeID{ProbeIface, ProbeInternet, ProbeDNS, ProbeDNSPublic, ProbeTargetTCP, ProbeTLS}
	res := map[ProbeID]ProbeResult{
		ProbeIface:     {Status: StatusPass},
		ProbeInternet:  {Status: StatusPass},
		ProbeDNS:       {Status: StatusFail, DNSNotFound: true},
		ProbeDNSPublic: {Status: StatusPass, DNSNotFound: true},
		ProbeTargetTCP: SkipPrereq(ProbeTargetTCP),
		ProbeTLS:       SkipPrereq(ProbeTLS),
	}
	d := Interpret(target, order, res)
	if len(d.Findings) == 0 || d.Findings[0].ID != DiagnosisDNSNameNotFound {
		t.Fatalf("diagnosis = %+v", d)
	}
	finding := d.Findings[0]
	skipped := false
	for _, e := range finding.Evidence {
		if e.Kind == EvidenceNotEvaluated && e.Reason == NotEvaluatedPrerequisite {
			skipped = true
		}
	}
	if !skipped {
		t.Fatal("fixture no longer carries a prerequisite-blocked check, so it cannot test this rule")
	}
	if finding.Confidence != ConfidenceHigh {
		t.Errorf("confidence = %q, want %q: the skipped rungs are consequences of the DNS answer, not gaps in it",
			finding.Confidence, ConfidenceHigh)
	}
}

// TestUnresolvedFindingsAreNeverConfident guards the trap the vocabulary
// invites: netdoc can be entirely certain that it did not test something, and
// that certainty is not evidence for a cause.
func TestUnresolvedFindingsAreNeverConfident(t *testing.T) {
	for _, id := range []DiagnosisID{
		DiagnosisReachabilityUntested,
		DiagnosisSelectedDNSCheckFailed,
		DiagnosisSelectedServiceCheckFailed,
		DiagnosisSelectedNetworkCheckFailed,
	} {
		f := DiagnosisFinding{ID: id, Evidence: []CausalEvidence{
			{Kind: EvidenceSupport, Check: ProbeTargetTCP, Observation: ObservationStatusFail},
			{Kind: EvidenceRuledOut, Check: ProbeInternet, Observation: ObservationStatusPass, Candidate: DiagnosisOffline},
		}}
		if got := confidenceFor(f); got != ConfidenceInsufficientEvidence {
			t.Errorf("%s with supporting evidence piled on = %q, want %q", id, got, ConfidenceInsufficientEvidence)
		}
	}
}

// TestEveryDiagnosisIDHasAConfidenceClass makes the policy table total. An
// identity nobody classified would silently answer insufficient_evidence, which
// reads as a considered judgment rather than as the omission it is.
func TestEveryDiagnosisIDHasAConfidenceClass(t *testing.T) {
	declared := map[DiagnosisID]string{}
	for _, spec := range declaredConstants(t, "finding.go", "DiagnosisID") {
		for i, name := range spec.Names {
			value, err := strconv.Unquote(spec.Values[i].(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			declared[DiagnosisID(value)] = name.Name
			if _, classified := diagnosisConfidence[DiagnosisID(value)]; !classified {
				t.Errorf("%s (%q) has no entry in diagnosisConfidence", name.Name, value)
			}
		}
	}
	for id := range diagnosisConfidence {
		if _, exists := declared[id]; !exists {
			t.Errorf("diagnosisConfidence classifies %q, which is not a declared diagnosis ID", id)
		}
	}
	if got := confidenceFor(DiagnosisFinding{ID: "not_a_real_identity"}); got != ConfidenceInsufficientEvidence {
		t.Errorf("unclassified identity = %q, want the conservative %q", got, ConfidenceInsufficientEvidence)
	}
}

// TestConfidenceIsDocumented keeps the published vocabulary and its
// documentation in step, for the same reason the diagnosis IDs are: these words
// reach a consumer through the JSON report and the .ndoc file.
func TestConfidenceIsDocumented(t *testing.T) {
	docs, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range declaredConstants(t, "confidence.go", "Confidence") {
		for i, name := range spec.Names {
			value, err := strconv.Unquote(spec.Values[i].(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			if !stableIDRe.MatchString(value) {
				t.Errorf("%s = %q is not stable lower_snake_case", name.Name, value)
			}
			if !strings.Contains(string(docs), "`"+value+"`") {
				t.Errorf("%s (%q) is not documented in docs/reference.md", name.Name, value)
			}
		}
	}
}

package main

import (
	"testing"

	"github.com/heymaikol/network-doctor/internal/simulation"
)

// The menu takes a number or the diagnosis by name. Both are exact: an
// out-of-range number and a name that merely resembles one are both asked again.
func TestPickAnswerTakesNumbersAndNames(t *testing.T) {
	for _, tt := range []struct {
		typed string
		want  simulation.ChallengeAnswer
	}{
		{"1", simulation.AnswerHealthy},
		{"2", simulation.AnswerDNSFailure},
		{"dns", simulation.AnswerDNSFailure},
		{"tcp_port_blocked", simulation.AnswerPortBlocked},
		{"tcp port blocked", simulation.AnswerPortBlocked},
		{"connection refused", simulation.AnswerRefused},
	} {
		got, ok := pickAnswer(tt.typed)
		if !ok || got.ID != tt.want {
			t.Errorf("pickAnswer(%q) = %q, %t; want %s", tt.typed, got.ID, ok, tt.want)
		}
	}
	for _, typed := range []string{"0", "-1", "999", "1.5", "tcp", "connection", "nonsense"} {
		if got, ok := pickAnswer(typed); ok {
			t.Errorf("pickAnswer(%q) resolved to %s; it must ask again", typed, got.ID)
		}
	}
}

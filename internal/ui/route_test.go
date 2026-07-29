package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMTRRouteQuality(t *testing.T) {
	tests := []struct {
		fixture string
		want    string
		ok      bool
	}{
		{"mtr-clean.txt", "destination: 0% loss · 12.4ms avg", true},
		{"mtr-loss.txt", "destination: 20% loss · 160.1ms avg", true},
		{"mtr-unreached.txt", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := parseMTRRouteQuality(strings.Split(string(data), "\n"))
			if ok != tt.ok || ok && got.String() != tt.want {
				t.Fatalf("parseMTRRouteQuality() = %q, %v; want %q, %v", got.String(), ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRouteQualityRequiresCompleteRawOutput(t *testing.T) {
	lines := []string{"  1.|-- 203.0.113.9  0.0%  5  12.0  12.4  12.0  13.0  0.4"}
	for _, job := range []jobState{
		{name: "mtr", status: JobRunning, lines: lines},
		{name: "mtr", status: JobDone, lines: lines, dropped: 1},
		{name: "mtr", status: JobDone, lines: lines, evicted: 1},
		{name: "pathping", status: JobDone, lines: lines},
	} {
		if got, ok := job.routeQuality(); ok {
			t.Errorf("%+v produced unsupported route quality %q", job, got)
		}
	}
}

func TestRouteQualityKeepsRawOutputInViewAndReport(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "mtr-loss.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	m := newModel(nil, false)
	m.width, m.height = 100, 30
	m.cur = jobState{
		name: "mtr", display: "mtr example.com", status: JobDone, lines: lines,
	}

	for name, output := range map[string]string{
		"view":   m.jobView(20),
		"report": m.report(),
	} {
		for _, want := range []string{
			"destination: 20% loss · 160.1ms avg",
			"4.|-- 203.0.113.9",
		} {
			if !strings.Contains(output, want) {
				t.Errorf("%s missing %q:\n%s", name, want, output)
			}
		}
	}
	if pane := m.jobView(5); pane != "" {
		t.Errorf("route summary overflowed a five-line pane:\n%s", pane)
	}
	if lines := strings.Count(m.jobView(6), "\n"); lines > 6 {
		t.Errorf("six-line pane rendered %d lines", lines)
	}
}

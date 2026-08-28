package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/heymaikol/network-doctor/internal/diagnostic"
	ndoc "github.com/heymaikol/network-doctor/internal/snapshot"
)

func recordWatchPass(m *model, at time.Time, failing bool, iface string) {
	m.results = make(map[diagnostic.ProbeID]diagnostic.ProbeResult, len(m.probes))
	for _, probe := range m.probes {
		status := diagnostic.StatusPass
		if failing && probe.ID == diagnostic.ProbeTargetTCP {
			status = diagnostic.StatusFail
		}
		m.results[probe.ID] = diagnostic.ProbeResult{ID: probe.ID, Status: status, Dur: time.Millisecond}
	}
	result := m.results[diagnostic.ProbeTargetTCP]
	result.Iface = iface
	m.results[diagnostic.ProbeTargetTCP] = result
	diagnostic.Finalize(m.results)
	m.now = func() time.Time { return at }
	m.recordRun()
}

func TestWatchCapturesAndDisplaysOneContinuingIncident(t *testing.T) {
	start := time.Date(2026, 8, 25, 12, 3, 41, 0, time.UTC)
	m := newModel(mustTarget(t, "example.com:443"), false)
	m.watch, m.width, m.height = true, 100, 40
	recordWatchPass(&m, start, false, "wlan0")
	recordWatchPass(&m, start.Add(5*time.Second), true, "wg0")
	recordWatchPass(&m, start.Add(10*time.Second), true, "wg0")
	recordWatchPass(&m, start.Add(15*time.Second), false, "wlan0")

	items := m.incidents.Incidents()
	if len(items) != 1 || items[0].Passes != 2 || items[0].Active() || items[0].Duration(start.Add(time.Minute)) != 10*time.Second {
		t.Fatalf("incidents = %+v, want one recovered incident with two failing passes", items)
	}
	if help := m.helpView(false); strings.Contains(help, "incidents") {
		t.Fatalf("watch footer advertises incident inspection: %s", help)
	}
	if header := m.headerView(); !strings.Contains(header, "last incident recovered after 10s") {
		t.Fatalf("watch header does not summarize the latest incident: %s", header)
	}

	u, _ := m.Update(keyMsg("i"))
	shown := asModel(t, u)
	view := shown.View()
	for _, want := range []string{
		"Watch incidents", "state: recovered", "pre-failure state:", "Changes at failure onset",
		"target_tcp interface changed", "does not establish", "Diagnosis", "causal evidence:",
		"While failing",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("incident view is missing %q:\n%s", want, view)
		}
	}
	u, _ = shown.Update(keyPress("end"))
	shown = asModel(t, u)
	if view = shown.View(); !strings.Contains(view, "Recovery") || !strings.Contains(view, "Changes when connectivity returned") {
		t.Errorf("incident recovery is not reachable by scrolling to the end:\n%s", view)
	}
	if u, _ = shown.Update(keyMsg("q")); asModel(t, u).incidentViewing {
		t.Error("q did not close incident inspection")
	}
}

func TestWatchSupportsMultipleIncidentNavigation(t *testing.T) {
	start := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	m := newModel(mustTarget(t, "example.com:443"), false)
	m.watch, m.width, m.height = true, 100, 30
	for n := 0; n < 2; n++ {
		base := start.Add(time.Duration(n*20) * time.Second)
		recordWatchPass(&m, base, false, "wlan0")
		recordWatchPass(&m, base.Add(5*time.Second), true, "wg0")
		recordWatchPass(&m, base.Add(10*time.Second), false, "wlan0")
	}
	m.openIncidentViewer()
	if m.incidentSelected != 1 || !strings.Contains(m.incidentView(), "2 of 2") {
		t.Fatalf("viewer did not open on latest incident: selected=%d", m.incidentSelected)
	}
	u, _ := m.handleIncidentKey(keyPress("left"))
	m = asModel(t, u)
	if m.incidentSelected != 0 || !strings.Contains(m.incidentView(), "1 of 2") {
		t.Fatalf("left did not select the earlier incident: selected=%d", m.incidentSelected)
	}
}

func TestIncidentExportIsCompatibleNdoc(t *testing.T) {
	start := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	m := newModel(mustTarget(t, "example.com:443"), false)
	m.watch, m.width, m.height = true, 100, 30
	recordWatchPass(&m, start, false, "wlan0")
	recordWatchPass(&m, start.Add(5*time.Second), true, "wg0")
	recordWatchPass(&m, start.Add(10*time.Second), false, "wlan0")
	m.openIncidentViewer()

	original := incidentWriteFile
	t.Cleanup(func() { incidentWriteFile = original })
	var path string
	var data []byte
	incidentWriteFile = func(name string, content []byte, _ os.FileMode) error {
		path, data = name, append([]byte(nil), content...)
		return nil
	}
	u, _ := m.handleIncidentKey(keyMsg("w"))
	m = asModel(t, u)
	if !strings.HasSuffix(path, ndoc.Extension) || !strings.Contains(m.notice, "incident saved") {
		t.Fatalf("export path/notice = %q / %q", path, m.notice)
	}
	got, err := ndoc.Decode(data)
	if err != nil {
		t.Fatalf("exported incident does not decode: %v\n%s", err, data)
	}
	if got.Incident == nil || got.Incident.Passes != 1 || got.Incident.Before == nil || got.Incident.Recovered == nil {
		t.Fatalf("exported incident = %+v", got.Incident)
	}
}

func TestCancelDuringIncidentKeepsItActive(t *testing.T) {
	start := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	m := newModel(mustTarget(t, "example.com:443"), false)
	m.watch = true
	recordWatchPass(&m, start, false, "wlan0")
	recordWatchPass(&m, start.Add(5*time.Second), true, "wg0")
	m.doRestart()
	final, _ := m.quit()
	m = asModel(t, final)
	active, ok := m.incidents.Active()
	if !ok || !active.Active() || active.Recovered != nil || active.Passes != 1 {
		t.Fatalf("active incident after cancellation = %+v, present=%v", active, ok)
	}
	if ExitCode(m) != 1 {
		t.Error("cancellation during a failing incident lost the last completed failing exit state")
	}
}

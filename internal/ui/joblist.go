// The set of tool runs: iteration, lookup by job id, and which one is selected.
// The selected run lives in model.cur and the parked ones in model.otherJobs —
// that split is this file's business, and nothing outside it should walk both
// fields by hand.

package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// jobs yields every run worth counting, selected first. An empty cur slot is
// not a run, so callers that mean "all jobs" never trip over the zero value.
func (m *model) jobs(yield func(*jobState) bool) {
	if m.hasJob() && !yield(&m.cur) {
		return
	}
	for i := range m.otherJobs {
		if !yield(&m.otherJobs[i]) {
			return
		}
	}
}

// jobByID finds the run a tool message belongs to. nil means the job is gone —
// a restart cleared it — and the message should be dropped.
func (m *model) jobByID(id string) *jobState {
	for j := range m.jobs {
		if j.active != nil && j.active.id == id {
			return j
		}
	}
	return nil
}

func (m model) jobsRunning() bool {
	for j := range m.jobs {
		if j.active != nil {
			return true
		}
	}
	return false
}

func (m *model) cancelJobs() {
	for j := range m.jobs {
		if j.active != nil && j.active.cancel != nil {
			j.active.cancel()
		}
	}
}

// stashJob parks the selected run at the end of the ring, leaving the slot free
// for a new one. An empty slot is dropped rather than parked — a zero jobState
// must never become a tab stop.
func (m *model) stashJob() {
	if m.hasJob() {
		m.otherJobs = append(m.otherJobs, m.cur)
		m.cur = jobState{}
	}
}

// selectJob pulls otherJobs[i] into the selected slot, parking whatever was
// there at the end of the ring. Both tab (round-robin) and 'v' (re-show the LAN
// scan by name) go through here, so they can't disagree about the ring order.
func (m *model) selectJob(i int) {
	next := m.otherJobs[i]
	m.otherJobs = append(m.otherJobs[:i], m.otherJobs[i+1:]...)
	m.stashJob()
	m.cur = next
}

// switchJob is tab: select the oldest parked run.
func (m *model) switchJob() tea.Cmd {
	if len(m.otherJobs) == 0 {
		return nil
	}
	m.selectJob(0)
	m.networkMap = false
	if m.viewing {
		m.follow = true
	}
	// Keep the armed quit intact so the next Ctrl+C still quits.
	if m.notice == ctrlCNotice && time.Now().Before(m.noticeDeadline) {
		if m.viewing {
			m.refreshViewport()
		}
		return nil
	}
	return m.setNotice("switched to "+m.cur.name, true)
}

// The Actions menu: one list of what the run can do right now, so a reader who
// has not learned the keys can still find them. It owns no commands of its
// own. Every row is an action from the shared key table or a drill-down tool,
// it carries whatever key the active preset binds, and running one
// goes through the same dispatch the keyboard does.

package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// actionItem is one row of the Actions menu: what to call it, the key that
// runs it under the active preset, and which action or tool it is. A tool row
// leaves act zero; enter finds the tool by that key, the way the keyboard does.
type actionItem struct {
	name string
	key  string
	act  keyAction
}

// actionAvailable reports whether act would do something in the current state.
// It is the single answer both the help bar and the Actions menu ask, so what
// a reader is offered cannot drift from what the key actually does.
func (m model) actionAvailable(act keyAction) bool {
	switch act {
	case actOpen:
		// Mirrors the dispatch: on the map enter opens a device or diagnoses a
		// service, and only an empty device list leaves it free for the job pane.
		if m.networkMap {
			if m.svc.host != "" {
				return len(m.svc.scan.Open) > 0
			}
			if len(m.networkHosts()) > 0 {
				return true
			}
		}
		return m.hasJob()
	case actCancelJob:
		return m.networkMap && m.svc.host != "" || m.cur.active != nil
	case actSwitchJob:
		return len(m.otherJobs) > 0
	case actExplain:
		d := m.diagnosis()
		return m.allDone() && len(d.Findings) > 0 && len(d.Findings[0].Evidence) > 0
	case actIncidents:
		return m.watch && len(m.incidents.Incidents()) > 0
	case actCopy:
		return m.selectedPortalURL() != "" || m.reportReady()
	case actSave:
		return m.reportReady()
	case actRetest:
		// Offered once there is a finished run to rerun. Before that the chain
		// is either already running or waiting on the restart key, and a second
		// way to start it would only be a second name for restart.
		return m.allDone() && m.chainRan()
	case actSSH:
		return m.sshDetected()
	case actExpand:
		if m.expanded {
			return m.allDone()
		}
		_, hiddenPass, hiddenNA := m.compactRows()
		return hiddenPass+hiddenNA > 0
	case actNetworkMap, actRestart, actActions, actTheme, actHelp, actQuit:
		return true
	}
	return false
}

// actionName is the menu's wording for a built-in action: the table's name,
// specialised where the action itself changes with what is on screen.
func (m model) actionName(def actionDef) string {
	switch def.act {
	case actOpen:
		if m.networkMap {
			if m.svc.host != "" {
				return "Diagnose service"
			}
			if len(m.networkHosts()) > 0 {
				return "Open device"
			}
		}
	case actCancelJob:
		if m.networkMap && m.svc.host != "" {
			return "Back to devices"
		}
	case actNetworkMap:
		if m.networkMap {
			return "Back to checks"
		}
	case actCopy:
		if m.selectedPortalURL() != "" {
			return "Copy portal URL"
		}
	case actExpand:
		if m.expanded {
			return "Collapse checks"
		}
	}
	return def.menu
}

// actionItems is what the current state can do: the bound list-context actions
// that are available right now, in cheatsheet order, then the drill-down tools
// whose binary is installed. Both halves are read from the definitions dispatch
// itself uses, so the menu cannot offer a key that does nothing, miss one that
// works, or name a tool differently from its hotkey.
func (m model) actionItems() []actionItem {
	var items []actionItem
	for _, def := range actionDefs {
		if def.menu == "" || !m.keys.bound(ctxList, def.act) || !m.actionAvailable(def.act) {
			continue
		}
		items = append(items, actionItem{
			name: m.actionName(def),
			key:  m.keys.label(ctxList, def.act),
			act:  def.act,
		})
	}
	for _, tool := range m.tools {
		// The menu lists only tools it can run now.
		if !tool.Available {
			continue
		}
		items = append(items, actionItem{
			name: strings.ToUpper(tool.Name[:1]) + tool.Name[1:],
			key:  tool.Key,
		})
	}
	return items
}

// handleActionsKey drives the Actions menu. Enter runs the selected row and esc
// closes, as they do in the theme picker; everything else is resolved through
// the very list bindings the menu is advertising, so a reader who already knows
// a shortcut can press it here and get exactly what it does outside the menu.
func (m model) handleActionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.actionItems()
	last := max(len(items)-1, 0)
	// The list is rebuilt from live state, so a row that went away under an
	// open menu must not leave the cursor past the end.
	m.actionsSel = min(m.actionsSel, last)
	switch msg.String() {
	case "enter":
		m.actionsOpen = false
		if len(items) == 0 {
			return m, nil
		}
		item := items[m.actionsSel]
		if item.act == actNone {
			if tool, ok := m.toolForKey(item.key); ok {
				return m.runTool(tool)
			}
			return m, nil
		}
		return m.runAction(item.act)
	case "esc":
		m.actionsOpen = false
		return m, nil
	}
	act, pending := m.resolveKey(ctxList, msg.String())
	m.pendingKeys = pending
	switch act {
	case actNone:
		if len(pending) > 0 {
			return m, nil // a chord owns the keyboard until it completes
		}
		if tool, ok := m.toolForKey(msg.String()); ok {
			m.actionsOpen = false
			return m.runTool(tool)
		}
	case actActions:
		m.actionsOpen = false
	case actUp:
		m.actionsSel = max(m.actionsSel-1, 0)
	case actDown:
		m.actionsSel = min(m.actionsSel+1, last)
	case actTop:
		m.actionsSel = 0
	case actBottom:
		m.actionsSel = last
	default:
		m.actionsOpen = false
		return m.runAction(act)
	}
	return m, nil
}

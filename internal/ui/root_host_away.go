package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// HostAwayMsg says the client's service went and is being waited for (#210).
type HostAwayMsg struct{}

// HostBackMsg says the service is back. What the window subscribed to comes
// again whole.
type HostBackMsg struct{}

// hostAwayTickMsg is the end of the moment absence number absence is let pass
// unnamed.
type hostAwayTickMsg struct{ absence int }

// hostAwayAfter is how long the host may be away before the window says so:
// a service restarting is back sooner than that, and is not worth a word.
var hostAwayAfter = 2 * time.Second

// handleHostAway starts an absence of the host. Enter stops sending at once;
// the status bar names the absence only if it lasts.
func (m RootModel) handleHostAway() (RootModel, tea.Cmd) {
	m = m.closeVideoPlayer()
	m.cancelExternalVideo()
	if !m.hostAway {
		m.hostAway = true
		m.absences++
	}
	m.chat.SetHostAway(true)
	absence := m.absences
	return m, tea.Tick(hostAwayAfter, func(time.Time) tea.Msg { return hostAwayTickMsg{absence: absence} })
}

// handleHostAwayTick names the absence it was started for, if the host is
// still away in it.
func (m RootModel) handleHostAwayTick(msg hostAwayTickMsg) RootModel {
	if m.hostAway && msg.absence == m.absences {
		m.statusBar.SetHostAway("host unreachable, reconnecting")
	}
	return m
}

// handleHostBack ends the absence.
func (m RootModel) handleHostBack() RootModel {
	m.hostAway = false
	m.chat.SetHostAway(false)
	m.statusBar.SetHostAway("")
	return m
}

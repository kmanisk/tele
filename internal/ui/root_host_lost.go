package ui

import tea "charm.land/bubbletea/v2"

// HostLostMsg says the client's link to its host is gone. Quit is set when the
// host said it was stopping first - the tele that held the account quit -
// and clear when the connection simply broke (#208). For a service the window
// waited for, GaveUp says it did not come back in time, and UpdatedTo names the
// newer version it came back on, which turns this older window away (#210,
// #183).
type HostLostMsg struct {
	Quit      bool
	GaveUp    bool
	UpdatedTo string
	// Uninstalled says the service was uninstalled, and nothing starts it
	// again (#182).
	Uninstalled bool
}

// handleHostLost leaves the window with nothing to show but why: a window
// that looks alive and receives nothing is the failure the whole design exists
// to avoid. What was being typed is kept, so the way out can hand it back.
func (m RootModel) handleHostLost(msg HostLostMsg) (RootModel, tea.Cmd) {
	m = m.closeVideoPlayer()
	m.cancelExternalVideo()
	if m.screen == ScreenMain {
		m.unsent = m.chat.ComposerValue()
	}
	var cause string
	switch {
	case msg.Uninstalled:
		cause = "The tele service holding this account was uninstalled.\n`tele daemon install` installs it again."
	case msg.UpdatedTo != "":
		cause = "tele was updated: the service holding this account now runs " + msg.UpdatedTo + "."
	case msg.GaveUp:
		cause = "The tele service holding this account went and did not come back.\n`tele daemon status` says whether it runs."
	case msg.Quit:
		cause = "The tele that held this account has quit."
	default:
		cause = "The connection to the tele that held this account broke."
	}
	cause += "\nStart tele again to carry on."
	m.screen = ScreenLogin
	return m.routeLoginError(m.loginErrorText(cause, ""))
}

// WithOtherWindows says this window holds the host, and how many other
// windows are attached to it now: quitting closes them too. Unset in a window
// attached to another tele's host, which closes only itself.
func (m RootModel) WithOtherWindows(count func() int) RootModel {
	m.othersAttached = count
	return m
}

// otherWindows is how many other windows quitting this one would close.
func (m RootModel) otherWindows() int {
	if m.othersAttached == nil {
		return 0
	}
	return m.othersAttached()
}

// Unsent is what was being typed when the host was lost, empty otherwise.
func (m RootModel) Unsent() string { return m.unsent }

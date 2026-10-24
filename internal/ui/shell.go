package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/core"
)

// Shell is the model the program runs. It outlives the accounts that pass
// through the process, as the client's attachment does, while the RootModel
// inside it is built for one account and replaced when that account ends
// (#297). The client builds the next one itself, on hearing from the host that
// the account ended (#309).
//
// Being the only model the program runs, it is also where every command the
// model starts is stamped with the model's epoch, and every late answer to one
// from a model that was replaced is dropped. That is the client's own job: what
// arrives on the stream is ordered by the host, but the answer to a command
// the model ran itself never crosses the stream.
type Shell struct {
	epoch uint64
	root  RootModel
	// next builds the model for the account that follows.
	next func() RootModel
}

// NewShell runs root, and next for each account after it.
func NewShell(root RootModel, next func() RootModel) Shell {
	return Shell{epoch: 1, root: root, next: next}
}

func (s Shell) Init() tea.Cmd { return stamp(s.epoch, s.root.Init()) }

// Update takes a message only from the current epoch, or one no epoch marked,
// such as input from the terminal or the stream, and stamps whatever the model
// asks for.
func (s Shell) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ended, ok := msg.(core.AccountEnded); ok {
		return s.nextAccount(ended)
	}
	if em, ok := msg.(epochMsg); ok {
		if em.epoch != s.epoch {
			return s, nil
		}
		msg = em.msg
	}
	next, cmd := s.root.Update(msg)
	s.root = next.(RootModel)
	return s, stamp(s.epoch, cmd)
}

func (s Shell) View() tea.View { return s.root.View() }

// Unsent is what was being typed when the host was lost, empty otherwise.
func (s Shell) Unsent() string { return s.root.Unsent() }

// nextAccount puts a new model on screen in place of the one whose account
// ended. Nothing of the old model carries over but the terminal's size, which
// belongs to neither: what an account had on screen is the account's.
func (s Shell) nextAccount(ended core.AccountEnded) (tea.Model, tea.Cmd) {
	s.root = s.root.closeVideoPlayer()
	s.root.cancelExternalVideo()
	width, height := s.root.width, s.root.height
	s.epoch++
	s.root = s.next()
	s.root.login.SetNotice(loginNotice(ended))
	init := s.root.Init()
	if width > 0 && height > 0 {
		next, cmd := s.root.Update(tea.WindowSizeMsg{Width: width, Height: height})
		s.root = next.(RootModel)
		init = tea.Batch(init, cmd)
	}
	return s, stamp(s.epoch, init)
}

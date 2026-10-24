package ui

import (
	"context"
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/sorokin-vladimir/tele/internal/domain"
)

// Linux can select a known URL-capable player without asking xdg-open to
// interpret an HTTP URL (which usually launches a browser). Other platforms
// retain their established full-download/system-player behavior.
func streamPlayer(goos string, lookPath func(string) (string, error)) string {
	if goos != "linux" {
		return ""
	}
	path, _ := lookPath("mpv")
	return path
}

type externalVideoStartedMsg struct {
	serial  int
	done    <-chan error
	ctx     context.Context
	release func()
	chatID  int64
	msgID   int
}

func (m *RootModel) cancelExternalVideo() {
	if m.externalVideoCancel != nil {
		m.externalVideoCancel()
		m.externalVideoCancel = nil
	}
}

func (m RootModel) startVideoExternal(msgID int, label string) (RootModel, tea.Cmd) {
	m = m.closeVideoPlayer()
	m.cancelExternalVideo()
	player := streamPlayer(runtime.GOOS, exec.LookPath)
	if player == "" {
		return m.startDocumentOpen(msgID, label)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.externalVideoCancel = cancel
	serial := m.statusBar.StartTransfer("Opening video…")
	o, chatID, tmpDir := m.owner, m.currentChatID, m.tmpDir
	seq := playbackSequence.Add(1)
	return m, func() tea.Msg {
		stream, release, err := openPlaybackStream(ctx, o, chatID, msgID, seq)
		if err != nil {
			return openDocumentCmd(ctx, o, chatID, msgID, tmpDir, serial)()
		}
		cmd := exec.CommandContext(ctx, player, "--no-terminal", "--force-window=yes", "--http-proxy=", "--", stream.URL)
		if err := cmd.Start(); err != nil {
			release()
			return openDocumentCmd(ctx, o, chatID, msgID, tmpDir, serial)()
		}
		done := make(chan error, 1)
		// Reap even if the UI exits before it consumes the started message.
		go func() { err := cmd.Wait(); release(); done <- err }()
		return externalVideoStartedMsg{serial: serial, done: done, ctx: ctx, release: release, chatID: chatID, msgID: msgID}
	}
}

func (m RootModel) handleExternalVideoStarted(msg externalVideoStartedMsg) (RootModel, tea.Cmd) {
	m.statusBar.ClearTransfer(msg.serial)
	o, tmpDir := m.owner, m.tmpDir
	return m, func() tea.Msg {
		defer msg.release()
		err := <-msg.done
		if err == nil || msg.ctx.Err() != nil {
			return nil
		}
		// A player/container failure gets one ordinary file fallback. Cancel
		// the HTTP session before saving; explicit downloads remain separate.
		msg.release()
		path, err := o.SaveMedia(msg.ctx, msg.chatID, msg.msgID, domain.DocFull, tmpDir, "")
		if err != nil {
			return errStatus("open video", err)
		}
		openPath(path)
		return nil
	}
}

package ui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/domain"
	vmedia "github.com/sorokin-vladimir/tele/internal/media"
	"github.com/sorokin-vladimir/tele/internal/videostream"
)

type streamOwnerStub struct {
	Owner
	closed atomic.Int32
	saved  atomic.Int32
	err    error
	gate   <-chan struct{}
}

func (o *streamOwnerStub) OpenMediaStream(context.Context, int64, int, uint64) (core.MediaStream, error) {
	if o.gate != nil {
		<-o.gate
	}
	return core.MediaStream{ID: 1, URL: "http://127.0.0.1:12345/opaque/video"}, o.err
}
func (o *streamOwnerStub) CloseMediaStream(context.Context, uint64) error {
	o.closed.Add(1)
	return nil
}
func (o *streamOwnerStub) SaveMedia(ctx context.Context, _ int64, _ int, _ domain.MediaSlot, _, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	o.saved.Add(1)
	return "/local/video.mp4", nil
}

func TestPlaybackSourceProgressiveAndCancellation(t *testing.T) {
	o := &streamOwnerStub{}
	ctx, cancel := context.WithCancel(context.Background())
	path, release, err := playbackSource(ctx, o, 1, 5, t.TempDir(), 1)
	require.NoError(t, err)
	require.Contains(t, path, "http://127.0.0.1:")
	require.Zero(t, o.saved.Load())
	cancel()
	require.Eventually(t, func() bool { return o.closed.Load() == 1 }, time.Second, time.Millisecond)
	release()
	require.Equal(t, int32(1), o.closed.Load())
}

func TestPlaybackSourceFallbackAndLateOpen(t *testing.T) {
	o := &streamOwnerStub{err: errors.New("not supported")}
	path, release, err := playbackSource(context.Background(), o, 1, 5, t.TempDir(), 1)
	require.NoError(t, err)
	require.Equal(t, "/local/video.mp4", path)
	release()
	require.Equal(t, int32(1), o.saved.Load())
	gate := make(chan struct{})
	o = &streamOwnerStub{gate: gate}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	close(gate)
	_, release, err = playbackSource(ctx, o, 1, 5, t.TempDir(), 1)
	require.NoError(t, err)
	release()
	require.Equal(t, int32(1), o.closed.Load(), "late capability must still be released")
}

func TestStreamPlayerSelection(t *testing.T) {
	lookup := func(string) (string, error) { return "/usr/bin/mpv", nil }
	require.Equal(t, "/usr/bin/mpv", streamPlayer("linux", lookup))
	for _, os := range []string{"darwin", "windows", "freebsd"} {
		require.Empty(t, streamPlayer(os, lookup))
	}
	require.Empty(t, streamPlayer("linux", func(string) (string, error) { return "", exec.ErrNotFound }))
}

func TestVideoSwitchAndStaleResponse(t *testing.T) {
	m := NewRootModel(false)
	m.ctx = context.Background()
	ctx, cancel := context.WithCancel(m.ctx)
	old := &videoPlayer{ctx: ctx, cancel: cancel}
	m.videoPlayer = old
	m, _ = m.openVideoModal(domain.DocumentRef{ID: 7}, 5, 1, "sender")
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	var released bool
	newPlayer := m.videoPlayer
	m, _ = m.handleVideoProbed(videoProbedMsg{target: old, release: func() { released = true }})
	require.True(t, released)
	require.Same(t, newPlayer, m.videoPlayer)
	// Even the same document reopening has a different identity.
	m, _ = m.openVideoModal(domain.DocumentRef{ID: 7}, 5, 1, "sender")
	require.ErrorIs(t, newPlayer.ctx.Err(), context.Canceled)
	require.Greater(t, m.videoPlayer.streamSeq, newPlayer.streamSeq, "sequence is reserved before an asynchronous command can run late")
	m = m.closeVideoPlayer()
}

// A real ffmpeg decode through the UI's command/message path. No rendering
// changes are involved, and the owner double refuses full-file saves.
type uiStreamOwner struct {
	Owner
	session *videostream.Session
	seq     uint64
	data    []byte
	closed  chan struct{}
}

func (o *uiStreamOwner) OpenMediaStream(ctx context.Context, _ int64, _ int, seq uint64) (core.MediaStream, error) {
	s, err := videostream.Open(context.Background(), int64(len(o.data)), "video/mp4", func(_ context.Context, off int64, b []byte) (int, error) {
		return bytes.NewReader(o.data).ReadAt(b, off)
	})
	if err != nil {
		return core.MediaStream{}, err
	}
	o.session = s
	o.seq = seq
	return core.MediaStream{ID: seq, URL: s.URL}, nil
}
func (o *uiStreamOwner) CloseMediaStream(_ context.Context, seq uint64) error {
	if seq == o.seq {
		o.session.Close()
		close(o.closed)
	}
	return nil
}
func (o *uiStreamOwner) SaveMedia(context.Context, int64, int, domain.MediaSlot, string, string) (string, error) {
	return "", errors.New("UI must stream instead of saving")
}

func TestVideoModalProgressiveSource(t *testing.T) {
	if !vmedia.HasFFmpeg() || !vmedia.HasFFprobe() {
		t.Skip("ffmpeg required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "video.mp4")
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x120:rate=15", "-t", "2", "-c:v", "mpeg4", "-an", path)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	o := &uiStreamOwner{data: data, closed: make(chan struct{})}
	m := NewRootModel(false)
	m.ctx = ctx
	m.owner = o
	m.width = 100
	m.height = 40
	m, open := m.openVideoModal(domain.DocumentRef{ID: 7}, 5, 2, "sender")
	probed, ok := open().(videoProbedMsg)
	require.True(t, ok)
	require.NoError(t, probed.err)
	require.True(t, probed.progressive)
	m, _ = m.handleVideoProbed(probed)
	require.NotNil(t, m.videoPlayer.source)
	m, read := m.handleVideoTick(videoTickMsg{gen: m.videoPlayer.gen})
	require.NotNil(t, read)
	frame, ok := read().(videoFrameMsg)
	require.True(t, ok)
	require.True(t, frame.ok)
	m, _ = m.handleVideoFrame(frame)
	require.NotNil(t, m.videoPlayer.frame)
	source := m.videoPlayer.source
	m = m.closeVideoPlayer()
	require.Nil(t, m.videoPlayer)
	require.NoError(t, source.Close())
	select {
	case <-o.closed:
	case <-time.After(time.Second):
		t.Fatal("UI did not close stream")
	}
}

func TestExternalPlayerExitReleasesStream(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux mpv selection")
	}
	dir := t.TempDir()
	// No graphical player is launched by this ownership test.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mpv"), []byte("#!/bin/sh\nexit 0\n"), 0700))
	t.Setenv("PATH", dir)
	o := &streamOwnerStub{}
	m := NewRootModel(false)
	m.owner = o
	m, start := m.startVideoExternal(5, "video")
	ready, ok := start().(externalVideoStartedMsg)
	require.True(t, ok)
	m, wait := m.handleExternalVideoStarted(ready)
	require.Nil(t, wait())
	require.Equal(t, int32(1), o.closed.Load())
	require.Zero(t, o.saved.Load())
	require.False(t, m.statusBar.DownloadActive())
	m.cancelExternalVideo()
}

func TestExternalPlayerCancellationReapsAndReleases(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux mpv selection")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mpv"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700))
	t.Setenv("PATH", dir)
	o := &streamOwnerStub{}
	m := NewRootModel(false)
	m.owner = o
	m, start := m.startVideoExternal(5, "video")
	ready, ok := start().(externalVideoStartedMsg)
	require.True(t, ok)
	m.cancelExternalVideo()
	select {
	case err := <-ready.done:
		require.Error(t, err, "cancelled player should exit unsuccessfully")
	case <-time.After(2 * time.Second):
		t.Fatal("owned player was not reaped")
	}
	require.Eventually(t, func() bool { return o.closed.Load() == 1 }, time.Second, time.Millisecond)
	require.Zero(t, o.saved.Load(), "cancel must not trigger a fallback download")
}

func TestOpeningPhotoCancelsExternalVideo(t *testing.T) {
	m := NewRootModel(false)
	ctx, cancel := context.WithCancel(context.Background())
	m.externalVideoCancel = cancel
	m, _ = m.openPhotoModal(domain.PhotoRef{ID: 7}, 5, "sender", time.Time{})
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestAccountSwitchCancelsVideoContexts(t *testing.T) {
	m := NewRootModel(false)
	ctx, cancel := context.WithCancel(context.Background())
	external, stop := context.WithCancel(context.Background())
	m.videoPlayer = &videoPlayer{ctx: ctx, cancel: cancel}
	m.externalVideoCancel = stop
	s := NewShell(m, nextAccountRoot)
	_, _ = s.Update(core.AccountEnded{})
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.ErrorIs(t, external.Err(), context.Canceled)
}

func TestExternalPlayerFailureFallsBackToCompletedFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux mpv selection")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mpv"), []byte("#!/bin/sh\nexit 1\n"), 0700))
	t.Setenv("PATH", dir)
	previous := openPath
	var opened string
	openPath = func(path string) { opened = path }
	t.Cleanup(func() { openPath = previous })
	o := &streamOwnerStub{}
	m := NewRootModel(false)
	m.owner = o
	m, start := m.startVideoExternal(5, "video")
	ready, ok := start().(externalVideoStartedMsg)
	require.True(t, ok)
	m, wait := m.handleExternalVideoStarted(ready)
	require.Nil(t, wait())
	require.Equal(t, "/local/video.mp4", opened)
	require.Equal(t, int32(1), o.saved.Load())
	require.Equal(t, int32(1), o.closed.Load())
	m.cancelExternalVideo()
}

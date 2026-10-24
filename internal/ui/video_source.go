package ui

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sorokin-vladimir/tele/internal/core"
	"github.com/sorokin-vladimir/tele/internal/domain"
)

var playbackSequence atomic.Uint64

// playbackSource is a local file or a loopback capability. release is always
// safe to repeat. A cancelled UI operation closes even a late IPC response.
func playbackSource(ctx context.Context, o Owner, chatID int64, msgID int, tmpDir string, seq uint64) (string, func(), error) {
	stream, release, err := openPlaybackStream(ctx, o, chatID, msgID, seq)
	if err != nil {
		path, err := o.SaveMedia(ctx, chatID, msgID, domain.DocFull, tmpDir, "")
		return path, func() {}, err
	}
	return stream.URL, release, nil
}

func openPlaybackStream(ctx context.Context, o Owner, chatID int64, msgID int, id uint64) (core.MediaStream, func(), error) {
	release := streamRelease(ctx, o, id)
	stream, err := o.OpenMediaStream(ctx, chatID, msgID, id)
	if err != nil {
		release()
	}
	return stream, release, err
}

func streamRelease(ctx context.Context, o Owner, id uint64) func() {
	var once sync.Once
	closeStream := func() {
		once.Do(func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = o.CloseMediaStream(closeCtx, id)
		})
	}
	stop := context.AfterFunc(ctx, closeStream)
	return func() { stop(); closeStream() }
}

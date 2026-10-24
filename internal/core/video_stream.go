package core

import (
	"context"
	"errors"
	"time"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	"github.com/sorokin-vladimir/tele/internal/tg"
	"github.com/sorokin-vladimir/tele/internal/videostream"
)

// MediaStream contains only a loopback capability, never account state.
type MediaStream struct {
	ID  uint64
	URL string
}

func (f *mediaFetcher) openVideo(ctx context.Context, chatID int64, msgID int) (*videostream.Session, error) {
	reader, ok := f.client.(tg.DocumentChunkReader)
	if !ok {
		return nil, errors.New("ranged video transport unavailable")
	}
	msg, err := messageByID(f.state, chatID, msgID)
	if err != nil {
		return nil, err
	}
	ref, err := resolveMediaRef(msg, domain.DocFull)
	if err != nil {
		return nil, err
	}
	if !ref.kind.IsVideo() || ref.doc.Size <= 0 {
		return nil, errors.New("video stream requires a video with known size")
	}
	identity, size := ref.doc.ID, ref.doc.Size
	// Called serially by the bounded source; fresh references stay in this
	// closure, on the owner side. Each range has a bounded network deadline.
	return videostream.Open(ctx, size, ref.doc.MimeType, func(ctx context.Context, off int64, dst []byte) (int, error) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		read := func() (int, error) {
			if ref.doc.ID != identity || ref.doc.Size != size {
				return 0, errors.New("video changed while streaming")
			}
			return reader.ReadDocumentChunk(ctx, ref.doc, off, dst)
		}
		n, err := read()
		if telerr.Of(err) == telerr.StaleReference {
			fresh, refreshErr := f.refreshReference(ctx, chatID, msgID, ref)
			if refreshErr != nil {
				return 0, refreshErr
			}
			ref = fresh
			return read() // exactly one retry of the same range
		}
		if telerr.Of(err) == telerr.Network {
			timer := time.NewTimer(150 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-timer.C:
				return read() // one transient retry, no background retry loop
			}
		}
		return n, err
	})
}

func (s *seat) openMediaStream(ctx context.Context, chatID int64, msgID int, seq uint64) (MediaStream, error) {
	if err := ctx.Err(); err != nil {
		return MediaStream{}, err
	}
	// Reserve a monotonically increasing client sequence before creating the
	// listener. A cancellation may arrive before Open, or its reply may be lost;
	// a close through this sequence prevents any late Open from leaking a port.
	s.mu.Lock()
	if s.left || seq <= s.videoSequence || seq <= s.videoClosedThrough {
		s.mu.Unlock()
		return MediaStream{}, context.Canceled
	}
	s.videoSequence = seq
	old := s.videoStream
	s.videoStream = nil
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	stream, err := s.media.openVideo(s.ctx, chatID, msgID)
	if err != nil {
		return MediaStream{}, err
	}
	s.mu.Lock()
	if s.left || ctx.Err() != nil || s.videoSequence != seq || s.videoClosedThrough >= seq {
		s.mu.Unlock()
		stream.Close()
		return MediaStream{}, context.Canceled
	}
	s.videoStream = stream
	s.mu.Unlock()
	return MediaStream{ID: seq, URL: stream.URL}, nil
}

func (s *seat) closeMediaStream(seq uint64) {
	s.mu.Lock()
	s.videoClosedThrough = max(s.videoClosedThrough, seq)
	var stream *videostream.Session
	if s.videoSequence <= seq {
		stream = s.videoStream
		s.videoStream = nil
	}
	s.mu.Unlock()
	if stream != nil {
		stream.Close()
	}
}

func (s *seat) stopVideoStream() {
	s.mu.Lock()
	stream := s.videoStream
	s.videoStream = nil
	s.mu.Unlock()
	if stream != nil {
		stream.Close()
	}
}

func (a *Attachment) OpenMediaStream(ctx context.Context, chatID int64, msgID int, seq uint64) (MediaStream, error) {
	return call(a, ctx, func(ctx context.Context, s *seat) (MediaStream, error) {
		return s.openMediaStream(ctx, chatID, msgID, seq)
	})
}

func (a *Attachment) CloseMediaStream(ctx context.Context, id uint64) error {
	return do(a, ctx, func(_ context.Context, s *seat) error {
		s.closeMediaStream(id)
		return nil
	})
}

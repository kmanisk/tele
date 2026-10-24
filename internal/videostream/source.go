// Package videostream provides private, short-lived video byte streams.
package videostream

import (
	"context"
	"errors"
	"io"

	"github.com/hashicorp/golang-lru/v2"
)

// ChunkSize respects upload.getFile's precise alignment: a multiple of 4 KiB
// dividing 1 MiB, with no request crossing a 1 MiB boundary.
const ChunkSize = 128 << 10

const cacheChunks = 8

// ChunkFunc reads one aligned chunk. Only the final chunk may be short.
type ChunkFunc func(context.Context, int64, []byte) (int, error)

// source serializes cache misses, collapsing overlapping requests without
// uncancellable workers. One Telegram RPC per stream also bounds read-ahead.
// The cache is scoped to one immutable document, so offset uniquely names data.
type source struct {
	size  int64
	chunk ChunkFunc
	gate  chan struct{}
	cache *lru.Cache[int64, []byte]
}

func newSource(size int64, chunk ChunkFunc) *source {
	cache, _ := lru.New[int64, []byte](cacheChunks)
	return &source{size: size, chunk: chunk, gate: make(chan struct{}, 1), cache: cache}
}

func (s *source) readAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if off < 0 {
		return 0, errors.New("negative video offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= s.size {
		return 0, io.EOF
	}
	n := 0
	for len(p) > 0 && off < s.size {
		start := off / ChunkSize * ChunkSize
		b, err := s.load(ctx, start)
		if err != nil {
			return n, err
		}
		copied := copy(p, b[off-start:])
		n += copied
		off += int64(copied)
		p = p[copied:]
	}
	if len(p) > 0 {
		return n, io.EOF
	}
	return n, nil
}

func (s *source) load(ctx context.Context, off int64) ([]byte, error) {
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b, ok := s.cache.Get(off); ok {
		return b, nil
	}
	b := make([]byte, ChunkSize)
	n, err := s.chunk(ctx, off, b)
	want := int(min(int64(ChunkSize), s.size-off))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if n != want {
		return nil, io.ErrUnexpectedEOF
	}
	b = b[:n]
	s.cache.Add(off, b)
	return b, nil
}

type requestReader struct {
	ctx context.Context
	s   *source
}

func (r requestReader) ReadAt(p []byte, off int64) (int, error) {
	return r.s.readAt(r.ctx, p, off)
}

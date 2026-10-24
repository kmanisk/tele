package videostream

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceRangesAndBounds(t *testing.T) {
	data := make([]byte, ChunkSize*20+31)
	for i := range data {
		data[i] = byte(i % 251)
	}
	var calls atomic.Int32
	s := newSource(int64(len(data)), func(ctx context.Context, off int64, b []byte) (int, error) {
		require.Zero(t, off%ChunkSize)
		require.Len(t, b, ChunkSize)
		calls.Add(1)
		return bytes.NewReader(data).ReadAt(b, off)
	})
	for _, tc := range []struct{ off, length int }{
		{0, 20}, {ChunkSize * 10, 33}, {ChunkSize - 7, 20},
		{len(data) - 10, 10}, {len(data) - 10, 40}, {len(data), 1}, {len(data) + 1, 1},
	} {
		b := make([]byte, tc.length)
		n, err := s.readAt(context.Background(), b, int64(tc.off))
		want := min(tc.length, max(0, len(data)-tc.off))
		require.Equal(t, want, n)
		if want < tc.length {
			require.ErrorIs(t, err, io.EOF)
		} else {
			require.NoError(t, err)
		}
		if want > 0 {
			require.Equal(t, data[tc.off:tc.off+want], b[:n])
		}
	}
	before := calls.Load()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			b := make([]byte, 40)
			_, err := s.readAt(context.Background(), b, ChunkSize*15+1)
			require.NoError(t, err)
		})
	}
	wg.Wait()
	require.Equal(t, before+1, calls.Load(), "overlapping reads share a chunk")
	for i := range 20 {
		_, err := s.readAt(context.Background(), make([]byte, 1), int64(i*ChunkSize))
		require.NoError(t, err)
		require.LessOrEqual(t, s.cache.Len(), cacheChunks)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.readAt(ctx, make([]byte, 1), 0)
	require.ErrorIs(t, err, context.Canceled)
}

func TestSourceShortChunkAndCancelledWaiter(t *testing.T) {
	s := newSource(ChunkSize, func(context.Context, int64, []byte) (int, error) { return 3, nil })
	_, err := s.readAt(context.Background(), make([]byte, 5), 0)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Zero(t, s.cache.Len())
	s.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.load(ctx, 0)
	require.ErrorIs(t, err, context.Canceled)
	<-s.gate
}

func TestTwoGiBSeeksStayBounded(t *testing.T) {
	const size int64 = 2 << 30
	var offsets []int64
	s := newSource(size, func(_ context.Context, off int64, b []byte) (int, error) {
		offsets = append(offsets, off)
		return len(b), nil
	})
	for _, percent := range []int64{0, 10, 50, 80} {
		before := len(offsets)
		off := size*percent/100 + 123
		_, err := s.readAt(context.Background(), make([]byte, 50), off)
		require.NoError(t, err)
		require.Len(t, offsets, before+1, "one tiny seek needs one aligned chunk")
		require.Equal(t, off/ChunkSize*ChunkSize, offsets[before])
	}
	for i := range 200 {
		_, err := s.readAt(context.Background(), make([]byte, 50), int64(i)*ChunkSize*17)
		require.NoError(t, err)
		var retained int
		for _, b := range s.cache.Values() {
			retained += cap(b)
		}
		require.LessOrEqual(t, retained, cacheChunks*ChunkSize)
	}
}

func TestSourceQueuedReadCancellation(t *testing.T) {
	started := make(chan struct{})
	leaderDone := make(chan error, 1)
	s := newSource(10*ChunkSize, func(ctx context.Context, _ int64, _ []byte) (int, error) {
		close(started)
		<-ctx.Done()
		return 0, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _, err := s.readAt(ctx, make([]byte, 1), 0); leaderDone <- err }()
	<-started
	waiter, stop := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() { _, err := s.readAt(waiter, make([]byte, 1), ChunkSize); waiterDone <- err }()
	stop()
	require.ErrorIs(t, <-waiterDone, context.Canceled, "queued read must not wait for the active RPC")
	cancel()
	require.ErrorIs(t, <-leaderDone, context.Canceled)
}

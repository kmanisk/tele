package core

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	"github.com/sorokin-vladimir/tele/internal/videostream"
)

type rangedClient struct {
	mediaStub
	read func(context.Context, domain.DocumentRef, int64, []byte) (int, error)
}

func (c *rangedClient) ReadDocumentChunk(ctx context.Context, ref domain.DocumentRef, off int64, b []byte) (int, error) {
	return c.read(ctx, ref, off, b)
}

func streamOwner(t *testing.T, c *rangedClient, size int64) (*Owner, *Attachment) {
	t.Helper()
	o, st := newOwnerWithClient(t, c)
	st.Store().SetChat(domain.Chat{ID: 1, Peer: domain.Peer{ID: 1, Type: domain.PeerUser}})
	st.Store().SetMessages(1, []domain.Message{{ID: 5, ChatID: 1, Document: &domain.DocumentRef{ID: 11, Size: size, MimeType: "video/mp4", FileReference: []byte("old")}, Media: &domain.MediaRef{Kind: domain.MediaVideo}}})
	a := attach(o)
	t.Cleanup(o.Stop)
	t.Cleanup(a.Detach)
	return o, a
}

func streamGet(t *testing.T, address, ran string) []byte {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest(http.MethodGet, address, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Range", ran)
	res, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, 206, res.StatusCode)
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return b
}

func TestVideoStreamRefreshSharedWithDownloads(t *testing.T) {
	const size = 20 * videostream.ChunkSize
	var calls atomic.Int32
	c := &rangedClient{}
	c.refreshed = domain.Message{ID: 5, ChatID: 1, Document: &domain.DocumentRef{ID: 11, Size: size, MimeType: "video/mp4", FileReference: []byte("fresh")}, Media: &domain.MediaRef{Kind: domain.MediaVideo}}
	c.read = func(_ context.Context, ref domain.DocumentRef, off int64, b []byte) (int, error) {
		calls.Add(1)
		if string(ref.FileReference) == "old" {
			return 0, &telerr.Error{Kind: telerr.StaleReference}
		}
		for i := range b {
			b[i] = byte(off / int64(videostream.ChunkSize))
		}
		return len(b), nil
	}
	o, a := streamOwner(t, c, size)
	// Separate clients can fail with the same old reference. Refresh must be
	// shared across streams as well as with full-file downloads.
	b := attach(o)
	defer b.Detach()
	first, err := a.OpenMediaStream(context.Background(), 1, 5, 1)
	require.NoError(t, err)
	second, err := b.OpenMediaStream(context.Background(), 1, 5, 1)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for _, address := range []string{first.URL, second.URL} {
		wg.Go(func() { require.Equal(t, bytes.Repeat([]byte{15}, 16), streamGet(t, address, "bytes=1966080-1966095")) })
	}
	wg.Wait()
	_, _, _, refreshed := c.calls()
	require.Equal(t, 1, refreshed)
	require.Equal(t, int32(4), calls.Load(), "same aligned range is retried once per stale stream")
	// An explicit saved download still uses the ordinary writer API.
	c.payload = "full download"
	dir := t.TempDir()
	path, err := a.SaveMedia(context.Background(), 1, 5, domain.DocFull, dir, "")
	require.NoError(t, err)
	require.NotEmpty(t, path)
	_, docs, _, refreshed := c.calls()
	require.Equal(t, 1, docs)
	require.Equal(t, 1, refreshed)
}

func TestVideoStreamRefreshCooldownAndIdentity(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale again", true: "changed document"}[changed], func(t *testing.T) {
			c := &rangedClient{}
			c.refreshed = domain.Message{ID: 5, ChatID: 1, Document: &domain.DocumentRef{ID: 11, Size: 100, FileReference: []byte("fresh")}, Media: &domain.MediaRef{Kind: domain.MediaVideo}}
			if changed {
				c.refreshed.Document.ID = 99
			}
			var reads atomic.Int32
			c.read = func(context.Context, domain.DocumentRef, int64, []byte) (int, error) {
				reads.Add(1)
				return 0, &telerr.Error{Kind: telerr.StaleReference}
			}
			o, _ := streamOwner(t, c, 100)
			s, err := o.media.openVideo(context.Background(), 1, 5)
			require.NoError(t, err)
			defer s.Close()
			// Drive the gateway, then confirm repeated expiry never loops.
			client := &http.Client{Timeout: time.Second}
			defer client.CloseIdleConnections()
			for range 2 {
				res, err := client.Get(s.URL)
				require.NoError(t, err)
				_, err = io.ReadAll(res.Body)
				_ = res.Body.Close()
				require.Error(t, err, "failed source must truncate the advertised body")
			}
			_, _, _, n := c.calls()
			require.Equal(t, 1, n)
			if changed {
				require.Equal(t, int32(1), reads.Load())
			} else {
				require.Equal(t, int32(3), reads.Load())
			}
		})
	}
}

func TestVideoStreamAttachmentLifecycle(t *testing.T) {
	c := &rangedClient{read: func(context.Context, domain.DocumentRef, int64, []byte) (int, error) { return 0, io.EOF }}
	o, a := streamOwner(t, c, 100)
	closed := func(stream MediaStream) {
		u, err := url.Parse(stream.URL)
		require.NoError(t, err)
		conn, err := net.DialTimeout("tcp", u.Host, time.Second)
		if conn != nil {
			_ = conn.Close()
		}
		require.Error(t, err)
	}
	first, err := a.OpenMediaStream(context.Background(), 1, 5, 1)
	require.NoError(t, err)
	second, err := a.OpenMediaStream(context.Background(), 1, 5, 2)
	require.NoError(t, err)
	closed(first)
	// A stale close must not close the replacement.
	require.NoError(t, a.CloseMediaStream(context.Background(), first.ID))
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodHead, second.URL, http.NoBody)
	res, err := client.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	a.Detach()
	closed(second)
	b := attach(o)
	defer b.Detach()
	third, err := b.OpenMediaStream(context.Background(), 1, 5, 1)
	require.NoError(t, err)
	o.Stop()
	closed(third)
}

func TestVideoStreamCloseBeforeOpenReply(t *testing.T) {
	c := &rangedClient{read: func(context.Context, domain.DocumentRef, int64, []byte) (int, error) { return 0, io.EOF }}
	_, a := streamOwner(t, c, 100)
	// A UI can cancel before its Open reaches the owner. The close names the
	// already-known sequence, so even a lost Open reply cannot leak a listener.
	require.NoError(t, a.CloseMediaStream(context.Background(), 10))
	_, err := a.OpenMediaStream(context.Background(), 1, 5, 10)
	require.ErrorIs(t, err, context.Canceled)
	stream, err := a.OpenMediaStream(context.Background(), 1, 5, 11)
	require.NoError(t, err)
	require.NoError(t, a.CloseMediaStream(context.Background(), 10))
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest(http.MethodHead, stream.URL, http.NoBody)
	res, err := client.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
}

func TestVideoStreamRetriesTransientRangeOnce(t *testing.T) {
	var calls atomic.Int32
	c := &rangedClient{read: func(_ context.Context, _ domain.DocumentRef, off int64, b []byte) (int, error) {
		require.Zero(t, off)
		if calls.Add(1) == 1 {
			return 0, &telerr.Error{Kind: telerr.Network, Transient: true}
		}
		return 100, nil
	}}
	_, a := streamOwner(t, c, 100)
	s, err := a.OpenMediaStream(context.Background(), 1, 5, 1)
	require.NoError(t, err)
	require.Equal(t, make([]byte, 10), streamGet(t, s.URL, "bytes=0-9"))
	require.Equal(t, int32(2), calls.Load())
}

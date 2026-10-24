package videostream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPRanges(t *testing.T) {
	data := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	var calls atomic.Int32
	s, err := Open(context.Background(), int64(len(data)), "video/mp4", func(_ context.Context, off int64, b []byte) (int, error) {
		calls.Add(1)
		return bytes.NewReader(data).ReadAt(b, off)
	})
	require.NoError(t, err)
	defer s.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		method, ran, body, contentRange string
		code                            int
	}{
		{"HEAD", "", "", "", 200},
		{"GET", "bytes=5-5", "5", "bytes 5-5/36", 206},
		{"GET", "bytes=10-15", "abcdef", "bytes 10-15/36", 206},
		{"GET", "bytes=33-", "xyz", "bytes 33-35/36", 206},
		{"GET", "bytes=-3", "xyz", "bytes 33-35/36", 206},
		{"GET", "bytes=34-999", "yz", "bytes 34-35/36", 206},
		{"GET", "", string(data), "", 200},
		{"GET", "bytes=36-", "", "bytes */36", 416},
		{"GET", "bytes=6-2", "", "", 416},
		{"GET", "bytes=0-1,4-5", "", "bytes */36", 416},
		{"POST", "", "", "", 405},
	} {
		t.Run(tc.method+tc.ran, func(t *testing.T) {
			req, e := http.NewRequest(tc.method, s.URL, http.NoBody)
			require.NoError(t, e)
			if tc.ran != "" {
				req.Header.Set("Range", tc.ran)
			}
			res, e := client.Do(req)
			require.NoError(t, e)
			defer func() { _ = res.Body.Close() }()
			b, e := io.ReadAll(res.Body)
			require.NoError(t, e)
			require.Equal(t, tc.code, res.StatusCode)
			if tc.contentRange != "" {
				require.Equal(t, tc.contentRange, res.Header.Get("Content-Range"))
			}
			if tc.code < 300 {
				require.Equal(t, tc.body, string(b))
				require.Equal(t, "bytes", res.Header.Get("Accept-Ranges"))
				require.Equal(t, "video/mp4", res.Header.Get("Content-Type"))
				if tc.method == "GET" {
					require.Equal(t, fmt.Sprint(len(b)), res.Header.Get("Content-Length"))
				}
			}
			if tc.method == "HEAD" {
				require.Zero(t, calls.Load())
				require.Equal(t, "36", res.Header.Get("Content-Length"))
			}
		})
	}
	u, err := url.Parse(s.URL)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(u.Host, "127.0.0.1:"))
	req, _ := http.NewRequest(http.MethodGet, "http://"+u.Host+"/unknown", http.NoBody)
	res, err := client.Do(req)
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, 404, res.StatusCode)
	s.Close()
	conn, err := net.DialTimeout("tcp", u.Host, time.Second)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err, "closed listener must disappear")
}

func TestOpenRejectsMissingSize(t *testing.T) {
	s, err := Open(context.Background(), 0, "video/mp4", func(context.Context, int64, []byte) (int, error) {
		t.Fatal("must not read an unknown size")
		return 0, nil
	})
	require.Nil(t, s)
	require.Error(t, err)
}

func TestDisconnectAndShutdownCancelReads(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(fmt.Sprint(closeSession), func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			s, err := Open(context.Background(), 10*ChunkSize, "video/mp4", func(ctx context.Context, _ int64, _ []byte) (int, error) {
				close(started)
				<-ctx.Done()
				close(stopped)
				return 0, ctx.Err()
			})
			require.NoError(t, err)
			defer s.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, http.NoBody)
			require.NoError(t, err)
			client := &http.Client{Timeout: 3 * time.Second}
			defer client.CloseIdleConnections()
			done := make(chan struct{})
			go func() {
				defer close(done)
				res, e := client.Do(req)
				if e == nil {
					_ = res.Body.Close()
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("read did not start")
			}
			if closeSession {
				s.Close()
			} else {
				cancel()
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("read was not cancelled")
			}
			<-done
		})
	}
}

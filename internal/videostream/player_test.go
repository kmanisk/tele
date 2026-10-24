package videostream

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/media"
)

// fixtureStream exercises the production gateway with a disk-backed fake
// Telegram source. The player never receives this file's path.
func fixtureStream(t *testing.T) (*Session, func() (int64, []int64), int64) {
	t.Helper()
	if !media.HasFFmpeg() || !media.HasFFprobe() {
		t.Skip("ffmpeg and ffprobe required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	path := filepath.Join(t.TempDir(), "video.mp4")
	// Leave the moov atom at the end: startup must read there without fetching
	// the intervening video. MPEG4 avoids depending on a particular H264 build.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25", "-t", "60", "-c:v", "mpeg4", "-q:v", "2", "-an", path)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	info, err := f.Stat()
	require.NoError(t, err)
	var mu sync.Mutex
	var fetched int64
	var offsets []int64
	s, err := Open(ctx, info.Size(), "video/mp4", func(ctx context.Context, off int64, b []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := f.ReadAt(b, off)
		mu.Lock()
		fetched += int64(n)
		offsets = append(offsets, off)
		mu.Unlock()
		return n, err
	})
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s, func() (int64, []int64) {
		mu.Lock()
		defer mu.Unlock()
		return fetched, append([]int64(nil), offsets...)
	}, info.Size()
}

func TestFFmpegProgressive(t *testing.T) {
	s, stats, size := fixtureStream(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	meta, err := media.ProbeVideo(ctx, s.URL)
	require.NoError(t, err)
	require.Equal(t, 320, meta.Width)
	fs, err := media.OpenFrameSource(ctx, s.URL, 80, 60, 15)
	require.NoError(t, err)
	_, ok := fs.Next()
	require.True(t, ok)
	firstFrameTime := time.Since(start)
	firstFrameBytes, _ := stats()
	require.NoError(t, fs.Close())
	fetched, offsets := stats()
	require.Less(t, fetched, size/4, "first frame must not require the full source")
	require.Contains(t, offsets, int64(0))
	var tail bool
	for _, off := range offsets {
		tail = tail || off > size*9/10
	}
	require.True(t, tail, "metadata at the end requires an out-of-order range")
	t.Logf("synthetic size=%d first_frame=%s first_frame_bytes=%d bytes_at_close=%d chunks=%d", size, firstFrameTime, firstFrameBytes, fetched, len(offsets))
	// Input seeking asks for a later region, rather than decoding the prefix.
	before := len(offsets)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-ss", "48", "-i", s.URL, "-frames:v", "1", "-f", "null", "-")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "ffmpeg seek failed (%d output bytes)", len(out))
	after, offsets := stats()
	var middle bool
	for _, off := range offsets[before:] {
		middle = middle || (off > size/2 && off < size*9/10)
	}
	require.True(t, middle, "seek must retrieve later video bytes directly")
	require.Less(t, after-fetched, size/4)
	t.Logf("seek_80pct_bytes=%d", after-fetched)
}

// Opt-in, headless mpv smoke test; ordinary unit tests never require mpv.
func TestMPVProgressive(t *testing.T) {
	if os.Getenv("TELE_MPV_TEST") != "1" {
		t.Skip("set TELE_MPV_TEST=1 for headless mpv validation")
	}
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv unavailable")
	}
	s, stats, size := fixtureStream(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	socket := filepath.Join(t.TempDir(), "mpv.sock")
	cmd := exec.CommandContext(ctx, "mpv", "--no-config", "--terminal=no", "--vo=null", "--ao=null", "--pause", "--cache=no", "--demuxer-readahead-secs=0", "--input-ipc-server="+socket, s.URL)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	var conn net.Conn
	require.Eventually(t, func() bool { var err error; conn, err = net.Dial("unix", socket); return err == nil }, 5*time.Second, 20*time.Millisecond)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	property := func(name string) any {
		require.NoError(t, enc.Encode(map[string]any{"command": []any{"get_property", name}, "request_id": 7}))
		for {
			var reply map[string]any
			require.NoError(t, dec.Decode(&reply))
			if reply["request_id"] == float64(7) {
				return reply["data"]
			}
		}
	}
	require.Eventually(t, func() bool { return property("time-pos") != nil }, 5*time.Second, 30*time.Millisecond)
	fetched, offsets := stats()
	require.Less(t, fetched, size/4)
	require.NoError(t, enc.Encode(map[string]any{"command": []any{"seek", 48, "absolute+exact"}}))
	require.Eventually(t, func() bool { n, ok := property("time-pos").(float64); return ok && n >= 47 }, 5*time.Second, 30*time.Millisecond)
	require.NoError(t, enc.Encode(map[string]any{"command": []any{"set_property", "pause", false}}))
	require.Eventually(t, func() bool {
		_, all := stats()
		for _, off := range all[len(offsets):] {
			if off > size/2 && off < size*9/10 {
				return true
			}
		}
		return false
	}, 5*time.Second, 30*time.Millisecond)
	after, seekOffsets := stats()
	var direct bool
	for _, off := range seekOffsets[len(offsets):] {
		direct = direct || (off > size/2 && off < size*9/10)
	}
	require.True(t, direct)
	require.Less(t, after-fetched, size/3)
	require.NoError(t, enc.Encode(map[string]any{"command": []any{"quit"}}))
	// The UI owns this same process lifetime and closes the stream on Wait.
	s.Close()
	t.Logf("mpv size=%d startup_bytes=%d seek_80pct_bytes=%d", size, fetched, after-fetched)
}

package videostream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Descriptor is the only stream state a UI receives. ID is an opaque capability
// used to close this session; neither field contains Telegram credentials.
// Do not log either field.
type Descriptor struct {
	ID  string
	URL string
}

// Session owns one listener and a bounded cache. Closing it cancels all reads.
type Session struct {
	Descriptor
	cancel context.CancelFunc
	done   chan struct{}
}

type boundedListener struct{ net.Listener }

func (l boundedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if tcp, ok := c.(*net.TCPConn); ok {
		// Loopback can otherwise queue several MiB before a player cancels a
		// probe or seek. Bound speculative Telegram reads in the socket too.
		_ = tcp.SetWriteBuffer(64 << 10)
	}
	return c, err
}

// Open binds only IPv4 loopback, using an OS-assigned port and a 256-bit path.
// Sessions also expire after four hours as a backstop for abandoned clients.
func Open(parent context.Context, size int64, mime string, chunk ChunkFunc) (*Session, error) {
	if size <= 0 || chunk == nil {
		return nil, errors.New("video stream needs a known positive size and source")
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Hour)
	id := hex.EncodeToString(token[:])
	path := "/" + id + "/video"
	s := &Session{Descriptor: Descriptor{ID: id, URL: "http://" + ln.Addr().String() + path}, cancel: cancel, done: make(chan struct{})}
	src := newSource(size, chunk)
	if mime == "" || strings.ContainsAny(mime, "\r\n") {
		mime = "application/octet-stream"
	}
	active := make(chan struct{}, 4)
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
		// net/http's default panic logger prints request paths. Keep capabilities
		// out of logs even on unexpected handler failure.
		ErrorLog:    log.New(io.Discard, "", 0),
		BaseContext: func(net.Listener) context.Context { return ctx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path || r.URL.RawQuery != "" {
				http.NotFound(w, r)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			// Players use single ranges. Reject multipart requests rather than
			// allocating multipart state for an arbitrary number of ranges.
			if h := r.Header.Get("Range"); len(h) > 256 || strings.Contains(h, ",") {
				w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			select {
			case active <- struct{}{}:
				defer func() { <-active }()
			default:
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			http.ServeContent(w, r, "video", time.Time{}, io.NewSectionReader(requestReader{r.Context(), src}, 0, size))
		}),
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(boundedListener{ln})
		cancel()
	}()
	go func() {
		defer close(s.done)
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
		_ = ln.Close()
		<-served
		src.cache.Purge()
	}()
	return s, nil
}

// Close waits for shutdown; it is safe to call concurrently or repeatedly.
func (s *Session) Close() {
	s.cancel()
	<-s.done
}

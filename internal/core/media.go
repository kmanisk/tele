package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/sorokin-vladimir/tele/internal/core/state"
	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/mediacache"
	"github.com/sorokin-vladimir/tele/internal/telerr"
	internaltg "github.com/sorokin-vladimir/tele/internal/tg"
)

// refreshCooldown is how long a refreshed file reference is trusted. A second
// expiry inside this window means refreshing did not help, so the next fetch
// fails instead of asking Telegram again. A variable so tests can shorten it.
var refreshCooldown = time.Minute

// mediaFetcher downloads media on behalf of clients. It is the only place a
// file reference is handled: a client names a message slot, this resolves the
// reference, streams the file to disk, refreshes an expired reference once, and
// answers with a path. Decoding stays with the client, because decoding serves
// rendering (#196).
type mediaFetcher struct {
	client internaltg.Client
	state  *state.State
	log    *zap.Logger

	// cache is nil until the app supplies one. A fetch without a cache has
	// nowhere to put the file and fails rather than inventing a location.
	cache *mediacache.Cache

	// inflight collapses concurrent fetches of one key into a single download.
	// Every repaint asks for the same thumbnails, so without this a slow
	// download starts again on each pass.
	inflight singleflight.Group

	// refreshedAt remembers when each key's file reference was last refreshed,
	// so a reference that keeps expiring costs one round trip rather than one
	// per repaint. Entries older than the cooldown are dropped as they are
	// passed, which keeps the map the size of what is currently failing.
	refreshMu   sync.Mutex
	refreshed   map[string]time.Time
	refreshGate chan struct{}
}

// watch is who is watching one save, and under which ref. The zero watch is
// nobody: a save is watched only when a client asked to see it move.
type watch struct {
	ref    string
	report func(DownloadProgress)
}

func newMediaFetcher(client internaltg.Client, st *state.State, log *zap.Logger) *mediaFetcher {
	return &mediaFetcher{client: client, state: st, log: log, refreshed: make(map[string]time.Time), refreshGate: make(chan struct{}, 1)}
}

// claimRefresh reports whether key may be refreshed now, and records the
// attempt when it may. A refresh inside the cooldown is refused: the reference
// it produced expired again, so another one buys nothing.
func (f *mediaFetcher) claimRefresh(key string) bool {
	f.refreshMu.Lock()
	defer f.refreshMu.Unlock()
	now := time.Now()
	for k, at := range f.refreshed {
		if now.Sub(at) >= refreshCooldown {
			delete(f.refreshed, k)
		}
	}
	if at, ok := f.refreshed[key]; ok && now.Sub(at) < refreshCooldown {
		return false
	}
	f.refreshed[key] = now
	return true
}

// mediaRef is a resolved location: exactly one of photo or doc is meaningful,
// picked by slot.
type mediaRef struct {
	slot  domain.MediaSlot
	photo domain.PhotoRef
	doc   domain.DocumentRef
	kind  domain.MediaKind
}

// resolveMediaRef turns a stored message and a slot into a file location. A slot
// the message has no media for is NotFound: the client asked for something that
// is not there, which is a mistake, not an empty answer.
func resolveMediaRef(msg domain.Message, slot domain.MediaSlot) (mediaRef, error) {
	var kind domain.MediaKind
	if msg.Media != nil {
		kind = msg.Media.Kind
	}
	switch slot {
	case domain.PhotoThumb:
		if msg.Photo == nil {
			return mediaRef{}, &telerr.Error{Kind: telerr.NotFound, Op: "fetch media", Detail: "message has no photo"}
		}
		return mediaRef{slot: slot, photo: *msg.Photo, kind: kind}, nil
	case domain.PhotoFull:
		if msg.Photo == nil {
			return mediaRef{}, &telerr.Error{Kind: telerr.NotFound, Op: "fetch media", Detail: "message has no photo"}
		}
		r := *msg.Photo
		if r.FullThumbSize != "" {
			r.ThumbSize = r.FullThumbSize
		}
		return mediaRef{slot: slot, photo: r, kind: kind}, nil
	case domain.DocThumb:
		if msg.Document == nil || msg.Document.ThumbSize == "" {
			return mediaRef{}, &telerr.Error{Kind: telerr.NotFound, Op: "fetch media", Detail: "message has no document thumbnail"}
		}
		return mediaRef{slot: slot, doc: *msg.Document, kind: kind}, nil
	case domain.DocFull:
		if msg.Document == nil {
			return mediaRef{}, &telerr.Error{Kind: telerr.NotFound, Op: "fetch media", Detail: "message has no document"}
		}
		return mediaRef{slot: slot, doc: *msg.Document, kind: kind}, nil
	}
	return mediaRef{}, &telerr.Error{Kind: telerr.NotFound, Op: "fetch media", Detail: "unknown media slot"}
}

// cacheKey names the file in the cache. Filename-safe by construction: every
// part is a number or a Telegram size letter.
func (r mediaRef) cacheKey() string {
	switch r.slot {
	case domain.PhotoThumb, domain.PhotoFull:
		if r.photo.ThumbSize == "" {
			return "photo_" + strconv.FormatInt(r.photo.ID, 10)
		}
		return "photo_" + strconv.FormatInt(r.photo.ID, 10) + "_" + r.photo.ThumbSize
	case domain.DocThumb:
		return "doc_" + strconv.FormatInt(r.doc.ID, 10) + "_thumb_" + r.doc.ThumbSize
	}
	return "doc_" + strconv.FormatInt(r.doc.ID, 10)
}

// stream writes the located file into dst.
func (f *mediaFetcher) stream(ctx context.Context, r mediaRef, dst io.Writer) error {
	switch r.slot {
	case domain.PhotoThumb, domain.PhotoFull:
		return f.client.DownloadPhotoToFile(ctx, r.photo, dst)
	case domain.DocThumb:
		return f.client.DownloadDocumentThumbToFile(ctx, r.doc, dst)
	}
	return f.client.DownloadDocumentToFile(ctx, r.doc, dst)
}

// streamInto writes the media named by (chatID, msgID, slot) into file. On an
// expired file reference it refreshes the message once, records the fresh
// reference in state, rewinds the file and retries. The rewind matters: a
// partial first attempt would otherwise be prefixed to the retry's bytes.
func (f *mediaFetcher) streamInto(ctx context.Context, chatID int64, msgID int, slot domain.MediaSlot, file *os.File, w watch) error {
	msg, err := messageByID(f.state, chatID, msgID)
	if err != nil {
		return err
	}
	ref, err := resolveMediaRef(msg, slot)
	if err != nil {
		return err
	}

	err = f.attempt(ctx, ref, file, w)
	if telerr.Of(err) != telerr.StaleReference {
		return err
	}

	freshRef, rerr := f.refreshReference(ctx, chatID, msgID, ref)
	if rerr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if telerr.Of(rerr) == telerr.NotFound || telerr.Of(rerr) == telerr.PeerNotFound {
			return rerr
		}
		return err
	}
	if err := f.attempt(ctx, freshRef, file, w); err != nil {
		f.log.Warn("media: download failed after refreshing the file reference",
			zap.Int64("chat_id", chatID), zap.Int("msg_id", msgID), zap.String("slot", slot.String()), zap.Error(err))
		return err
	}
	return nil
}

// refreshReference is shared by saved downloads and ranged playback. Waiters
// reuse a reference already refreshed by another caller before the cooldown is
// checked. The gate is cancellable and prevents simultaneous refresh storms.
func (f *mediaFetcher) refreshReference(ctx context.Context, chatID int64, msgID int, old mediaRef) (mediaRef, error) {
	select {
	case f.refreshGate <- struct{}{}:
	case <-ctx.Done():
		return mediaRef{}, ctx.Err()
	}
	defer func() { <-f.refreshGate }()
	msg, err := messageByID(f.state, chatID, msgID)
	if err != nil {
		return mediaRef{}, err
	}
	current, err := resolveMediaRef(msg, old.slot)
	if err != nil {
		return mediaRef{}, err
	}
	fileReference := func(r mediaRef) []byte {
		if r.slot == domain.PhotoThumb || r.slot == domain.PhotoFull {
			return r.photo.FileReference
		}
		return r.doc.FileReference
	}
	if !bytes.Equal(fileReference(current), fileReference(old)) {
		return current, nil
	}
	where := []zap.Field{zap.Int64("chat_id", chatID), zap.Int("msg_id", msgID), zap.String("slot", old.slot.String())}
	if !f.claimRefresh(old.cacheKey()) {
		f.log.Warn("media: reference expired again within the refresh cooldown, giving up", where...)
		return mediaRef{}, &telerr.Error{Kind: telerr.StaleReference}
	}
	chat, ok := f.state.Store().GetChat(chatID)
	if !ok {
		return mediaRef{}, &telerr.Error{Kind: telerr.PeerNotFound}
	}
	fresh, err := f.client.RefreshMessage(ctx, chat.Peer, msgID)
	if err != nil {
		f.log.Warn("media: could not refresh an expired file reference", append(where, zap.Error(err))...)
		return mediaRef{}, err
	}
	f.state.ApplyMediaRef(chatID, msgID, fresh.Photo, fresh.Document)
	f.log.Debug("media: refreshed an expired file reference", where...)
	ref, err := resolveMediaRef(fresh, old.slot)
	if err != nil {
		f.log.Warn("media: the refreshed message no longer carries this media", append(where, zap.Error(err))...)
	}
	return ref, err
}

// attempt rewinds file and streams one download into it. A watched download
// counts from zero again on a retry, because the file does.
func (f *mediaFetcher) attempt(ctx context.Context, ref mediaRef, file *os.File, w watch) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	var dst io.Writer = file
	if total := ref.size(); w.ref != "" && total > 0 && w.report != nil {
		dst = &progressWriter{w: file, ref: w.ref, total: total, pct: -1, report: w.report}
	}
	return f.stream(ctx, ref, dst)
}

// size is the file's length in bytes, or 0 when Telegram did not say. Only a
// whole document carries one: a photo size and a thumbnail are not given a
// length in what is stored.
func (r mediaRef) size() int64 {
	if r.slot == domain.DocFull {
		return r.doc.Size
	}
	return 0
}

// progressWriter counts what passes through it and reports each time the whole
// percentage moves: the downloader writes in chunks of half a megabyte, and a
// frame per chunk would be traffic with no visible difference (#204).
type progressWriter struct {
	w      io.Writer
	ref    string
	done   int64
	total  int64
	pct    int64
	report func(DownloadProgress)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	// Capped, so a file longer than announced reads as finished rather than
	// past it.
	if pct := min(p.done*100/p.total, 100); pct != p.pct {
		p.pct = pct
		p.report(DownloadProgress{Ref: p.ref, Done: p.done, Total: p.total})
	}
	return n, err
}

// Fetch returns a path to the media, downloading it into the cache on a miss.
//
// A fetch crosses four layers (client -> owner -> cache -> tg) and shows
// nothing at all when it fails, because the client renders no placeholder for
// media it does not have. Each step therefore says what it did, so "the preview
// did not appear" can be traced to the layer that gave up (#196).
func (f *mediaFetcher) Fetch(ctx context.Context, chatID int64, msgID int, slot domain.MediaSlot) (string, error) {
	if f.cache == nil {
		return "", &telerr.Error{Kind: telerr.Internal, Op: "fetch media", Detail: "no media cache"}
	}
	key, err := f.key(chatID, msgID, slot)
	if err != nil {
		f.log.Debug("media: fetch could not resolve the reference",
			zap.Int64("chat_id", chatID), zap.Int("msg_id", msgID),
			zap.String("slot", slot.String()), zap.Error(err))
		return "", err
	}
	if p, ok := f.cache.Path(key); ok {
		f.log.Debug("media: fetch served from the cache", zap.String("key", key))
		return p, nil
	}
	path, err, _ := f.inflight.Do(key, func() (any, error) {
		// Another caller may have finished while this one waited.
		if p, ok := f.cache.Path(key); ok {
			return p, nil
		}
		f.log.Debug("media: fetch downloading",
			zap.String("key", key), zap.String("slot", slot.String()))
		return f.cache.Put(key, func(file *os.File) error {
			return f.streamInto(ctx, chatID, msgID, slot, file, watch{})
		})
	})
	if err != nil {
		f.log.Debug("media: fetch failed", zap.String("key", key), zap.Error(err))
		return "", err
	}
	f.log.Debug("media: fetch done", zap.String("key", key), zap.String("path", path.(string)))
	return path.(string), nil
}

// Save streams the media into destDir under a name derived from the media
// itself, bypassing the cache: what the user saved must not be evicted, and a
// large file must not push thumbnails out of a budget sized for thumbnails.
// A non-empty ref with report asks for progress, reported to report alone.
func (f *mediaFetcher) Save(ctx context.Context, chatID int64, msgID int, slot domain.MediaSlot, destDir, progressRef string, report func(DownloadProgress)) (string, error) {
	w := watch{ref: progressRef, report: report}
	msg, err := messageByID(f.state, chatID, msgID)
	if err != nil {
		return "", err
	}
	ref, err := resolveMediaRef(msg, slot)
	if err != nil {
		return "", err
	}
	file, err := createUniqueFile(destDir, savedFileName(ref))
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := f.streamInto(ctx, chatID, msgID, slot, file, w); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// Invalidate drops a cached entry. A client calls it when the bytes turn out to
// be undecodable, so the next fetch downloads the file again instead of
// returning the same broken entry forever.
func (f *mediaFetcher) Invalidate(chatID int64, msgID int, slot domain.MediaSlot) {
	if f.cache == nil {
		return
	}
	if key, err := f.key(chatID, msgID, slot); err == nil {
		// The client could not decode these bytes. That is invisible on screen —
		// the media simply never appears — so say it here.
		f.log.Debug("media: dropped an entry the client could not decode", zap.String("key", key))
		f.cache.Remove(key)
	}
}

func (f *mediaFetcher) key(chatID int64, msgID int, slot domain.MediaSlot) (string, error) {
	msg, err := messageByID(f.state, chatID, msgID)
	if err != nil {
		return "", err
	}
	ref, err := resolveMediaRef(msg, slot)
	if err != nil {
		return "", err
	}
	return ref.cacheKey(), nil
}

// savedFileName is the on-disk name for saved media: a document's own name when
// it has one, otherwise "<prefix>_<id><ext>" with the prefix reflecting the
// media kind and the extension derived from the MIME type. Telegram photos,
// voice messages and round notes often carry no file name.
func savedFileName(r mediaRef) string {
	if r.slot == domain.PhotoThumb || r.slot == domain.PhotoFull {
		return "photo_" + strconv.FormatInt(r.photo.ID, 10) + ".jpg"
	}
	if r.doc.FileName != "" {
		return r.doc.FileName
	}
	prefix := "file"
	switch r.kind {
	case domain.MediaVideo:
		prefix = "video"
	case domain.MediaVideoNote:
		prefix = "video_note"
	case domain.MediaVoice:
		prefix = "voice"
	case domain.MediaAudio:
		prefix = "audio"
	case domain.MediaGIF:
		prefix = "gif"
	}
	return prefix + "_" + strconv.FormatInt(r.doc.ID, 10) + extFromMime(r.doc.MimeType, r.kind)
}

// extFromMime derives a file extension from a MIME type via the mimetype
// library rather than a hand-rolled table, so the OS opens saved media in the
// right application. An unknown type yields no extension, because a wrong one
// is worse than none — except for video, which is opened in an external player
// that picks the application by extension, and where .mp4 is the usual Telegram
// container.
func extFromMime(mimeType string, kind domain.MediaKind) string {
	if mt := mimetype.Lookup(mimeType); mt != nil {
		return mt.Extension()
	}
	if kind.IsVideo() || kind == domain.MediaGIF {
		return ".mp4"
	}
	return ""
}

// createUniqueFile creates a new file in dir under name's base name, resolving
// collisions as "name (1).ext", "name (2).ext" and so on. It uses O_EXCL so the
// name is claimed atomically: no overwrite, no TOCTOU race. The caller owns the
// returned file and must Close it.
func createUniqueFile(dir, name string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	base := filepath.Base(filepath.Clean(name))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "file"
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; ; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, candidate), os.O_CREATE|os.O_RDWR|os.O_EXCL, 0644)
		if err == nil {
			return f, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
	}
}

package ui

import (
	"hash/fnv"
	"image"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"go.uber.org/zap"

	"github.com/sorokin-vladimir/tele/internal/ui/media"
)

// Inline sixel painting.
//
// The message list stays block art, which keeps its height accounting and its
// layout exactly as they were. Each art row carries an invisible marker (see
// media.InlineMarker) naming the image and the geometry it was drawn at, so the
// finished frame alone tells us where every inline image sits and how much of
// it is on screen.
//
// foot erases the covered part of a sixel whenever those cells are reprinted,
// and every scroll, selection change or pane update does reprint them - the
// renderer diffs whole lines, so a change anywhere in a line rewrites the cells
// of the chat pane beside it. So the paint is not a one-off: it runs after the
// frames that change the cells under an image, and only those. A hash of the
// lines under each image is what tells the two cases apart - identical lines
// mean the renderer never rewrote them, so the graphic is still there and
// repainting it would only cost time and bandwidth.

// inlinePaintDelay is when the inline paint runs after the event that dirtied
// it: late enough that the frame carrying the change has been written, early
// enough to be invisible.
const inlinePaintDelay = 30 * time.Millisecond

// inlineSeqCacheSize bounds the encoded sequences kept between repaints. A
// scrolled chat repaints the same crops over and over, and encoding one is the
// expensive part; the sequences themselves are a few hundred kilobytes at most.
const inlineSeqCacheSize = 24

// inlineSixelState is shared by every copy of the model (RootModel is a value
// type) and holds what the last frame looked like and what was painted over it.
type inlineSixelState struct {
	mu      sync.Mutex
	frame   []string
	painted map[string]uint64
	seqs    map[string]string
	order   []string
}

func newInlineSixelState() *inlineSixelState {
	return &inlineSixelState{
		painted: map[string]uint64{},
		seqs:    map[string]string{},
	}
}

// record stores the frame that is about to be drawn. Called from View.
func (s *inlineSixelState) record(lines []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.frame = lines
	s.mu.Unlock()
}

// forget drops the painted bookkeeping: the cells under the images were
// rewritten wholesale (an overlay covered them, the screen changed), so the
// next paint must not assume the graphics survived.
func (s *inlineSixelState) forget() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.painted = map[string]uint64{}
	s.mu.Unlock()
}

// seq returns a cached encoding for one crop.
func (s *inlineSixelState) seq(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := s.seqs[key]
	return seq, ok
}

// putSeq caches an encoding, evicting the oldest entries past the cap.
func (s *inlineSixelState) putSeq(key, seq string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seqs[key]; !ok {
		s.order = append(s.order, key)
	}
	s.seqs[key] = seq
	for len(s.order) > inlineSeqCacheSize {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.seqs, old)
	}
}

// inlineRegion is one inline image found in the frame, already clipped to the
// rows that are visible.
type inlineRegion struct {
	ref      media.InlineRef
	row, col int // 1-based screen cell of the first visible row
	skipTop  int
	visible  int
	key      string // identifies this placement on screen
	seqKey   string // identifies the encoded crop (independent of position)
	hash     uint64
}

// inlineSixelBlocked reports whether something is drawn over the chat. While it
// is, painting underneath would put pixels on top of the overlay, so inline
// images wait until the chat is back.
func (m RootModel) inlineSixelBlocked() bool {
	return m.screen == ScreenLogin ||
		m.photoViewer != nil || m.videoPlayer != nil ||
		m.help != nil || m.settings != nil || m.profile != nil ||
		m.searchModel != nil || m.filePicker != nil || m.logOut != nil ||
		m.confirmQuit != nil || m.openPicker != nil || m.contextMenu != nil ||
		m.chatMenu != nil || m.reactionPicker != nil || m.mentionPopup != nil ||
		m.noticeActive()
}

// inlineMarkerHit is one marker found in the frame.
type inlineMarkerHit struct {
	ref      media.InlineRef
	row, col int // 0-based frame row, 0-based cell column
}

// inlineRegions extracts the inline images from a frame, one region per image
// (or per mosaic tile), clipped to the rows the frame actually shows.
func inlineRegions(lines []string) []inlineRegion {
	groups := map[string][]inlineMarkerHit{}
	for i, line := range lines {
		from := 0
		for {
			ref, start, next, ok := media.ParseInlineMarker(line, from)
			if next == 0 {
				break
			}
			from = next
			if !ok {
				continue
			}
			col := xansi.StringWidth(line[:start])
			// The same image can be on screen twice with identical geometry (two
			// bubbles holding the same photo), so the column is part of the group
			// key along with the unclipped top row to separate stacked copies.
			key := inlineRefKey(ref) + "@" + strconv.Itoa(col) + "/" + strconv.Itoa(i-ref.Row)
			groups[key] = append(groups[key], inlineMarkerHit{ref: ref, row: i, col: col})
		}
	}
	return buildInlineRegions(groups, lines)
}

// inlineRefKey identifies one inline image placement's geometry: the same image
// can be on screen twice at different sizes (a mosaic tile and a preview), and
// each needs its own paint.
func inlineRefKey(ref media.InlineRef) string {
	return strconv.FormatInt(ref.ID, 10) + "/" +
		strconv.Itoa(ref.CoverCols) + "/" + strconv.Itoa(ref.CoverRows) + "/" +
		strconv.Itoa(ref.HOff) + "/" + strconv.Itoa(ref.VOff) + "/" +
		strconv.Itoa(ref.WinCols) + "/" + strconv.Itoa(ref.WinRows)
}

// buildInlineRegions turns the markers of one image into the single run of rows
// that is actually on screen. A viewport cuts images at both ends, so the run
// starts at the first marker present and continues while both the art row and
// the frame row advance by one.
func buildInlineRegions(groups map[string][]inlineMarkerHit, lines []string) []inlineRegion {
	var out []inlineRegion
	for groupKey, hits := range groups {
		sort.Slice(hits, func(a, b int) bool { return hits[a].ref.Row < hits[b].ref.Row })
		first := hits[0]
		visible := 0
		for _, h := range hits {
			if h.ref.Row != first.ref.Row+visible || h.row != first.row+visible || h.col != first.col {
				break
			}
			visible++
		}
		if visible == 0 {
			continue
		}
		out = append(out, inlineRegion{
			ref:     first.ref,
			row:     first.row + 1,
			col:     first.col + 1,
			skipTop: first.ref.Row,
			visible: visible,
			key:     groupKey + "/" + strconv.Itoa(first.row),
			seqKey:  inlineRefKey(first.ref) + "#" + strconv.Itoa(first.ref.Row) + "/" + strconv.Itoa(visible),
			hash:    hashFrameLines(lines, first.row, visible),
		})
	}
	return out
}

// hashFrameLines hashes the frame rows one image covers, so an image whose
// cells were not rewritten is not repainted.
func hashFrameLines(lines []string, from, n int) uint64 {
	h := fnv.New64a()
	for i := from; i < from+n && i < len(lines); i++ {
		_, _ = h.Write([]byte(lines[i]))
		_, _ = h.Write([]byte{'\n'})
	}
	return h.Sum64()
}

// scheduleInlineSixelPaint asks for a paint after the frame this event will
// produce. Other modes draw their images in the cells themselves and need none.
func (m RootModel) scheduleInlineSixelPaint() tea.Cmd {
	if m.imageMode != media.ModeSixel || m.sixelInline == nil {
		return nil
	}
	return tea.Tick(inlinePaintDelay, func(time.Time) tea.Msg { return inlineSixelTickMsg{} })
}

// inlinePaintOp is one image to encode and paint.
type inlinePaintOp struct {
	region inlineRegion
	img    image.Image
	seq    string // set when the crop was already encoded
}

// inlinePaintOps collects the images that need repainting. It runs on the
// update goroutine: the image cache is not safe for concurrent use.
func (m *RootModel) inlinePaintOps() []inlinePaintOp {
	if m.imageMode != media.ModeSixel || m.sixelInline == nil {
		return nil
	}
	if m.inlineSixelBlocked() {
		m.sixelInline.forget()
		return nil
	}
	m.sixelInline.mu.Lock()
	lines := m.sixelInline.frame
	painted := m.sixelInline.painted
	m.sixelInline.mu.Unlock()
	if len(lines) == 0 {
		return nil
	}

	var ops []inlinePaintOp
	seen := map[string]bool{}
	for _, r := range inlineRegions(lines) {
		seen[r.key] = true
		if h, ok := painted[r.key]; ok && h == r.hash {
			continue
		}
		img, ok := m.imageCache.Peek(r.ref.ID)
		if !ok {
			continue
		}
		op := inlinePaintOp{region: r, img: img}
		if seq, ok := m.sixelInline.seq(r.seqKey); ok {
			op.seq = seq
		}
		ops = append(ops, op)
		painted[r.key] = r.hash
	}
	// Drop bookkeeping for images that scrolled away, so returning to them
	// repaints instead of trusting a hash of cells that were since rewritten.
	for k := range painted {
		if !seen[k] {
			delete(painted, k)
		}
	}
	return ops
}

// handleInlineSixelTick paints whatever the last frame showed and left dirty.
func (m RootModel) handleInlineSixelTick() (RootModel, tea.Cmd) {
	ops := (&m).inlinePaintOps()
	if len(ops) == 0 {
		return m, nil
	}
	state := m.sixelInline
	return m, func() tea.Msg {
		started := time.Now()
		var sb strings.Builder
		images := 0
		for _, op := range ops {
			seq := op.seq
			if seq == "" {
				encoded, err := media.SixelRegionSeq(op.img, op.region.ref, op.region.skipTop, op.region.visible)
				if err != nil || encoded == "" {
					continue
				}
				seq = encoded
				state.putSeq(op.region.seqKey, seq)
			}
			sb.WriteString(media.PositionSixel(op.region.row, op.region.col, seq))
			images++
		}
		if images == 0 {
			return nil
		}
		m.debug("inline sixel painted", zap.Int("images", images), zap.Int("bytes", sb.Len()), zap.Duration("took", time.Since(started)))
		return tea.Raw(sb.String())()
	}
}

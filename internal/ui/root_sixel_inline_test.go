package ui

import (
	"image"
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/ui/media"
)

// markedRow is one frame line carrying the marker of image id at art row row.
func markedRow(id int64, coverCols, coverRows, winCols, winRows, row int, text string) string {
	return media.InlineMarker(media.InlineRef{
		ID: id, CoverCols: coverCols, CoverRows: coverRows,
		WinCols: winCols, WinRows: winRows, Row: row,
	}) + text
}

func TestInlineRegionsClipTopAndBottom(t *testing.T) {
	// A six-row image of which rows 2..5 are on screen, starting at frame row 10.
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "row"
	}
	for i := 2; i < 6; i++ {
		lines = append(lines, markedRow(7, 10, 6, 10, 6, i, "          ▄▄▄▄"))
	}
	regions := inlineRegions(lines)
	require.Len(t, regions, 1)
	r := regions[0]
	assert.Equal(t, int64(7), r.ref.ID)
	assert.Equal(t, 11, r.row, "1-based frame row of the first visible art row")
	assert.Equal(t, 1, r.col, "marker at the start of the line")
	assert.Equal(t, 2, r.skipTop, "two art rows scrolled off the top")
	assert.Equal(t, 4, r.visible)
}

func TestInlineRegionsUsesMarkerColumn(t *testing.T) {
	lines := []string{
		"│ pane │" + markedRow(3, 4, 2, 4, 2, 0, "▄▄▄▄"),
		"│ pane │" + markedRow(3, 4, 2, 4, 2, 1, "▄▄▄▄"),
	}
	regions := inlineRegions(lines)
	require.Len(t, regions, 1)
	assert.Equal(t, 9, regions[0].col, "1-based column after the 8-cell prefix")
	assert.Equal(t, 2, regions[0].visible)
}

func TestInlineRegionsSameImageTwice(t *testing.T) {
	// The same photo in two bubbles with identical geometry must paint twice.
	lines := []string{
		markedRow(5, 4, 2, 4, 2, 0, "▄▄▄▄") + "   " + markedRow(5, 4, 2, 4, 2, 0, "▄▄▄▄"),
		markedRow(5, 4, 2, 4, 2, 1, "▄▄▄▄") + "   " + markedRow(5, 4, 2, 4, 2, 1, "▄▄▄▄"),
	}
	regions := inlineRegions(lines)
	require.Len(t, regions, 2)
	assert.NotEqual(t, regions[0].key, regions[1].key, "placements are keyed apart")
}

func TestInlineRegionsSameImageStacked(t *testing.T) {
	lines := []string{
		markedRow(5, 4, 2, 4, 2, 0, "art"),
		markedRow(5, 4, 2, 4, 2, 1, "art"),
		"gap",
		markedRow(5, 4, 2, 4, 2, 0, "art"),
		markedRow(5, 4, 2, 4, 2, 1, "art"),
	}
	regions := inlineRegions(lines)
	require.Len(t, regions, 2)
	assert.NotEqual(t, regions[0].key, regions[1].key)
	for _, region := range regions {
		assert.Equal(t, 2, region.visible)
	}
}

func TestInlineRegionsStopAtGap(t *testing.T) {
	lines := []string{
		markedRow(9, 4, 4, 4, 4, 0, "▄▄▄▄"),
		"unrelated",
		markedRow(9, 4, 4, 4, 4, 2, "▄▄▄▄"),
	}
	regions := inlineRegions(lines)
	require.Len(t, regions, 1)
	assert.Equal(t, 1, regions[0].visible, "a gap ends the visible run")
}

func TestInlinePlainFrameHasNoRegions(t *testing.T) {
	assert.Empty(t, inlineRegions([]string{"just", "text", "│ box │"}))
}

// inlineModel is a main-screen model in sixel mode with image id cached.
func inlineModel(t *testing.T) RootModel {
	t.Helper()
	m := newSizedModel(t)
	m.screen = ScreenMain
	m.imageMode = media.ModeSixel
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: uint8(y * 6), B: 90, A: 255})
		}
	}
	m.imageCache.Add(11, img)
	return m
}

func inlineFrame() []string {
	return []string{
		"header",
		markedRow(11, 8, 4, 8, 4, 0, "        ▄▄▄▄▄▄▄▄"),
		markedRow(11, 8, 4, 8, 4, 1, "        ▄▄▄▄▄▄▄▄"),
		markedRow(11, 8, 4, 8, 4, 2, "        ▄▄▄▄▄▄▄▄"),
		markedRow(11, 8, 4, 8, 4, 3, "        ▄▄▄▄▄▄▄▄"),
		"footer",
	}
}

func TestInlineTickPaintsThenSkipsUnchangedFrame(t *testing.T) {
	m := inlineModel(t)
	m.sixelInline.record(inlineFrame())

	next, cmd := m.handleInlineSixelTick()
	require.NotNil(t, cmd, "first tick paints")
	msg := cmd()
	require.NotNil(t, msg, "paint yields a raw write")
	raw, ok := msg.(tea.RawMsg)
	require.Truef(t, ok, "expected a raw write, got %T", msg)
	payload, _ := raw.Msg.(string)
	assert.Contains(t, payload, "\x1bP", "the payload is a sixel")
	assert.Contains(t, payload, "\x1b[2;1H", "positioned at the first art row")

	_, again := next.handleInlineSixelTick()
	assert.Nil(t, again, "an unchanged frame is not repainted")
}

func TestInlineTickRepaintsChangedFrame(t *testing.T) {
	m := inlineModel(t)
	m.sixelInline.record(inlineFrame())
	next, cmd := m.handleInlineSixelTick()
	require.NotNil(t, cmd)
	cmd()

	// A selection change rewrites the bubble border on the image rows.
	changed := inlineFrame()
	changed[1] = "\x1b[1m" + changed[1]
	next.sixelInline.record(changed)
	_, cmd = next.handleInlineSixelTick()
	assert.NotNil(t, cmd, "a rewritten frame repaints")
}

func TestInlineTickSkipsWithoutCachedImage(t *testing.T) {
	m := inlineModel(t)
	m.sixelInline.record([]string{
		markedRow(999, 8, 4, 8, 4, 0, "▄▄▄"),
	})
	_, cmd := m.handleInlineSixelTick()
	assert.Nil(t, cmd, "an uncached image is not painted")
}

func TestInlineTickBlocksUnderOverlay(t *testing.T) {
	m := inlineModel(t)
	m.sixelInline.record(inlineFrame())
	m.photoViewer = &photoViewer{cols: 10, rows: 10}

	_, cmd := m.handleInlineSixelTick()
	assert.Nil(t, cmd, "no inline paint under the photo modal")

	m2 := m
	m2.photoViewer = nil
	next, cmd := m2.handleInlineSixelTick()
	assert.NotNil(t, cmd, "the chat repaints once the modal closes")
	_ = next
}

func TestInlineSeqCacheReusesEncoding(t *testing.T) {
	m := inlineModel(t)
	m.sixelInline.record(inlineFrame())
	next, cmd := m.handleInlineSixelTick()
	require.NotNil(t, cmd)
	cmd()

	region := inlineRegions(inlineFrame())[0]
	cached, ok := next.sixelInline.seq(region.seqKey)
	require.True(t, ok, "the encoding is cached")
	assert.Contains(t, cached, "\x1bP")
}

func TestInlineCellSizeChangeDropsPixelEncodingCache(t *testing.T) {
	t.Cleanup(func() { media.SetCellPx(0, 0) })
	media.SetCellPx(8, 16)
	m := inlineModel(t)
	m.sixelInline.putSeq("crop", "old pixels")
	next, _ := m.updateUIMsg(uv.CellSizeEvent{Width: 10, Height: 20})
	_, cached := next.sixelInline.seq("crop")
	assert.False(t, cached)
}

func TestInlineScheduleOnlyInSixelMode(t *testing.T) {
	m := inlineModel(t)
	assert.NotNil(t, m.scheduleInlineSixelPaint())

	m.imageMode = media.ModeBlocks
	assert.Nil(t, m.scheduleInlineSixelPaint())
}

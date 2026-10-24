package tg

import (
	"context"
	"io"
	"testing"

	"github.com/gotd/td/bin"
	gotdtg "github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/domain"
	"github.com/sorokin-vladimir/tele/internal/videostream"
)

type chunkInvoker struct {
	t      *testing.T
	offset int64
	length int
	wait   bool
}

func (i chunkInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	r, ok := input.(*gotdtg.UploadGetFileRequest)
	require.True(i.t, ok)
	require.Equal(i.t, i.offset, r.Offset)
	require.Equal(i.t, videostream.ChunkSize, r.Limit)
	require.True(i.t, r.Precise)
	require.False(i.t, r.CDNSupported)
	loc := r.Location.(*gotdtg.InputDocumentFileLocation)
	require.Equal(i.t, int64(7), loc.ID)
	require.Equal(i.t, []byte("reference"), loc.FileReference)
	if i.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	output.(*gotdtg.UploadFileBox).File = &gotdtg.UploadFile{Bytes: make([]byte, i.length)}
	return nil
}

func TestReadDocumentChunkRequestsOnlyAlignedRange(t *testing.T) {
	for _, final := range []bool{false, true} {
		c := testClient()
		offset := int64(videostream.ChunkSize * 400)
		length := videostream.ChunkSize
		if final {
			length = 31
		}
		c.api = gotdtg.NewClient(chunkInvoker{t: t, offset: offset, length: length})
		ref := domain.DocumentRef{ID: 7, FileReference: []byte("reference"), Size: offset + int64(length) + 1}
		if final {
			ref.Size--
		}
		n, err := c.ReadDocumentChunk(context.Background(), ref, offset, make([]byte, videostream.ChunkSize))
		require.Equal(t, length, n)
		if final {
			require.ErrorIs(t, err, io.EOF)
		} else {
			require.NoError(t, err)
		}
	}
}

func TestReadDocumentChunkCancellation(t *testing.T) {
	c := testClient()
	c.api = gotdtg.NewClient(chunkInvoker{t: t, wait: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ReadDocumentChunk(ctx, domain.DocumentRef{ID: 7, FileReference: []byte("reference"), Size: 1}, 0, make([]byte, videostream.ChunkSize))
	require.ErrorIs(t, err, context.Canceled)
}

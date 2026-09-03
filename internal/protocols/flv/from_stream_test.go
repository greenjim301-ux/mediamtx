package flv

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// syncBuffer is a bytes.Buffer that can be written from one goroutine
// (the stream reader) and read from another (the test) concurrently.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// splitTags parses a buffer containing a FLV header followed by tags,
// and returns the data section of each tag.
func splitTags(t *testing.T, buf []byte) [][]byte {
	require.GreaterOrEqual(t, len(buf), 13)
	require.Equal(t, []byte("FLV"), buf[:3])

	buf = buf[13:] // header + PreviousTagSize0

	var tags [][]byte

	for len(buf) > 0 {
		require.GreaterOrEqual(t, len(buf), 11)
		require.Equal(t, byte(9), buf[0]) // video tag

		size := int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		require.GreaterOrEqual(t, len(buf), 11+size+4)

		data := buf[11 : 11+size]
		tags = append(tags, data)

		prevTagSize := uint32(buf[11+size])<<24 | uint32(buf[11+size+1])<<16 |
			uint32(buf[11+size+2])<<8 | uint32(buf[11+size+3])
		require.Equal(t, uint32(11+size), prevTagSize)

		buf = buf[11+size+4:]
	}

	return tags
}

func TestFromStreamH264(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{test.FormatH264},
	}}

	strm := &stream.Stream{
		OrigDesc:          &description.Session{Medias: medias},
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	err := strm.Initialize()
	require.NoError(t, err)

	subStream := &stream.SubStream{Stream: strm}
	err = subStream.Initialize()
	require.NoError(t, err)

	buf := &syncBuffer{}
	w := &Writer{W: buf}
	err = w.WriteHeader()
	require.NoError(t, err)

	r := &stream.Reader{Parent: test.NilLogger}
	err = FromStream(strm.OrigDesc, r, w)
	require.NoError(t, err)

	strm.AddReader(r)
	defer strm.RemoveReader(r)

	// sent before any IDR: must be dropped, since there is no GOP cache
	subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH264{{0x61, 0x00}}, // non-IDR
	})

	require.Never(t, func() bool {
		return len(buf.Bytes()) > 13
	}, 200*time.Millisecond, 20*time.Millisecond)

	subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
		PTS:     90000,
		Payload: unit.PayloadH264{{0x65, 0x00}}, // IDR
	})

	var tags [][]byte
	require.Eventually(t, func() bool {
		tags = splitTags(t, buf.Bytes())
		return len(tags) == 2
	}, 3*time.Second, 10*time.Millisecond)

	// sequence header
	require.Equal(t, byte(0x17), tags[0][0]) // key frame, codec H264
	require.Equal(t, byte(0), tags[0][1])    // AVC sequence header

	// AU
	require.Equal(t, byte(0x17), tags[1][0]) // key frame, codec H264
	require.Equal(t, byte(1), tags[1][1])    // AVC NALU
}

func TestFromStreamH265(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{test.FormatH265},
	}}

	strm := &stream.Stream{
		OrigDesc:          &description.Session{Medias: medias},
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	err := strm.Initialize()
	require.NoError(t, err)

	subStream := &stream.SubStream{Stream: strm}
	err = subStream.Initialize()
	require.NoError(t, err)

	buf := &syncBuffer{}
	w := &Writer{W: buf}
	err = w.WriteHeader()
	require.NoError(t, err)

	r := &stream.Reader{Parent: test.NilLogger}
	err = FromStream(strm.OrigDesc, r, w)
	require.NoError(t, err)

	strm.AddReader(r)
	defer strm.RemoveReader(r)

	// IDR NALU type for H265 is in range 16-23; use 19 (IDR_W_RADL)
	subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH265{{19 << 1, 0x00}},
	})

	var tags [][]byte
	require.Eventually(t, func() bool {
		tags = splitTags(t, buf.Bytes())
		return len(tags) == 2
	}, 3*time.Second, 10*time.Millisecond)

	// sequence start: isExHeader=1, packetType=0
	require.Equal(t, byte(0x80), tags[0][0])
	require.Equal(t, "hvc1", string(tags[0][1:5]))

	// coded frames: isExHeader=1, frameType=key, packetType=coded frames
	require.Equal(t, byte(0x80|(1<<4)|1), tags[1][0])
	require.Equal(t, "hvc1", string(tags[1][1:5]))
}

func TestFromStreamNoSupportedCodecs(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{test.FormatMPEG4Audio},
	}}

	r := &stream.Reader{Parent: test.NilLogger}
	err := FromStream(&description.Session{Medias: medias}, r, &Writer{W: &bytes.Buffer{}})
	require.Equal(t, errNoSupportedCodecsFrom, err)
}

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

type flvTag struct {
	typ  byte
	data []byte
}

// splitTags parses a buffer containing a FLV header followed by tags,
// and returns the header flags and the tags.
func splitTags(t *testing.T, buf []byte) (byte, []flvTag) {
	require.GreaterOrEqual(t, len(buf), 13)
	require.Equal(t, []byte("FLV"), buf[:3])

	flags := buf[4]
	buf = buf[13:] // header + PreviousTagSize0

	var tags []flvTag

	for len(buf) > 0 {
		require.GreaterOrEqual(t, len(buf), 11)
		require.Contains(t, []byte{tagTypeAudio, tagTypeVideo}, buf[0])

		size := int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		require.GreaterOrEqual(t, len(buf), 11+size+4)

		tags = append(tags, flvTag{typ: buf[0], data: buf[11 : 11+size]})

		prevTagSize := uint32(buf[11+size])<<24 | uint32(buf[11+size+1])<<16 |
			uint32(buf[11+size+2])<<8 | uint32(buf[11+size+3])
		require.Equal(t, uint32(11+size), prevTagSize)

		buf = buf[11+size+4:]
	}

	return flags, tags
}

// initStream creates a stream and attaches a FLV writer to it.
func initStream(t *testing.T, medias []*description.Media) (*stream.SubStream, *syncBuffer, func()) {
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

	r := &stream.Reader{Parent: test.NilLogger}
	err = FromStream(strm.OrigDesc, r, w)
	require.NoError(t, err)

	err = w.WriteHeader()
	require.NoError(t, err)

	strm.AddReader(r)

	return subStream, buf, func() { strm.RemoveReader(r) }
}

// waitTags waits until the given number of tags has been written.
func waitTags(t *testing.T, buf *syncBuffer, count int) (byte, []flvTag) {
	var flags byte
	var tags []flvTag

	require.Eventually(t, func() bool {
		flags, tags = splitTags(t, buf.Bytes())
		return len(tags) == count
	}, 3*time.Second, 10*time.Millisecond)

	return flags, tags
}

func TestFromStreamH264(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{test.FormatH264},
	}}

	subStream, buf, cleanup := initStream(t, medias)
	defer cleanup()

	// non-IDR unit before any IDR: must be dropped, since there is no GOP cache
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

	flags, tags := waitTags(t, buf, 2)

	require.Equal(t, byte(0x01), flags) // video only

	// sequence header
	require.Equal(t, byte(tagTypeVideo), tags[0].typ)
	require.Equal(t, byte(0x17), tags[0].data[0]) // key frame, codec H264
	require.Equal(t, byte(0), tags[0].data[1])    // AVC sequence header

	// AU
	require.Equal(t, byte(tagTypeVideo), tags[1].typ)
	require.Equal(t, byte(0x17), tags[1].data[0]) // key frame, codec H264
	require.Equal(t, byte(1), tags[1].data[1])    // AVC NALU
}

func TestFromStreamH265(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{test.FormatH265},
	}}

	subStream, buf, cleanup := initStream(t, medias)
	defer cleanup()

	// IDR NALU type for H265 is in range 16-23; use 19 (IDR_W_RADL)
	subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH265{{19 << 1, 0x00}},
	})

	flags, tags := waitTags(t, buf, 2)

	require.Equal(t, byte(0x01), flags) // video only

	// sequence start: isExHeader=1, packetType=0
	require.Equal(t, byte(0x80), tags[0].data[0])
	require.Equal(t, "hvc1", string(tags[0].data[1:5]))

	// coded frames: isExHeader=1, frameType=key, packetType=coded frames
	require.Equal(t, byte(0x80|(1<<4)|1), tags[1].data[0])
	require.Equal(t, "hvc1", string(tags[1].data[1:5]))
}

func TestFromStreamH264AndMPEG4Audio(t *testing.T) {
	medias := []*description.Media{
		{Formats: []format.Format{test.FormatH264}},
		{Formats: []format.Format{test.FormatMPEG4Audio}},
	}

	subStream, buf, cleanup := initStream(t, medias)
	defer cleanup()

	// audio before the first video key frame: must be dropped
	subStream.WriteUnit(medias[1], medias[1].Formats[0], &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadMPEG4Audio{{0x01, 0x02}},
	})

	require.Never(t, func() bool {
		return len(buf.Bytes()) > 13
	}, 200*time.Millisecond, 20*time.Millisecond)

	subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
		PTS:     0,
		Payload: unit.PayloadH264{{0x65, 0x00}}, // IDR
	})

	subStream.WriteUnit(medias[1], medias[1].Formats[0], &unit.Unit{
		PTS:     44100,
		Payload: unit.PayloadMPEG4Audio{{0x01, 0x02}, {0x03, 0x04}},
	})

	flags, tags := waitTags(t, buf, 5)

	require.Equal(t, byte(0x05), flags) // audio + video

	// video sequence header + AU
	require.Equal(t, byte(tagTypeVideo), tags[0].typ)
	require.Equal(t, byte(tagTypeVideo), tags[1].typ)

	// AAC sequence header
	require.Equal(t, byte(tagTypeAudio), tags[2].typ)
	require.Equal(t, byte(0xAF), tags[2].data[0])
	require.Equal(t, byte(0), tags[2].data[1])

	// AAC access units
	for _, tag := range tags[3:] {
		require.Equal(t, byte(tagTypeAudio), tag.typ)
		require.Equal(t, byte(0xAF), tag.data[0])
		require.Equal(t, byte(1), tag.data[1])
	}
	require.Equal(t, []byte{0x01, 0x02}, tags[3].data[2:])
	require.Equal(t, []byte{0x03, 0x04}, tags[4].data[2:])
}

func TestFromStreamG711(t *testing.T) {
	for _, ca := range []struct {
		name     string
		forma    *format.G711
		expected byte
	}{
		{
			"pcma",
			&format.G711{PayloadTyp: 8, MULaw: false, SampleRate: 8000, ChannelCount: 1},
			0x72, // codec 7, rate 5512, depth 16, mono
		},
		{
			"pcmu",
			&format.G711{PayloadTyp: 0, MULaw: true, SampleRate: 8000, ChannelCount: 1},
			0x82, // codec 8, rate 5512, depth 16, mono
		},
		{
			"pcma stereo",
			&format.G711{PayloadTyp: 96, MULaw: false, SampleRate: 8000, ChannelCount: 2},
			0x73, // codec 7, rate 5512, depth 16, stereo
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			medias := []*description.Media{{
				Formats: []format.Format{ca.forma},
			}}

			subStream, buf, cleanup := initStream(t, medias)
			defer cleanup()

			// there's no video track, so audio is sent immediately
			subStream.WriteUnit(medias[0], medias[0].Formats[0], &unit.Unit{
				PTS:     0,
				Payload: unit.PayloadG711{0x01, 0x02, 0x03, 0x04},
			})

			flags, tags := waitTags(t, buf, 1)

			require.Equal(t, byte(0x04), flags) // audio only

			require.Equal(t, byte(tagTypeAudio), tags[0].typ)
			require.Equal(t, ca.expected, tags[0].data[0])
			require.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, tags[0].data[1:])
		})
	}
}

func TestFromStreamG711UnsupportedSampleRate(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{
			&format.G711{PayloadTyp: 96, MULaw: false, SampleRate: 16000, ChannelCount: 1},
		},
	}}

	r := &stream.Reader{Parent: test.NilLogger}
	err := FromStream(&description.Session{Medias: medias}, r, &Writer{W: &bytes.Buffer{}})
	require.Equal(t, errNoSupportedCodecsFrom, err)
}

func TestFromStreamNoSupportedCodecs(t *testing.T) {
	medias := []*description.Media{{
		Formats: []format.Format{&format.Opus{PayloadTyp: 96, ChannelCount: 2}},
	}}

	r := &stream.Reader{Parent: test.NilLogger}
	err := FromStream(&description.Session{Medias: medias}, r, &Writer{W: &bytes.Buffer{}})
	require.Equal(t, errNoSupportedCodecsFrom, err)
}

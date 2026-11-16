package gb28181

import (
	"bytes"
	"testing"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/mpegps"
	"github.com/bluenviron/mediamtx/internal/stream"
)

type nilParent struct{}

func (nilParent) Log(logger.Level, string, ...any) {}

func (nilParent) SetReady(defs.PathSourceStaticSetReadyReq) defs.PathSourceStaticSetReadyRes {
	return defs.PathSourceStaticSetReadyRes{}
}

func (nilParent) SetNotReady(defs.PathSourceStaticSetNotReadyReq) {}

func packHeader() []byte {
	return []byte{
		0x00, 0x00, 0x01, 0xBA,
		0x44, 0x00, 0x04, 0x00, 0x04, 0x01,
		0x00, 0x89, 0xC3, 0xF8,
	}
}

func programStreamMap(entries ...[2]byte) []byte {
	esMap := make([]byte, 0, 4*len(entries))
	for _, e := range entries {
		esMap = append(esMap, e[0], e[1], 0x00, 0x00)
	}

	body := make([]byte, 0, 10+len(esMap))
	body = append(body, 0xE0, 0xFF, 0x00, 0x00)
	body = append(body, byte(len(esMap)>>8), byte(len(esMap)))
	body = append(body, esMap...)
	body = append(body, 0x00, 0x00, 0x00, 0x00)

	buf := []byte{0x00, 0x00, 0x01, 0xBC}
	buf = append(buf, byte(len(body)>>8), byte(len(body)))
	return append(buf, body...)
}

func pesPacket(streamID byte, pts int64, payload []byte) []byte {
	optional := []byte{
		0x20 | byte((pts>>29)&0x0E) | 0x01,
		byte(pts >> 22),
		byte((pts>>14)&0xFE) | 0x01,
		byte(pts >> 7),
		byte((pts<<1)&0xFE) | 0x01,
	}

	body := make([]byte, 0, 3+len(optional)+len(payload))
	body = append(body, 0x80, 0x80, byte(len(optional)))
	body = append(body, optional...)
	body = append(body, payload...)

	buf := []byte{0x00, 0x00, 0x01, streamID}
	buf = append(buf, byte(len(body)>>8), byte(len(body)))
	return append(buf, body...)
}

// buildStream builds 3 seconds of H264 at 25 fps, with a declared G711 track,
// that is transmitted only when withAudio is true.
func buildStream(withAudio bool) *bytes.Buffer {
	var buf bytes.Buffer

	buf.Write(packHeader())
	buf.Write(programStreamMap(
		[2]byte{mpegps.StreamTypeH264, 0xE0},
		[2]byte{mpegps.StreamTypeG711U, 0xC0}))

	for i := range int64(75) {
		pts := 90000 + i*3600
		buf.Write(packHeader())
		if i == 0 {
			buf.Write(pesPacket(0xE0, pts, []byte{0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x00, 0x00, 0x01, 0x65, 0x01}))
		} else {
			buf.Write(pesPacket(0xE0, pts, []byte{0x00, 0x00, 0x01, 0x41, byte(i)}))
		}
		if withAudio {
			buf.Write(pesPacket(0xC0, pts, bytes.Repeat([]byte{0xFF}, 320)))
		}
	}

	return &buf
}

func TestToStreamSkipsSilentAudioTrack(t *testing.T) {
	r := &mpegps.Reader{R: buildStream(false)}
	require.NoError(t, r.Initialize())

	s := &Source{Parent: nilParent{}}
	var subStream *stream.SubStream

	medias, replay, err := s.toStream(r, &subStream)
	require.NoError(t, err)
	require.NotNil(t, replay)
	require.Len(t, medias, 1)
	require.Equal(t, description.MediaTypeVideo, medias[0].Type)
}

func TestToStreamKeepsAudioTrack(t *testing.T) {
	r := &mpegps.Reader{R: buildStream(true)}
	require.NoError(t, r.Initialize())

	s := &Source{Parent: nilParent{}}
	var subStream *stream.SubStream

	medias, _, err := s.toStream(r, &subStream)
	require.NoError(t, err)
	require.Len(t, medias, 2)
	require.Equal(t, description.MediaTypeVideo, medias[0].Type)
	require.Equal(t, description.MediaTypeAudio, medias[1].Type)
}

func TestProbeTracks(t *testing.T) {
	t.Run("silent audio", func(t *testing.T) {
		r := &mpegps.Reader{R: buildStream(false)}
		require.NoError(t, r.Initialize())

		res, err := probeTracks(r)
		require.NoError(t, err)
		require.Empty(t, res.audioReceived)

		// probing stops after about one second of video, and video is kept.
		require.NotEmpty(t, res.aus)
		require.Less(t, len(res.aus), 40)
		require.Equal(t, int64(90000), res.aus[0].pts)
		require.Equal(t, []byte{0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x00, 0x00, 0x01, 0x65, 0x01}, res.aus[0].au)
	})

	t.Run("audio", func(t *testing.T) {
		r := &mpegps.Reader{R: buildStream(true)}
		require.NoError(t, r.Initialize())

		res, err := probeTracks(r)
		require.NoError(t, err)
		require.Len(t, res.audioReceived, 1)

		// probing stops as soon as audio is received.
		require.Less(t, len(res.aus), 5)
	})
}

func TestParseSource(t *testing.T) {
	id, pb, start, end, err := parseSource("gb28181://35020000001320000002")
	require.NoError(t, err)
	require.Equal(t, "35020000001320000002", id)
	require.False(t, pb)
	require.Zero(t, start)
	require.Zero(t, end)

	id, pb, start, end, err = parseSource("gb28181://35020000001110000001/35020000001320000002?type=playback&start=1794672000&end=1794675600")
	require.NoError(t, err)
	require.Equal(t, "35020000001320000002", id)
	require.True(t, pb)
	require.Equal(t, int64(1794672000), start)
	require.Equal(t, int64(1794675600), end)

	_, _, _, _, err = parseSource("gb28181://35020000001320000002?type=playback&start=1794675600&end=1794672000")
	require.Error(t, err)
}

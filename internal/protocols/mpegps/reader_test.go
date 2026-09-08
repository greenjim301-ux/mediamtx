package mpegps

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// packHeader builds a PS pack header.
func packHeader() []byte {
	return []byte{
		0x00, 0x00, 0x01, 0xBA,
		0x44, 0x00, 0x04, 0x00, 0x04, 0x01, // SCR
		0x00, 0x89, 0xC3, 0xF8, // mux rate + stuffing length (0)
	}
}

// programStreamMap builds a PSM with the given (streamType, streamID) pairs.
func programStreamMap(entries ...[2]byte) []byte {
	esMap := make([]byte, 0, 4*len(entries))
	for _, e := range entries {
		esMap = append(esMap, e[0], e[1], 0x00, 0x00)
	}

	body := make([]byte, 0, 10+len(esMap))
	body = append(body, 0xE0, 0xFF, 0x00, 0x00) // current_next + reserved + program_stream_info_length
	body = append(body, byte(len(esMap)>>8), byte(len(esMap)))
	body = append(body, esMap...)
	body = append(body, 0x00, 0x00, 0x00, 0x00) // CRC32

	buf := []byte{0x00, 0x00, 0x01, 0xBC}
	buf = append(buf, byte(len(body)>>8), byte(len(body)))
	return append(buf, body...)
}

// pesPacket builds a PES packet.
func pesPacket(streamID byte, pts int64, dts int64, payload []byte) []byte {
	var optional []byte
	var flags byte

	switch {
	case pts >= 0 && dts >= 0 && pts != dts:
		flags = 0xC0
		optional = append(optional, encodeTimestamp(0x03, pts)...)
		optional = append(optional, encodeTimestamp(0x01, dts)...)

	case pts >= 0:
		flags = 0x80
		optional = append(optional, encodeTimestamp(0x02, pts)...)
	}

	body := make([]byte, 0, 3+len(optional)+len(payload))
	body = append(body, 0x80, flags, byte(len(optional)))
	body = append(body, optional...)
	body = append(body, payload...)

	buf := []byte{0x00, 0x00, 0x01, streamID}
	buf = append(buf, byte(len(body)>>8), byte(len(body)))
	return append(buf, body...)
}

// pesPacketUnbounded builds a PES packet with length zero.
func pesPacketUnbounded(streamID byte, pts int64, payload []byte) []byte {
	optional := encodeTimestamp(0x02, pts)

	body := make([]byte, 0, 3+len(optional)+len(payload))
	body = append(body, 0x80, 0x80, byte(len(optional)))
	body = append(body, optional...)
	body = append(body, payload...)

	buf := make([]byte, 0, 6+len(body))
	buf = append(buf, 0x00, 0x00, 0x01, streamID, 0x00, 0x00)
	return append(buf, body...)
}

func encodeTimestamp(prefix byte, ts int64) []byte {
	return []byte{
		prefix<<4 | byte((ts>>29)&0x0E) | 0x01,
		byte(ts >> 22),
		byte((ts>>14)&0xFE) | 0x01,
		byte(ts >> 7),
		byte((ts<<1)&0xFE) | 0x01,
	}
}

func TestDecodeTimestamp(t *testing.T) {
	for _, ts := range []int64{0, 90000, 1 << 20, (1 << 33) - 1} {
		enc := encodeTimestamp(0x02, ts)
		require.Equal(t, ts, decodeTimestamp(enc))
	}
}

func TestReaderH264AndG711(t *testing.T) {
	var buf bytes.Buffer

	buf.Write(packHeader())
	buf.Write(programStreamMap([2]byte{StreamTypeH264, 0xE0}, [2]byte{StreamTypeG711A, 0xC0}))

	// first video access unit, split into two PES packets
	buf.Write(pesPacket(0xE0, 90000, 90000, []byte{0x00, 0x00, 0x01, 0x67, 0x01, 0x02}))
	buf.Write(pesPacket(0xE0, -1, -1, []byte{0x00, 0x00, 0x01, 0x65, 0x03, 0x04}))

	buf.Write(pesPacket(0xC0, 90000, -1, []byte{0x10, 0x11, 0x12}))

	// second video access unit
	buf.Write(packHeader())
	buf.Write(pesPacket(0xE0, 93600, 93600, []byte{0x00, 0x00, 0x01, 0x61, 0x05}))

	// third one, needed to flush the second
	buf.Write(pesPacket(0xE0, 97200, 97200, []byte{0x00, 0x00, 0x01, 0x61, 0x06}))

	// second audio access unit, needed to flush the first
	buf.Write(pesPacket(0xC0, 90720, -1, []byte{0x13, 0x14}))

	r := &Reader{R: &buf}
	err := r.Initialize()
	require.NoError(t, err)

	tracks := r.Tracks()
	require.Equal(t, 2, len(tracks))
	require.Equal(t, byte(StreamTypeH264), tracks[0].StreamType)
	require.Equal(t, byte(0xE0), tracks[0].StreamID)
	require.True(t, tracks[0].IsVideo())
	require.Equal(t, byte(StreamTypeG711A), tracks[1].StreamType)
	require.False(t, tracks[1].IsVideo())

	type au struct {
		pts  int64
		dts  int64
		data []byte
	}

	var videoAUs []au
	var audioAUs []au

	r.OnData(tracks[0], func(pts int64, dts int64, data []byte) error {
		videoAUs = append(videoAUs, au{pts, dts, data})
		return nil
	})

	r.OnData(tracks[1], func(pts int64, dts int64, data []byte) error {
		audioAUs = append(audioAUs, au{pts, dts, data})
		return nil
	})

	for {
		err = r.Read()
		if err != nil {
			break
		}
	}

	require.Equal(t, []au{
		{90000, 90000, []byte{
			0x00, 0x00, 0x01, 0x67, 0x01, 0x02,
			0x00, 0x00, 0x01, 0x65, 0x03, 0x04,
		}},
		{93600, 93600, []byte{0x00, 0x00, 0x01, 0x61, 0x05}},
	}, videoAUs)

	require.Equal(t, []au{
		{90000, 90000, []byte{0x10, 0x11, 0x12}},
	}, audioAUs)
}

func TestReaderH265DetectionWithoutPSM(t *testing.T) {
	var buf bytes.Buffer

	buf.Write(packHeader())

	// VPS (type 32) of H265
	buf.Write(pesPacket(0xE0, 90000, 90000, []byte{0x00, 0x00, 0x01, 0x40, 0x01, 0x0c}))
	buf.Write(pesPacket(0xE0, 93600, 93600, []byte{0x00, 0x00, 0x01, 0x26, 0x01}))

	r := &Reader{R: &buf}
	err := r.Initialize()
	require.NoError(t, err)

	tracks := r.Tracks()
	require.Equal(t, 1, len(tracks))
	require.Equal(t, byte(StreamTypeH265), tracks[0].StreamType)
}

func TestReaderUnboundedPES(t *testing.T) {
	var buf bytes.Buffer

	buf.Write(packHeader())
	buf.Write(programStreamMap([2]byte{StreamTypeH264, 0xE0}))
	buf.Write(pesPacketUnbounded(0xE0, 90000, []byte{0x00, 0x00, 0x01, 0x65, 0x01, 0x02}))
	buf.Write(pesPacket(0xE0, 93600, 93600, []byte{0x00, 0x00, 0x01, 0x61, 0x03}))
	buf.Write(pesPacket(0xE0, 97200, 97200, []byte{0x00, 0x00, 0x01, 0x61, 0x04}))

	r := &Reader{R: &buf}
	err := r.Initialize()
	require.NoError(t, err)

	var aus [][]byte

	r.OnData(r.Tracks()[0], func(_ int64, _ int64, data []byte) error {
		aus = append(aus, data)
		return nil
	})

	for {
		err = r.Read()
		if err != nil {
			break
		}
	}

	require.Equal(t, [][]byte{
		{0x00, 0x00, 0x01, 0x65, 0x01, 0x02},
		{0x00, 0x00, 0x01, 0x61, 0x03},
	}, aus)
}

func TestReaderResyncAfterPacketLoss(t *testing.T) {
	var buf bytes.Buffer

	buf.Write(packHeader())
	buf.Write(programStreamMap([2]byte{StreamTypeH264, 0xE0}))

	// truncated packet, as it happens when UDP packets are lost
	trunc := pesPacket(0xE0, 90000, 90000, bytes.Repeat([]byte{0xAA}, 100))
	buf.Write(trunc[:20])

	// the corrupted packet declares a size bigger than its content, so the
	// reader consumes part of the packets that follow, and has to resynchronize.
	for i := range 10 {
		buf.Write(pesPacket(0xE0, int64(93600+3600*i), int64(93600+3600*i),
			[]byte{0x00, 0x00, 0x01, 0x61, byte(i)}))
	}

	r := &Reader{R: &buf}
	err := r.Initialize()
	require.NoError(t, err)

	r.OnDecodeError(func(_ error) {})

	var aus [][]byte
	r.OnData(r.Tracks()[0], func(_ int64, _ int64, data []byte) error {
		aus = append(aus, data)
		return nil
	})

	for {
		err = r.Read()
		if err != nil {
			break
		}
	}

	// the reader must recover and decode the packets that follow the corrupted one.
	// the first access unit contains the remains of the corrupted packet,
	// while the following ones are intact.
	require.Greater(t, len(aus), 1)

	for _, au := range aus[1:] {
		require.Equal(t, []byte{0x00, 0x00, 0x01, 0x61}, au[:4])
	}
}

func TestTrackString(t *testing.T) {
	require.Equal(t, "H264", (&Track{StreamType: StreamTypeH264}).String())
	require.Equal(t, "H265", (&Track{StreamType: StreamTypeH265}).String())
	require.Equal(t, "MPEG-4 Audio", (&Track{StreamType: StreamTypeAAC}).String())
	require.Equal(t, "G711 A-law", (&Track{StreamType: StreamTypeG711A}).String())
	require.Equal(t, "G711 mu-law", (&Track{StreamType: StreamTypeG711U}).String())
	require.Equal(t, "unknown (stream type 0x99)", (&Track{StreamType: 0x99}).String())
}

func TestPESLengthEncoding(t *testing.T) {
	// verify that the helper produces a well-formed length
	pkt := pesPacket(0xE0, 0, -1, []byte{0x01, 0x02, 0x03})
	require.Equal(t, uint16(len(pkt)-6), binary.BigEndian.Uint16(pkt[4:6]))
}

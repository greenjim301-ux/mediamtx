// Package mpegps contains a MPEG-PS (program stream) demuxer,
// used to decode the media streams of GB28181 devices.
package mpegps

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// start codes.
const (
	startCodePackHeader   = 0x000001BA
	startCodeSystemHeader = 0x000001BB
	startCodeProgramMap   = 0x000001BC
	startCodeProgramEnd   = 0x000001B9
)

// stream types, as defined by GB/T 28181 and ISO/IEC 13818-1.
const (
	StreamTypeH264  = 0x1B
	StreamTypeH265  = 0x24
	StreamTypeAAC   = 0x0F
	StreamTypeG711A = 0x90
	StreamTypeG711U = 0x91
	StreamTypeSVACV = 0x80
	StreamTypeSVACA = 0x9B
)

const (
	// maximum size of an access unit. It prevents memory exhaustion
	// in case of corrupted streams.
	maxAccessUnitSize = 8 * 1024 * 1024

	// maximum number of packets read while looking for tracks.
	maxProbePackets = 2048
)

// isSystemStartCodeID returns whether a start code belongs to the program stream
// layer. Start codes of elementary streams (H264 and H265 NAL units) always have
// a lower ID, since their forbidden_zero_bit is zero.
func isSystemStartCodeID(id byte) bool {
	return id >= 0xB9
}

func isVideoStreamID(id byte) bool {
	return id >= 0xE0 && id <= 0xEF
}

func isAudioStreamID(id byte) bool {
	return id >= 0xC0 && id <= 0xDF
}

// Track is a track of a MPEG-PS stream.
type Track struct {
	// StreamType is the ISO/IEC 13818-1 stream type.
	StreamType byte
	// StreamID is the PES stream ID.
	StreamID byte

	pendingAU  []byte
	pendingPTS int64
	pendingDTS int64
	hasPending bool
	cb         func(pts int64, dts int64, au []byte) error
}

// IsVideo returns whether the track is a video track.
func (t *Track) IsVideo() bool {
	return isVideoStreamID(t.StreamID)
}

// String returns a description of the track.
func (t *Track) String() string {
	switch t.StreamType {
	case StreamTypeH264:
		return "H264"
	case StreamTypeH265:
		return "H265"
	case StreamTypeAAC:
		return "MPEG-4 Audio"
	case StreamTypeG711A:
		return "G711 A-law"
	case StreamTypeG711U:
		return "G711 mu-law"
	}
	return fmt.Sprintf("unknown (stream type 0x%.2x)", t.StreamType)
}

// Reader reads a MPEG-PS stream.
type Reader struct {
	// R is the underlying reader.
	R io.Reader

	br               *bufio.Reader
	tracks           []*Track
	onDecodeError    func(err error)
	pendingStartCode uint32
}

// Initialize initializes a Reader, reading the stream until tracks are found.
func (r *Reader) Initialize() error {
	r.br = bufio.NewReaderSize(r.R, 32*1024)
	r.onDecodeError = func(_ error) {}

	// read the stream until the program stream map (or a recognizable
	// video track) is found.
	for range maxProbePackets {
		err := r.readPacket(true)
		if err != nil {
			return err
		}

		if len(r.tracks) != 0 {
			return nil
		}
	}

	return fmt.Errorf("no tracks found")
}

// Tracks returns the detected tracks.
func (r *Reader) Tracks() []*Track {
	return r.tracks
}

// OnDecodeError sets a callback that is called when a non-fatal decode error occurs.
func (r *Reader) OnDecodeError(cb func(err error)) {
	r.onDecodeError = cb
}

// OnData sets a callback that is called when an access unit of the given track is available.
// Timestamps are expressed in the 90khz clock of MPEG-PS.
func (r *Reader) OnData(track *Track, cb func(pts int64, dts int64, au []byte) error) {
	track.cb = cb
}

// Read reads data from the stream.
func (r *Reader) Read() error {
	return r.readPacket(false)
}

func (r *Reader) track(streamID byte, streamType byte) *Track {
	for _, track := range r.tracks {
		if track.StreamID == streamID {
			// the program stream map can arrive after the first PES packets,
			// and provides the definitive stream type.
			if streamType != 0 && track.StreamType != streamType {
				track.StreamType = streamType
			}
			return track
		}
	}

	if streamType == 0 {
		return nil
	}

	track := &Track{
		StreamType: streamType,
		StreamID:   streamID,
	}
	r.tracks = append(r.tracks, track)

	return track
}

// readPacket reads a single PS packet.
// When probing, PES payloads are used to detect tracks and then discarded.
func (r *Reader) readPacket(probing bool) error {
	code, err := r.readStartCode()
	if err != nil {
		return err
	}

	switch {
	case code == startCodePackHeader:
		return r.readPackHeader()

	case code == startCodeProgramMap:
		payload, err2 := r.readPayload()
		if err2 != nil {
			return err2
		}

		err2 = r.readProgramStreamMap(payload)
		if err2 != nil {
			r.onDecodeError(err2)
		}
		return nil

	case code == startCodeProgramEnd:
		return nil

	case code == startCodeSystemHeader:
		_, err = r.readPayload()
		return err

	case isVideoStreamID(byte(code)) || isAudioStreamID(byte(code)):
		payload, err2 := r.readPayload()
		if err2 != nil {
			return err2
		}

		return r.readPES(byte(code), payload, probing)

	default:
		// any other start code is followed by a 16-bit length.
		_, err = r.readPayload()
		return err
	}
}

func (r *Reader) readStartCode() (uint32, error) {
	if r.pendingStartCode != 0 {
		code := r.pendingStartCode
		r.pendingStartCode = 0
		return code, nil
	}

	// resynchronize on the next start code prefix (0x000001).
	// this is needed since UDP streams can lose packets.
	var zeroes int

	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return 0, err
		}

		switch {
		case b == 0:
			zeroes++

		case b == 1 && zeroes >= 2:
			id, err2 := r.br.ReadByte()
			if err2 != nil {
				return 0, err2
			}

			if isSystemStartCodeID(id) {
				return 0x00000100 | uint32(id), nil
			}

			// elementary stream data: keep looking.
			zeroes = 0

		default:
			zeroes = 0
		}
	}
}

func (r *Reader) readPackHeader() error {
	// system clock reference and mux rate.
	_, err := r.br.Discard(9)
	if err != nil {
		return err
	}

	b, err := r.br.ReadByte()
	if err != nil {
		return err
	}

	// pack_stuffing_length
	_, err = r.br.Discard(int(b & 0x07))
	return err
}

func (r *Reader) readPayload() ([]byte, error) {
	buf := make([]byte, 2)

	_, err := io.ReadFull(r.br, buf)
	if err != nil {
		return nil, err
	}

	le := binary.BigEndian.Uint16(buf)
	if le == 0 {
		// unbounded packet: it ends at the next start code.
		return r.readUntilStartCode()
	}

	payload := make([]byte, le)

	_, err = io.ReadFull(r.br, payload)
	if err != nil {
		return nil, err
	}

	return payload, nil
}

// readUntilStartCode reads until the next start code, that is stored
// and returned by the next call to readStartCode().
func (r *Reader) readUntilStartCode() ([]byte, error) {
	var payload []byte
	var zeroes int

	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return nil, err
		}

		switch {
		case b == 0:
			zeroes++
			payload = append(payload, b)

		case b == 1 && zeroes >= 2:
			id, err2 := r.br.ReadByte()
			if err2 != nil {
				return nil, err2
			}

			if isSystemStartCodeID(id) {
				r.pendingStartCode = 0x00000100 | uint32(id)

				// remove the trailing zeroes of the start code prefix.
				return payload[:len(payload)-2], nil
			}

			// elementary stream data: it belongs to the payload.
			payload = append(payload, b, id)
			zeroes = 0

		default:
			zeroes = 0
			payload = append(payload, b)
		}

		if len(payload) > maxAccessUnitSize {
			return nil, fmt.Errorf("packet size exceeds maximum allowed size")
		}
	}
}

func (r *Reader) readProgramStreamMap(buf []byte) error {
	if len(buf) < 4 {
		return fmt.Errorf("invalid program stream map")
	}

	pos := 2

	// program_stream_info_length
	if (pos + 2) > len(buf) {
		return fmt.Errorf("invalid program stream map")
	}
	infoLength := int(binary.BigEndian.Uint16(buf[pos:]))
	pos += 2 + infoLength

	// elementary_stream_map_length
	if (pos + 2) > len(buf) {
		return fmt.Errorf("invalid program stream map")
	}
	mapLength := int(binary.BigEndian.Uint16(buf[pos:]))
	pos += 2

	end := pos + mapLength
	if end > len(buf) {
		return fmt.Errorf("invalid program stream map")
	}

	for (pos + 4) <= end {
		streamType := buf[pos]
		streamID := buf[pos+1]
		esInfoLength := int(binary.BigEndian.Uint16(buf[pos+2:]))
		pos += 4 + esInfoLength

		if isVideoStreamID(streamID) || isAudioStreamID(streamID) {
			r.track(streamID, streamType)
		}
	}

	return nil
}

func (r *Reader) readPES(streamID byte, buf []byte, probing bool) error {
	pts, dts, payload, err := decodePESHeader(buf)
	if err != nil {
		r.onDecodeError(err)
		return nil
	}

	streamType := byte(0)

	track := r.track(streamID, 0)
	if track == nil {
		// the program stream map hasn't been received yet:
		// try to detect the codec from the payload itself.
		if isVideoStreamID(streamID) {
			streamType = detectVideoStreamType(payload)
		}
		if streamType == 0 {
			return nil
		}
		track = r.track(streamID, streamType)
	}

	if probing {
		return nil
	}

	return track.push(pts, dts, payload, r.onDecodeError)
}

func (t *Track) push(pts int64, dts int64, payload []byte, onDecodeError func(error)) error {
	// a new timestamp means that a new access unit is starting:
	// flush the pending one.
	if pts >= 0 && t.hasPending && pts != t.pendingPTS {
		err := t.flush()
		if err != nil {
			return err
		}
	}

	if !t.hasPending {
		if pts < 0 {
			// discard data until the first timestamp is received.
			return nil
		}
		t.hasPending = true
		t.pendingPTS = pts
		t.pendingDTS = dts
	}

	if (len(t.pendingAU) + len(payload)) > maxAccessUnitSize {
		t.pendingAU = nil
		t.hasPending = false
		onDecodeError(fmt.Errorf("access unit size exceeds maximum allowed size"))
		return nil
	}

	t.pendingAU = append(t.pendingAU, payload...)

	return nil
}

func (t *Track) flush() error {
	au := t.pendingAU
	pts := t.pendingPTS
	dts := t.pendingDTS

	t.pendingAU = nil
	t.hasPending = false

	if len(au) == 0 || t.cb == nil {
		return nil
	}

	return t.cb(pts, dts, au)
}

// decodePESHeader decodes a PES header, returning timestamps and payload.
// Timestamps are negative when not present.
func decodePESHeader(buf []byte) (int64, int64, []byte, error) {
	if len(buf) < 3 {
		return 0, 0, nil, fmt.Errorf("invalid PES header")
	}

	if (buf[0] & 0xC0) != 0x80 {
		return 0, 0, nil, fmt.Errorf("invalid PES header")
	}

	ptsDTSFlags := buf[1] >> 6
	headerLength := int(buf[2])

	if (3 + headerLength) > len(buf) {
		return 0, 0, nil, fmt.Errorf("invalid PES header")
	}

	optional := buf[3 : 3+headerLength]
	payload := buf[3+headerLength:]

	pts := int64(-1)
	dts := int64(-1)

	switch ptsDTSFlags {
	case 0x02:
		if len(optional) < 5 {
			return 0, 0, nil, fmt.Errorf("invalid PES header")
		}
		pts = decodeTimestamp(optional[0:5])
		dts = pts

	case 0x03:
		if len(optional) < 10 {
			return 0, 0, nil, fmt.Errorf("invalid PES header")
		}
		pts = decodeTimestamp(optional[0:5])
		dts = decodeTimestamp(optional[5:10])
	}

	return pts, dts, payload, nil
}

func decodeTimestamp(buf []byte) int64 {
	return int64(buf[0]&0x0E)<<29 |
		int64(buf[1])<<22 |
		int64(buf[2]&0xFE)<<14 |
		int64(buf[3])<<7 |
		int64(buf[4])>>1
}

// detectVideoStreamType detects H264 or H265 by inspecting NAL units,
// for devices that don't send a program stream map.
func detectVideoStreamType(payload []byte) byte {
	pos := 0

	for {
		// find the next start code.
		i := indexStartCode(payload[pos:])
		if i < 0 {
			return 0
		}

		pos += i + 3
		if pos >= len(payload) {
			return 0
		}

		b := payload[pos]

		// H265 NAL units have a 2-byte header with the forbidden_zero_bit
		// followed by a 6-bit type; VPS (32), SPS (33), PPS (34) are unique to H265.
		h265Type := (b >> 1) & 0x3F
		if (b & 0x81) == 0 {
			switch h265Type {
			case 32, 33, 34:
				return StreamTypeH265
			}
		}

		// H264 SPS (7) and PPS (8).
		if (b & 0x80) == 0 {
			switch b & 0x1F {
			case 7, 8:
				return StreamTypeH264
			}
		}
	}
}

func indexStartCode(buf []byte) int {
	for i := 0; (i + 2) < len(buf); i++ {
		if buf[i] == 0 && buf[i+1] == 0 && buf[i+2] == 1 {
			return i
		}
	}
	return -1
}

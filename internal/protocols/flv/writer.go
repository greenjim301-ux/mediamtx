// Package flv provides FLV multiplexing utilities, used to implement HTTP-FLV.
package flv

import (
	"encoding/binary"
	"io"
	"time"
)

const (
	tagTypeAudio = 8
	tagTypeVideo = 9
)

// Writer writes a FLV stream to W.
type Writer struct {
	W io.Writer

	// filled by FromStream(), that must be called before WriteHeader().
	hasVideo bool
	hasAudio bool
}

// WriteHeader writes the FLV file header.
func (w *Writer) WriteHeader() error {
	var flags byte
	if w.hasVideo {
		flags |= 0x01
	}
	if w.hasAudio {
		flags |= 0x04
	}

	_, err := w.W.Write([]byte{
		'F', 'L', 'V',
		1,          // version
		flags,      // presence of audio and video
		0, 0, 0, 9, // header size
		0, 0, 0, 0, // PreviousTagSize0
	})
	return err
}

func (w *Writer) writeVideoTag(dts time.Duration, data []byte) error {
	return w.writeTag(tagTypeVideo, dts, data)
}

func (w *Writer) writeAudioTag(dts time.Duration, data []byte) error {
	return w.writeTag(tagTypeAudio, dts, data)
}

// writeTag writes a tag (header, data and PreviousTagSize) to W.
func (w *Writer) writeTag(tagType byte, dts time.Duration, data []byte) error {
	ms := uint32(dts.Milliseconds())
	size := uint32(len(data))

	buf := make([]byte, 11+len(data)+4)

	buf[0] = tagType
	buf[1] = byte(size >> 16)
	buf[2] = byte(size >> 8)
	buf[3] = byte(size)
	buf[4] = byte(ms >> 16)
	buf[5] = byte(ms >> 8)
	buf[6] = byte(ms)
	buf[7] = byte(ms >> 24)
	// buf[8:11] = StreamID, always zero

	copy(buf[11:], data)

	binary.BigEndian.PutUint32(buf[11+len(data):], uint32(11+len(data)))

	_, err := w.W.Write(buf)
	return err
}

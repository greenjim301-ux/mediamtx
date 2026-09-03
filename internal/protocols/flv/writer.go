// Package flv provides FLV multiplexing utilities, used to implement HTTP-FLV.
package flv

import (
	"encoding/binary"
	"io"
	"time"
)

const (
	tagTypeVideo = 9
)

// Writer writes a video-only FLV stream to W.
type Writer struct {
	W io.Writer
}

// WriteHeader writes the FLV file header.
// The stream is always declared as video-only, since audio is not supported.
func (w *Writer) WriteHeader() error {
	_, err := w.W.Write([]byte{
		'F', 'L', 'V',
		1,          // version
		0x01,       // flags: video present, audio absent
		0, 0, 0, 9, // header size
		0, 0, 0, 0, // PreviousTagSize0
	})
	return err
}

// writeTag writes a video tag (header, data and PreviousTagSize) to W.
func (w *Writer) writeTag(dts time.Duration, data []byte) error {
	ms := uint32(dts.Milliseconds())
	size := uint32(len(data))

	buf := make([]byte, 11+len(data)+4)

	buf[0] = tagTypeVideo
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

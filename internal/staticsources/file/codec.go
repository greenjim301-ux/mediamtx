package file

import (
	"encoding/binary"
	"fmt"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
)

// codecConfig decodes avcC / hvcC, also used by Matroska CodecPrivate.
func codecConfig(codec string, b []byte) (format.Format, int, error) {
	invalid := fmt.Errorf("invalid %s decoder configuration", codec)
	if codec == "h264" {
		if len(b) < 7 || b[0] != 1 {
			return nil, 0, invalid
		}
		length := int(b[4]&3) + 1
		pos := 6
		read := func(count int) ([][]byte, error) {
			var out [][]byte
			for range count {
				if pos+2 > len(b) {
					return nil, invalid
				}
				n := int(binary.BigEndian.Uint16(b[pos:]))
				pos += 2
				if n == 0 || pos+n > len(b) {
					return nil, invalid
				}
				out = append(out, append([]byte(nil), b[pos:pos+n]...))
				pos += n
			}
			return out, nil
		}
		sps, err := read(int(b[5] & 31))
		if err != nil || len(sps) == 0 {
			return nil, 0, invalid
		}
		if pos >= len(b) {
			return nil, 0, invalid
		}
		count := int(b[pos])
		pos++
		pps, err := read(count)
		if err != nil || len(pps) == 0 {
			return nil, 0, invalid
		}
		return &format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: sps[0], PPS: pps[0]}, length, nil
	}
	if len(b) < 23 || b[0] != 1 {
		return nil, 0, invalid
	}
	f := &format.H265{PayloadTyp: 96}
	pos := 23
	for range int(b[22]) {
		if pos+3 > len(b) {
			return nil, 0, invalid
		}
		typ := b[pos] & 63
		count := int(binary.BigEndian.Uint16(b[pos+1:]))
		pos += 3
		for range count {
			if pos+2 > len(b) {
				return nil, 0, invalid
			}
			n := int(binary.BigEndian.Uint16(b[pos:]))
			pos += 2
			if n == 0 || pos+n > len(b) {
				return nil, 0, invalid
			}
			nal := append([]byte(nil), b[pos:pos+n]...)
			pos += n
			switch typ {
			case 32:
				f.VPS = nal
			case 33:
				f.SPS = nal
			case 34:
				f.PPS = nal
			}
		}
	}
	if f.VPS == nil || f.SPS == nil || f.PPS == nil {
		return nil, 0, invalid
	}
	return f, int(b[21]&3) + 1, nil
}

package file

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediamtx/internal/unit"
)

type element struct {
	id         uint64
	start, end int64
	unknown    bool
}

func vint(b []byte, id bool) (uint64, int, error) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, fmt.Errorf("invalid EBML integer")
	}
	n := 1
	mask := byte(0x80)
	for b[0]&mask == 0 {
		n++
		mask >>= 1
	}
	if n > len(b) || (id && n > 4) {
		return 0, 0, io.ErrUnexpectedEOF
	}
	val := uint64(b[0])
	if !id {
		val &= uint64(mask - 1)
	}
	for _, v := range b[1:n] {
		val = val<<8 | uint64(v)
	}
	return val, n, nil
}

func readElement(f *os.File, off, end int64) (element, error) {
	var h [16]byte
	n, err := f.ReadAt(h[:], off)
	if err != nil && err != io.EOF {
		return element{}, err
	}
	id, ni, err := vint(h[:n], true)
	if err != nil {
		return element{}, err
	}
	size, ns, err := vint(h[ni:n], false)
	if err != nil {
		return element{}, err
	}
	start := off + int64(ni+ns)
	unknown := size == (uint64(1)<<uint(7*ns))-1
	stop := end
	if !unknown {
		if size > uint64(max(end-start, 0)) {
			return element{}, fmt.Errorf("EBML element outside container")
		}
		stop = start + int64(size)
	}
	if start > end {
		return element{}, io.ErrUnexpectedEOF
	}
	return element{id, start, stop, unknown}, nil
}

func elementBytes(f *os.File, e element) ([]byte, error) {
	if e.end-e.start > maxFrameSize {
		return nil, fmt.Errorf("Matroska element too large")
	}
	b := make([]byte, e.end-e.start)
	_, err := f.ReadAt(b, e.start)
	return b, err
}

func uintEBML(b []byte) uint64 {
	var n uint64
	for _, v := range b {
		n = n<<8 | uint64(v)
	}
	return n
}

// walkElements only visits one level; bulk frame data is never loaded as an index.
func walkElements(ctx context.Context, f *os.File, start, end int64, cb func(element) error) error {
	for off := start; off < end; {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, err := readElement(f, off, end)
		if err != nil {
			return err
		}
		if e.unknown {
			return fmt.Errorf("unknown-size Matroska element %x is unsupported here", e.id)
		}
		if err = cb(e); err != nil {
			return err
		}
		off = e.end
	}
	return nil
}

func levelOne(id uint64) bool {
	switch id {
	case 0x1f43b675, 0x1549a966, 0x1654ae6b, 0x1c53bb6b, 0x114d9b74, 0x1254c367, 0x1043a770, 0x1941a469:
		return true
	}
	return false
}

func readMKV(ctx context.Context, f *os.File, size int64) (*video, error) {
	var segment element
	found := false
	for off := int64(0); off < size; {
		e, err := readElement(f, off, size)
		if err != nil {
			return nil, err
		}
		if e.id == 0x18538067 {
			segment = e
			found = true
			break
		}
		if e.unknown {
			return nil, fmt.Errorf("invalid Matroska header")
		}
		off = e.end
	}
	if !found {
		return nil, fmt.Errorf("Matroska segment not found")
	}
	scale := int64(1_000_000)
	var track uint64
	var defaultDuration time.Duration
	var v *video
	var clusters []element
	for off := segment.start; off < segment.end; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e, err := readElement(f, off, segment.end)
		if err != nil {
			return nil, err
		}
		if e.unknown {
			if e.id != 0x1f43b675 {
				return nil, fmt.Errorf("unsupported unknown-size Matroska element")
			}
			// An unknown-size Cluster terminates at the next Segment-level element.
			for pos := e.start; pos < e.end; {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				next, readErr := readElement(f, pos, e.end)
				if readErr != nil {
					return nil, readErr
				}
				if levelOne(next.id) {
					e.end = pos
					break
				}
				if next.unknown {
					return nil, fmt.Errorf("unknown-size cluster child")
				}
				pos = next.end
			}
		}
		switch e.id {
		case 0x1549a966:
			err = walkElements(ctx, f, e.start, e.end, func(c element) error {
				if c.id != 0x2ad7b1 {
					return nil
				}
				b, er := elementBytes(f, c)
				if er != nil {
					return er
				}
				if len(b) > 8 {
					return fmt.Errorf("invalid timestamp scale")
				}
				scale = int64(uintEBML(b))
				if scale <= 0 || scale > int64(time.Second) {
					return fmt.Errorf("invalid timestamp scale")
				}
				return nil
			})
		case 0x1654ae6b:
			err = walkElements(ctx, f, e.start, e.end, func(c element) error {
				if c.id != 0xae || v != nil {
					return nil
				}
				var num, typ uint64
				var codec string
				var config []byte
				var dur time.Duration
				encoded := false
				er := walkElements(ctx, f, c.start, c.end, func(p element) error {
					switch p.id {
					case 0xd7, 0x83, 0x86, 0x63a2, 0x23e383:
						b, readErr := elementBytes(f, p)
						if readErr != nil {
							return readErr
						}
						switch p.id {
						case 0xd7:
							num = uintEBML(b)
						case 0x83:
							typ = uintEBML(b)
						case 0x86:
							codec = string(b)
						case 0x63a2:
							config = b
						case 0x23e383:
							dur = time.Duration(uintEBML(b))
						}
					case 0x6d80:
						encoded = true
					}
					return nil
				})
				if er != nil {
					return er
				}
				if typ != 1 || (codec != "V_MPEG4/ISO/AVC" && codec != "V_MPEGH/ISO/HEVC") {
					return nil
				}
				if num == 0 || encoded {
					return fmt.Errorf("invalid, compressed or encrypted video track")
				}
				name := "h264"
				if codec == "V_MPEGH/ISO/HEVC" {
					name = "h265"
				}
				forma, length, er := codecConfig(name, config)
				if er != nil {
					return er
				}
				v = &video{format: forma, lengthSize: length}
				track = num
				defaultDuration = dur
				return nil
			})
		case 0x1f43b675:
			clusters = append(clusters, e)
		}
		if err != nil {
			return nil, err
		}
		off = e.end
	}
	if v == nil {
		return nil, fmt.Errorf("file has no H264 or H265 video track")
	}
	for _, cluster := range clusters {
		// Find Timestamp before reading blocks, regardless of child ordering.
		var timestamp int64
		err := walkElements(ctx, f, cluster.start, cluster.end, func(e element) error {
			if e.id != 0xe7 {
				return nil
			}
			b, er := elementBytes(f, e)
			if er != nil {
				return er
			}
			if len(b) > 8 {
				return fmt.Errorf("invalid cluster timestamp")
			}
			timestamp = int64(uintEBML(b))
			return nil
		})
		if err != nil {
			return nil, err
		}
		err = walkElements(ctx, f, cluster.start, cluster.end, func(e element) error {
			if e.id == 0xa3 {
				return mkvBlock(f, e, track, timestamp, scale, defaultDuration, true, false, v)
			}
			if e.id != 0xa0 {
				return nil
			}
			var block *element
			key := true
			dur := defaultDuration
			totalDuration := false
			er := walkElements(ctx, f, e.start, e.end, func(c element) error {
				switch c.id {
				case 0xa1:
					copy := c
					block = &copy
				case 0xfb:
					key = false
				case 0x9b:
					b, re := elementBytes(f, c)
					if re != nil {
						return re
					}
					dur = time.Duration(uintEBML(b)) * time.Duration(scale)
					totalDuration = true
				}
				return nil
			})
			if er != nil {
				return er
			}
			if block == nil {
				return fmt.Errorf("BlockGroup has no Block")
			}
			return mkvBlock(f, *block, track, timestamp, scale, dur, key, totalDuration, v)
		})
		if err != nil {
			return nil, err
		}
	}
	if len(v.samples) == 0 {
		return nil, fmt.Errorf("file contains no video frames")
	}
	// Matroska blocks carry PTS in decode order. Recover DTS from the video bitstream.
	var avc h264.DTSExtractor
	avc.Initialize()
	var hevc h265.DTSExtractor
	hevc.Initialize()
	for i := range v.samples {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sa := &v.samples[i]
		payload, err := v.payload(f, *sa)
		if err != nil {
			return nil, err
		}
		pts := int64(sa.pts/time.Second)*90000 + int64(sa.pts%time.Second)*90000/int64(time.Second)
		var dts int64
		if _, ok := v.format.(*format.H264); ok {
			au := [][]byte(payload.(unit.PayloadH264))
			if i == 0 {
				fo := v.format.(*format.H264)
				au = append([][]byte{fo.SPS, fo.PPS}, au...)
			}
			dts, err = avc.Extract(au, pts)
		} else {
			au := [][]byte(payload.(unit.PayloadH265))
			if i == 0 {
				fo := v.format.(*format.H265)
				au = append([][]byte{fo.VPS, fo.SPS, fo.PPS}, au...)
			}
			dts, err = hevc.Extract(au, pts)
		}
		if err != nil {
			return nil, fmt.Errorf("Matroska decode timestamp: %w", err)
		}
		sa.dts = ticks(dts, 90000)
	}
	for i := range v.samples {
		if i+1 < len(v.samples) {
			v.samples[i].duration = v.samples[i+1].dts - v.samples[i].dts
		}
		if v.samples[i].duration <= 0 && i > 0 {
			v.samples[i].duration = v.samples[i-1].duration
		}
		if v.samples[i].duration <= 0 {
			return nil, fmt.Errorf("cannot determine video frame duration")
		}
	}
	return v, nil
}

func mkvBlock(f *os.File, e element, track uint64, cluster, scale int64, duration time.Duration, key, totalDuration bool, v *video) error {
	// Probe only the header of non-video blocks, so audio is truly ignored.
	var head [11]byte
	n, err := f.ReadAt(head[:], e.start)
	if err != nil && err != io.EOF {
		return err
	}
	num, nv, err := vint(head[:n], false)
	if err != nil {
		return err
	}
	if num != track {
		return nil
	}
	b, err := elementBytes(f, e)
	if err != nil {
		return err
	}
	if len(b) < nv+3 {
		return io.ErrUnexpectedEOF
	}
	rel := int64(int16(binary.BigEndian.Uint16(b[nv:])))
	flags := b[nv+2]
	pos := nv + 3
	if e.id == 0xa3 {
		key = flags&0x80 != 0
	}
	sizes, payloadPos, err := laceSizes(b, pos, (flags>>1)&3)
	if err != nil {
		return err
	}
	if len(sizes) > 1 && duration <= 0 {
		return fmt.Errorf("laced video requires frame duration")
	}
	if len(v.samples)+len(sizes) > maxSamples {
		return fmt.Errorf("video index exceeds sample limit")
	}
	// BlockDuration is the duration of the whole block; Track DefaultDuration is per frame.
	frameDuration := duration
	if totalDuration && len(sizes) > 1 {
		frameDuration /= time.Duration(len(sizes))
	}
	for i, size := range sizes {
		v.samples = append(v.samples, sample{offset: e.start + int64(payloadPos), size: size, pts: time.Duration(cluster+rel)*time.Duration(scale) + time.Duration(i)*frameDuration, duration: frameDuration, key: key && i == 0})
		payloadPos += size
	}
	return nil
}

func laceSizes(b []byte, pos int, mode byte) ([]int, int, error) {
	if mode == 0 {
		return []int{len(b) - pos}, pos, nil
	}
	if pos >= len(b) {
		return nil, 0, io.ErrUnexpectedEOF
	}
	count := int(b[pos]) + 1
	pos++
	sizes := make([]int, count)
	switch mode {
	case 1:
		for i := 0; i < count-1; i++ {
			for {
				if pos >= len(b) {
					return nil, 0, io.ErrUnexpectedEOF
				}
				n := int(b[pos])
				pos++
				sizes[i] += n
				if n < 255 {
					break
				}
			}
		}
	case 2:
		if (len(b)-pos)%count != 0 {
			return nil, 0, fmt.Errorf("invalid fixed lacing")
		}
		for i := range sizes {
			sizes[i] = (len(b) - pos) / count
		}
		return sizes, pos, nil
	case 3:
		for i := 0; i < count-1; i++ {
			n, width, err := vint(b[pos:], false)
			if err != nil {
				return nil, 0, err
			}
			pos += width
			if i == 0 {
				sizes[i] = int(n)
			} else {
				sizes[i] = sizes[i-1] + int(n) - int((uint64(1)<<uint(7*width-1))-1)
			}
		}
	}
	remaining := len(b) - pos
	for _, n := range sizes[:count-1] {
		if n <= 0 || n > remaining {
			return nil, 0, fmt.Errorf("invalid laced frame size")
		}
		remaining -= n
	}
	sizes[count-1] = remaining
	if remaining <= 0 {
		return nil, 0, fmt.Errorf("empty laced frame")
	}
	return sizes, pos, nil
}

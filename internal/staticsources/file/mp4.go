package file

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

const maxIndexSize = 64 * 1024 * 1024
const maxSamples = 2_000_000

type box struct {
	typ  string
	data []byte
}

func boxes(data []byte) ([]box, error) {
	var out []box
	for len(data) > 0 {
		if len(data) < 8 {
			return nil, fmt.Errorf("truncated MP4 box")
		}
		n := int64(binary.BigEndian.Uint32(data))
		head := int64(8)
		if n == 1 {
			if len(data) < 16 {
				return nil, io.ErrUnexpectedEOF
			}
			n = int64(binary.BigEndian.Uint64(data[8:]))
			head = 16
		}
		if n == 0 {
			n = int64(len(data))
		}
		if n < head || n > int64(len(data)) {
			return nil, fmt.Errorf("invalid MP4 box size")
		}
		out = append(out, box{string(data[4:8]), data[head:n]})
		data = data[n:]
	}
	return out, nil
}

func child(data []byte, path ...string) ([]byte, error) {
	for _, name := range path {
		bs, err := boxes(data)
		if err != nil {
			return nil, err
		}
		var found []byte
		for _, b := range bs {
			if b.typ == name {
				found = b.data
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("missing MP4 box %s", name)
		}
		data = found
	}
	return data, nil
}

func ticks(n int64, scale uint32) time.Duration {
	return time.Duration(n/int64(scale))*time.Second + time.Duration(n%int64(scale))*time.Second/time.Duration(scale)
}

func table(b []byte, width int) ([][]byte, error) {
	if len(b) < 8 {
		return nil, io.ErrUnexpectedEOF
	}
	count := int(binary.BigEndian.Uint32(b[4:]))
	b = b[8:]
	if count > maxSamples || count > len(b)/width {
		return nil, fmt.Errorf("invalid MP4 table")
	}
	out := make([][]byte, count)
	for i := range out {
		out[i] = b[i*width : (i+1)*width]
	}
	return out, nil
}

func readMP4(ctx context.Context, f *os.File, size int64) (*video, error) {
	var moov []byte
	for off := int64(0); off < size; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h := make([]byte, 16)
		if _, err := f.ReadAt(h[:8], off); err != nil {
			return nil, err
		}
		n := int64(binary.BigEndian.Uint32(h))
		head := int64(8)
		if n == 1 {
			if _, err := f.ReadAt(h[8:], off+8); err != nil {
				return nil, err
			}
			n = int64(binary.BigEndian.Uint64(h[8:]))
			head = 16
		}
		if n == 0 {
			n = size - off
		}
		if n < head || n > size-off {
			return nil, fmt.Errorf("invalid MP4 box size")
		}
		if string(h[4:8]) == "moov" {
			if n-head > maxIndexSize {
				return nil, fmt.Errorf("MP4 index exceeds 64 MiB")
			}
			moov = make([]byte, n-head)
			if _, err := f.ReadAt(moov, off+head); err != nil {
				return nil, err
			}
		}
		off += n
	}
	if moov == nil {
		return nil, fmt.Errorf("MP4 has no moov box")
	}
	bs, err := boxes(moov)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		if b.typ == "mvex" {
			return nil, fmt.Errorf("fragmented MP4 is not supported")
		}
	}
	for _, b := range bs {
		if b.typ != "trak" {
			continue
		}
		handler, err := child(b.data, "mdia", "hdlr")
		if err != nil {
			return nil, err
		}
		if len(handler) < 12 {
			return nil, io.ErrUnexpectedEOF
		}
		if string(handler[8:12]) != "vide" {
			continue
		}
		stbl, err := child(b.data, "mdia", "minf", "stbl")
		if err != nil {
			return nil, err
		}
		stsd, err := child(stbl, "stsd")
		if err != nil {
			return nil, err
		}
		if len(stsd) < 8 {
			return nil, io.ErrUnexpectedEOF
		}
		entries, err := boxes(stsd[8:])
		if err != nil {
			return nil, err
		}
		if len(entries) != 1 {
			return nil, fmt.Errorf("multiple video sample descriptions are not supported")
		}
		entry := entries[0]
		var codec, config string
		switch entry.typ {
		case "avc1", "avc3":
			codec = "h264"
			config = "avcC"
		case "hvc1", "hev1":
			codec = "h265"
			config = "hvcC"
		default:
			continue
		}
		if len(entry.data) < 78 {
			return nil, io.ErrUnexpectedEOF
		}
		conf, err := child(entry.data[78:], config)
		if err != nil {
			return nil, err
		}
		forma, length, err := codecConfig(codec, conf)
		if err != nil {
			return nil, err
		}
		v := &video{format: forma, lengthSize: length}
		mdhd, err := child(b.data, "mdia", "mdhd")
		if err != nil {
			return nil, err
		}
		scalePos := 12
		if len(mdhd) > 0 && mdhd[0] == 1 {
			scalePos = 20
		}
		if len(mdhd) < scalePos+4 {
			return nil, io.ErrUnexpectedEOF
		}
		scale := binary.BigEndian.Uint32(mdhd[scalePos:])
		if scale == 0 {
			return nil, fmt.Errorf("invalid video timescale")
		}
		if err = mp4Samples(ctx, stbl, scale, size, v); err != nil {
			return nil, err
		}
		return v, nil
	}
	return nil, fmt.Errorf("file has no H264 or H265 video track")
}

func mp4Samples(ctx context.Context, stbl []byte, scale uint32, fileSize int64, v *video) error {
	sz, err := child(stbl, "stsz")
	if err != nil {
		return err
	}
	if len(sz) < 12 {
		return io.ErrUnexpectedEOF
	}
	fixed := binary.BigEndian.Uint32(sz[4:])
	count := int(binary.BigEndian.Uint32(sz[8:]))
	if count == 0 || count > maxSamples {
		return fmt.Errorf("invalid MP4 sample count")
	}
	if fixed == 0 && count > (len(sz)-12)/4 {
		return io.ErrUnexpectedEOF
	}
	v.samples = make([]sample, count)
	for i := range v.samples {
		n := fixed
		if n == 0 {
			n = binary.BigEndian.Uint32(sz[12+i*4:])
		}
		if n == 0 || n > maxFrameSize {
			return fmt.Errorf("invalid sample size")
		}
		v.samples[i].size = int(n)
		v.samples[i].key = true
	}
	ts, err := child(stbl, "stts")
	if err != nil {
		return err
	}
	rows, err := table(ts, 8)
	if err != nil {
		return err
	}
	index := 0
	var dts int64
	for _, row := range rows {
		num := int(binary.BigEndian.Uint32(row))
		dur := binary.BigEndian.Uint32(row[4:])
		if num > count-index || dur == 0 {
			return fmt.Errorf("invalid stts")
		}
		for range num {
			v.samples[index].dts = ticks(dts, scale)
			v.samples[index].pts = ticks(dts, scale)
			v.samples[index].duration = ticks(int64(dur), scale)
			dts += int64(dur)
			index++
		}
	}
	if index != count {
		return fmt.Errorf("incomplete stts")
	}
	bs, err := boxes(stbl)
	if err != nil {
		return err
	}
	for _, b := range bs {
		switch b.typ {
		case "ctts":
			rows, err = table(b.data, 8)
			if err != nil {
				return err
			}
			index = 0
			for _, row := range rows {
				num := int(binary.BigEndian.Uint32(row))
				val := int64(binary.BigEndian.Uint32(row[4:]))
				if b.data[0] == 1 {
					val = int64(int32(val))
				}
				if num > count-index {
					return fmt.Errorf("invalid ctts")
				}
				for range num {
					v.samples[index].pts += ticks(val, scale)
					index++
				}
			}
			if index != count {
				return fmt.Errorf("incomplete ctts")
			}
		case "stss":
			rows, err = table(b.data, 4)
			if err != nil {
				return err
			}
			for i := range v.samples {
				v.samples[i].key = false
			}
			for _, row := range rows {
				n := int(binary.BigEndian.Uint32(row))
				if n < 1 || n > count {
					return fmt.Errorf("invalid stss")
				}
				v.samples[n-1].key = true
			}
		}
	}
	sc, err := child(stbl, "stsc")
	if err != nil {
		return err
	}
	chunks, err := table(sc, 12)
	if err != nil {
		return err
	}
	if len(chunks) == 0 || binary.BigEndian.Uint32(chunks[0]) != 1 {
		return fmt.Errorf("invalid stsc")
	}
	for i, c := range chunks {
		if binary.BigEndian.Uint32(c[4:]) == 0 || binary.BigEndian.Uint32(c[8:]) != 1 {
			return fmt.Errorf("unsupported sample description")
		}
		if i > 0 && binary.BigEndian.Uint32(c) <= binary.BigEndian.Uint32(chunks[i-1]) {
			return fmt.Errorf("invalid stsc order")
		}
	}
	var offsets [][]byte
	wide := false
	for _, b := range bs {
		if b.typ == "stco" {
			offsets, err = table(b.data, 4)
		}
		if b.typ == "co64" {
			offsets, err = table(b.data, 8)
			wide = true
		}
		if err != nil {
			return err
		}
	}
	if len(offsets) == 0 {
		return fmt.Errorf("missing chunk offsets")
	}
	index = 0
	ci := 0
	for i, row := range offsets {
		if err = ctx.Err(); err != nil {
			return err
		}
		for ci+1 < len(chunks) && uint32(i+1) >= binary.BigEndian.Uint32(chunks[ci+1]) {
			ci++
		}
		n := int(binary.BigEndian.Uint32(chunks[ci][4:]))
		if n > count-index {
			return fmt.Errorf("invalid chunk sample count")
		}
		off := int64(binary.BigEndian.Uint32(row))
		if wide {
			off = int64(binary.BigEndian.Uint64(row))
		}
		for range n {
			sa := &v.samples[index]
			if off < 0 || off > fileSize-int64(sa.size) {
				return fmt.Errorf("sample outside file")
			}
			sa.offset = off
			off += int64(sa.size)
			index++
		}
	}
	if index != count {
		return fmt.Errorf("incomplete MP4 sample index")
	}
	return nil
}

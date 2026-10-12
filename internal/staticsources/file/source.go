// Package file implements a shared, continuously looping local video source.
package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/fileurl"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/unit"
)

const maxFrameSize = 32 * 1024 * 1024

type parent interface {
	logger.Writer
	SetReady(defs.PathSourceStaticSetReadyReq) defs.PathSourceStaticSetReadyRes
	SetNotReady(defs.PathSourceStaticSetNotReadyReq)
}

// Source reads the first supported video track; all other tracks are ignored.
type Source struct{ Parent parent }

func (s *Source) Log(level logger.Level, msg string, args ...any) {
	s.Parent.Log(level, "[file source] "+msg, args...)
}

// Info implements StaticSource.
func (*Source) Info() defs.StaticSourceInfo { return defs.StaticSourceInfo{} }

// APISourceDescribe implements StaticSource.
func (*Source) APISourceDescribe() *defs.APIPathSource {
	return &defs.APIPathSource{Type: defs.APIPathSourceTypeFileSource}
}

type sample struct {
	offset             int64
	size               int
	pts, dts, duration time.Duration
	key                bool
}

type video struct {
	format     format.Format
	lengthSize int
	samples    []sample
	duration   time.Duration
}

func (v *video) payload(f *os.File, sa sample) (unit.Payload, error) {
	if sa.size <= 0 || sa.size > maxFrameSize {
		return nil, fmt.Errorf("invalid video sample size")
	}
	b := make([]byte, sa.size)
	if _, err := f.ReadAt(b, sa.offset); err != nil {
		return nil, err
	}
	var nalus [][]byte
	for len(b) > 0 {
		if len(b) < v.lengthSize {
			return nil, fmt.Errorf("truncated NAL length")
		}
		n := 0
		for _, val := range b[:v.lengthSize] {
			n = n<<8 | int(val)
		}
		b = b[v.lengthSize:]
		if n <= 0 || n > len(b) {
			return nil, fmt.Errorf("invalid NAL length")
		}
		nalus = append(nalus, b[:n])
		b = b[n:]
	}
	if _, ok := v.format.(*format.H264); ok {
		return unit.PayloadH264(nalus), nil
	}
	return unit.PayloadH265(nalus), nil
}

func (v *video) finalize() error {
	if len(v.samples) == 0 {
		return fmt.Errorf("file contains no video samples")
	}
	// Starting at a sync sample is required for every repeat to be decodable.
	if !v.samples[0].key {
		return fmt.Errorf("video must start with a key frame")
	}
	minDTS := v.samples[0].dts
	minPTS := v.samples[0].pts
	var presentationEnd time.Duration
	for i := range v.samples {
		sa := &v.samples[i]
		if sa.duration <= 0 {
			return fmt.Errorf("invalid video sample duration")
		}
		if i > 0 && sa.dts < v.samples[i-1].dts {
			return fmt.Errorf("video decode timestamps are not ordered")
		}
		minPTS = min(minPTS, sa.pts)
		presentationEnd = max(presentationEnd, sa.pts+sa.duration)
		if sa.dts < minDTS {
			minDTS = sa.dts
		}
	}
	for i := range v.samples {
		sa := &v.samples[i]
		sa.dts -= minDTS
		sa.pts -= minDTS
		v.duration = max(v.duration, sa.dts+sa.duration)
	}
	v.duration = max(v.duration, presentationEnd-minPTS)
	if v.duration <= 0 {
		return fmt.Errorf("invalid video duration")
	}
	return nil
}

func wait(ctx context.Context, deadline time.Time) error {
	t := time.NewTimer(time.Until(deadline))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run implements StaticSource. EOF never marks the source unavailable.
func (s *Source) Run(params defs.StaticSourceRunParams) error {
	path, err := fileurl.Path(params.ResolvedSource)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("file source requires a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("file source requires a regular file")
	}
	var v *video
	if strings.EqualFold(filepath.Ext(path), ".mp4") {
		v, err = readMP4(params.Context, f, st.Size())
	} else {
		v, err = readMKV(params.Context, f, st.Size())
	}
	if err != nil {
		return err
	}
	if err = v.finalize(); err != nil {
		return err
	}
	media := &description.Media{Type: description.MediaTypeVideo, Formats: []format.Format{v.format}}
	res := s.Parent.SetReady(defs.PathSourceStaticSetReadyReq{
		Desc: &description.Session{Medias: []*description.Media{media}}, UseRTPPackets: false, ReplaceNTP: true,
	})
	if res.Err != nil {
		return res.Err
	}
	defer s.Parent.SetNotReady(defs.PathSourceStaticSetNotReadyReq{})
	s.Log(logger.Info, "looping %s (video only)", path)
	start := time.Now()
	var base time.Duration
	for {
		for _, sa := range v.samples {
			if err = wait(params.Context, start.Add(base+sa.dts)); err != nil {
				return err
			}
			select {
			case <-params.ReloadConf:
			default:
			}
			payload, payloadErr := v.payload(f, sa)
			if payloadErr != nil {
				return payloadErr
			}
			pts := base + sa.pts
			res.SubStream.WriteUnit(media, v.format, &unit.Unit{
				PTS:     int64(pts/time.Second)*90000 + int64(pts%time.Second)*90000/int64(time.Second),
				Payload: payload,
			})
		}
		base += v.duration
	}
}

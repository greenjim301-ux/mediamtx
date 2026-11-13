// Package gb28181 contains the GB28181 static source.
package gb28181

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/errordumper"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/mpegps"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

const (
	// maximum number of packets read while probing tracks.
	probeMaxPackets = 4096

	// maximum duration of video, in the 90khz clock, read while waiting for data of audio tracks.
	// Some devices declare an audio track in the program stream map but never transmit it;
	// such tracks are removed, otherwise readers that wait for audio (e.g. HTTP-FLV players)
	// would never start playback.
	probeMaxVideoDuration = 90000

	ptsMask = (1 << 33) - 1
)

type parent interface {
	logger.Writer
	SetReady(req defs.PathSourceStaticSetReadyReq) defs.PathSourceStaticSetReadyRes
	SetNotReady(req defs.PathSourceStaticSetNotReadyReq)
}

// Session is a media session with a GB28181 device.
type Session interface {
	// Reader returns the MPEG-PS stream sent by the device.
	Reader() io.Reader
	Close()
}

// Server is the GB28181 server, that establishes sessions with devices.
type Server interface {
	StartSession(ctx context.Context, channelID string) (Session, error)
}

// Source is a GB28181 static source.
type Source struct {
	ReadTimeout conf.Duration
	Server      Server
	Parent      parent
}

// Log implements logger.Writer.
func (s *Source) Log(level logger.Level, format string, args ...any) {
	s.Parent.Log(level, "[GB28181 source] "+format, args...)
}

// Info implements StaticSource.
func (*Source) Info() defs.StaticSourceInfo {
	return defs.StaticSourceInfo{}
}

// channelID extracts the channel ID from a source URL.
// Both gb28181://<channel> and gb28181://<device>/<channel> are supported.
func channelID(source string) (string, error) {
	v := strings.TrimPrefix(source, "gb28181://")
	v = strings.Trim(v, "/")

	if i := strings.LastIndex(v, "/"); i >= 0 {
		v = v[i+1:]
	}

	if v == "" {
		return "", fmt.Errorf("invalid source: '%s'", source)
	}

	return v, nil
}

// Run implements StaticSource.
func (s *Source) Run(params defs.StaticSourceRunParams) error {
	if s.Server == nil {
		return fmt.Errorf("the GB28181 server is not enabled in the configuration")
	}

	id, err := channelID(params.ResolvedSource)
	if err != nil {
		return err
	}

	s.Log(logger.Debug, "requesting channel '%s'", id)

	sess, err := s.Server.StartSession(params.Context, id)
	if err != nil {
		return err
	}
	defer sess.Close()

	readerErr := make(chan error)
	go func() {
		readerErr <- s.runReader(sess)
	}()

	for {
		select {
		case err = <-readerErr:
			return err

		case <-params.ReloadConf:

		case <-params.Context.Done():
			sess.Close()
			<-readerErr
			return fmt.Errorf("terminated")
		}
	}
}

func (s *Source) runReader(sess Session) error {
	r := &mpegps.Reader{R: sess.Reader()}

	err := r.Initialize()
	if err != nil {
		return err
	}

	decodeErrors := &errordumper.Dumper{
		OnReport: func(val uint64, last error) {
			if val == 1 {
				s.Log(logger.Warn, "decode error: %v", last)
			} else {
				s.Log(logger.Warn, "%d decode errors, last was: %v", val, last)
			}
		},
	}

	decodeErrors.Start()
	defer decodeErrors.Stop()

	r.OnDecodeError(func(err error) {
		decodeErrors.Add(err)
	})

	var subStream *stream.SubStream

	medias, replay, err := s.toStream(r, &subStream)
	if err != nil {
		return err
	}

	res := s.Parent.SetReady(defs.PathSourceStaticSetReadyReq{
		Desc:          &description.Session{Medias: medias},
		UseRTPPackets: false,
		ReplaceNTP:    true,
	})
	if res.Err != nil {
		return res.Err
	}

	defer s.Parent.SetNotReady(defs.PathSourceStaticSetNotReadyReq{})

	subStream = res.SubStream

	// write data that has been read while probing tracks.
	err = replay()
	if err != nil {
		return err
	}

	for {
		err = r.Read()
		if err != nil {
			return err
		}
	}
}

type probedAU struct {
	track *mpegps.Track
	pts   int64
	dts   int64
	au    []byte
}

// probeResult contains the outcome of probeTracks.
type probeResult struct {
	// audio tracks for which data has been received.
	audioReceived map[*mpegps.Track]bool
	// configuration of AAC tracks.
	aacConfigs map[*mpegps.Track]*mpeg4audio.AudioSpecificConfig
	// access units read while probing, in reception order.
	aus []probedAU
}

func isProbedAudio(track *mpegps.Track) bool {
	switch track.StreamType {
	case mpegps.StreamTypeG711A, mpegps.StreamTypeG711U, mpegps.StreamTypeAAC:
		return true
	}
	return false
}

func aacConfigFromAU(au []byte) *mpeg4audio.AudioSpecificConfig {
	var pkts mpeg4audio.ADTSPackets
	err := pkts.Unmarshal(au)
	if err != nil || len(pkts) == 0 {
		return nil
	}

	return &mpeg4audio.AudioSpecificConfig{
		Type:          pkts[0].Type,
		SampleRate:    pkts[0].SampleRate,
		ChannelConfig: pkts[0].ChannelConfig,
	}
}

// probeTracks reads the stream until data of all audio tracks has been received
// (and the configuration of AAC tracks has been found), or until a time limit is reached.
// Access units read in the meanwhile are stored, in order to be written later.
func probeTracks(r *mpegps.Reader) (*probeResult, error) {
	res := &probeResult{
		audioReceived: make(map[*mpegps.Track]bool),
		aacConfigs:    make(map[*mpegps.Track]*mpeg4audio.AudioSpecificConfig),
	}

	tracks := r.Tracks()

	hasAudio := false
	for _, track := range tracks {
		if isProbedAudio(track) {
			hasAudio = true
		}
	}
	if !hasAudio {
		return res, nil
	}

	firstVideoPTS := int64(-1)
	videoDuration := int64(0)

	for _, track := range tracks {
		r.OnData(track, func(pts int64, dts int64, au []byte) error {
			res.aus = append(res.aus, probedAU{track: track, pts: pts, dts: dts, au: au})

			switch {
			case isProbedAudio(track):
				res.audioReceived[track] = true

				if track.StreamType == mpegps.StreamTypeAAC && res.aacConfigs[track] == nil {
					if conf := aacConfigFromAU(au); conf != nil {
						res.aacConfigs[track] = conf
					}
				}

			case track.IsVideo():
				if firstVideoPTS < 0 {
					firstVideoPTS = pts
				} else {
					videoDuration = (pts - firstVideoPTS) & ptsMask
				}
			}
			return nil
		})
	}

	// remove temporary callbacks.
	defer func() {
		for _, track := range tracks {
			r.OnData(track, nil)
		}
	}()

	complete := func() bool {
		for _, track := range tracks {
			if !isProbedAudio(track) {
				continue
			}
			if !res.audioReceived[track] {
				return false
			}
			if track.StreamType == mpegps.StreamTypeAAC && res.aacConfigs[track] == nil {
				return false
			}
		}
		return true
	}

	for range probeMaxPackets {
		err := r.Read()
		if err != nil {
			return nil, err
		}

		if complete() || videoDuration >= probeMaxVideoDuration {
			break
		}
	}

	return res, nil
}

// toStream maps the tracks of a MPEG-PS stream to MediaMTX medias.
// It returns a function that writes data read while probing tracks;
// the function must be called once *subStream has been set.
func (s *Source) toStream(
	r *mpegps.Reader,
	subStream **stream.SubStream,
) ([]*description.Media, func() error, error) {
	var medias []*description.Media //nolint:prealloc

	// timestamps of all tracks are decoded with the same decoder,
	// in order to keep tracks in sync.
	td := &mpegts.TimeDecoder{}
	td.Initialize()

	// audio tracks declared in the program stream map are checked for actual data,
	// and the configuration of AAC tracks, which is not present in the program stream map,
	// is extracted from the stream. This is performed before setting up callbacks,
	// since the stream is not ready yet.
	probe, err := probeTracks(r)
	if err != nil {
		return nil, nil, err
	}

	callbacks := make(map[*mpegps.Track]func(int64, int64, []byte) error)
	setCallback := func(track *mpegps.Track, cb func(int64, int64, []byte) error) {
		callbacks[track] = cb
		r.OnData(track, cb)
	}

	for _, track := range r.Tracks() {
		var medi *description.Media

		if isProbedAudio(track) && !probe.audioReceived[track] {
			s.Log(logger.Warn, "skipping audio track %s: declared by the device but no data received", track)
			continue
		}

		switch track.StreamType {
		case mpegps.StreamTypeH264:
			medi = &description.Media{
				Type: description.MediaTypeVideo,
				Formats: []format.Format{&format.H264{
					PayloadTyp:        96,
					PacketizationMode: 1,
				}},
			}

			setCallback(track, func(pts int64, _ int64, au []byte) error {
				var annexb h264.AnnexB
				err := annexb.Unmarshal(au)
				if err != nil {
					// corrupted access units are discarded.
					return nil //nolint:nilerr
				}

				(*subStream).WriteUnit(medi, medi.Formats[0], &unit.Unit{
					// no conversion is needed since the clock rate is 90khz
					// in both MPEG-PS and RTSP.
					PTS:     td.Decode(pts),
					Payload: unit.PayloadH264(annexb),
				})
				return nil
			})

		case mpegps.StreamTypeH265:
			medi = &description.Media{
				Type: description.MediaTypeVideo,
				Formats: []format.Format{&format.H265{
					PayloadTyp: 96,
				}},
			}

			setCallback(track, func(pts int64, _ int64, au []byte) error {
				var annexb h264.AnnexB
				err := annexb.Unmarshal(au)
				if err != nil {
					// corrupted access units are discarded.
					return nil //nolint:nilerr
				}

				(*subStream).WriteUnit(medi, medi.Formats[0], &unit.Unit{
					PTS:     td.Decode(pts),
					Payload: unit.PayloadH265(annexb),
				})
				return nil
			})

		case mpegps.StreamTypeG711A, mpegps.StreamTypeG711U:
			muLaw := (track.StreamType == mpegps.StreamTypeG711U)

			payloadTyp := uint8(8)
			if muLaw {
				payloadTyp = 0
			}

			medi = &description.Media{
				Type: description.MediaTypeAudio,
				Formats: []format.Format{&format.G711{
					PayloadTyp:   payloadTyp,
					MULaw:        muLaw,
					SampleRate:   8000,
					ChannelCount: 1,
				}},
			}

			setCallback(track, func(pts int64, _ int64, au []byte) error {
				(*subStream).WriteUnit(medi, medi.Formats[0], &unit.Unit{
					PTS:     multiplyAndDivide(td.Decode(pts), 8000, 90000),
					Payload: unit.PayloadG711(au),
				})
				return nil
			})

		case mpegps.StreamTypeAAC:
			conf, ok := probe.aacConfigs[track]
			if !ok {
				s.Log(logger.Warn, "skipping audio track: unable to detect the configuration of the audio track")
				continue
			}

			medi = &description.Media{
				Type: description.MediaTypeAudio,
				Formats: []format.Format{&format.MPEG4Audio{
					PayloadTyp:       96,
					Config:           conf,
					SizeLength:       13,
					IndexLength:      3,
					IndexDeltaLength: 3,
				}},
			}

			sampleRate := int64(conf.SampleRate)

			setCallback(track, func(pts int64, _ int64, au []byte) error {
				var pkts mpeg4audio.ADTSPackets
				err2 := pkts.Unmarshal(au)
				if err2 != nil {
					// corrupted access units are discarded.
					return nil //nolint:nilerr
				}

				aus := make([][]byte, len(pkts))
				for i, pkt := range pkts {
					aus[i] = pkt.AU
				}

				(*subStream).WriteUnit(medi, medi.Formats[0], &unit.Unit{
					PTS:     multiplyAndDivide(td.Decode(pts), sampleRate, 90000),
					Payload: unit.PayloadMPEG4Audio(aus),
				})
				return nil
			})

		default:
			s.Log(logger.Warn, "skipping track with unsupported codec: %s", track)
			continue
		}

		medias = append(medias, medi)
	}

	if len(medias) == 0 {
		return nil, nil, fmt.Errorf("no supported tracks found")
	}

	// data of skipped tracks has no callback and is discarded.
	replay := func() error {
		for _, pau := range probe.aus {
			cb := callbacks[pau.track]
			if cb == nil {
				continue
			}
			err2 := cb(pau.pts, pau.dts, pau.au)
			if err2 != nil {
				return err2
			}
		}
		probe.aus = nil
		return nil
	}

	return medias, replay, nil
}

func multiplyAndDivide(v, m, d int64) int64 {
	secs := v / d
	dec := v % d
	return secs*m + dec*m/d
}

// APISourceDescribe implements StaticSource.
func (*Source) APISourceDescribe() *defs.APIPathSource {
	return &defs.APIPathSource{
		Type: defs.APIPathSourceTypeGB28181Source,
		ID:   "",
	}
}

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

// maximum number of packets read while looking for the AAC configuration.
const aacProbePackets = 512

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

	medias, err := s.toStream(r, &subStream)
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

	for {
		err = r.Read()
		if err != nil {
			return err
		}
	}
}

// probeAACConfig reads the stream until the configuration of a AAC track is found.
func (s *Source) probeAACConfig(r *mpegps.Reader, track *mpegps.Track) (*mpeg4audio.AudioSpecificConfig, error) {
	var conf *mpeg4audio.AudioSpecificConfig

	r.OnData(track, func(_ int64, _ int64, au []byte) error {
		var pkts mpeg4audio.ADTSPackets
		err := pkts.Unmarshal(au)
		if err != nil || len(pkts) == 0 {
			// wait for a decodable packet.
			return nil //nolint:nilerr
		}

		conf = &mpeg4audio.AudioSpecificConfig{
			Type:          pkts[0].Type,
			SampleRate:    pkts[0].SampleRate,
			ChannelConfig: pkts[0].ChannelConfig,
		}
		return nil
	})

	for range aacProbePackets {
		err := r.Read()
		if err != nil {
			return nil, err
		}

		if conf != nil {
			// remove the temporary callback.
			r.OnData(track, nil)
			return conf, nil
		}
	}

	r.OnData(track, nil)

	return nil, fmt.Errorf("unable to detect the configuration of the audio track")
}

// toStream maps the tracks of a MPEG-PS stream to MediaMTX medias.
func (s *Source) toStream(
	r *mpegps.Reader,
	subStream **stream.SubStream,
) ([]*description.Media, error) {
	var medias []*description.Media //nolint:prealloc

	// timestamps of all tracks are decoded with the same decoder,
	// in order to keep tracks in sync.
	td := &mpegts.TimeDecoder{}
	td.Initialize()

	// the configuration of AAC tracks is not present in the program stream map,
	// and has to be extracted from the stream. This is performed before
	// setting up callbacks, since the stream is not ready yet.
	aacConfigs := make(map[*mpegps.Track]*mpeg4audio.AudioSpecificConfig)

	for _, track := range r.Tracks() {
		if track.StreamType != mpegps.StreamTypeAAC {
			continue
		}

		conf, err := s.probeAACConfig(r, track)
		if err != nil {
			s.Log(logger.Warn, "skipping audio track: %v", err)
			continue
		}

		aacConfigs[track] = conf
	}

	for _, track := range r.Tracks() {
		var medi *description.Media

		switch track.StreamType {
		case mpegps.StreamTypeH264:
			medi = &description.Media{
				Type: description.MediaTypeVideo,
				Formats: []format.Format{&format.H264{
					PayloadTyp:        96,
					PacketizationMode: 1,
				}},
			}

			r.OnData(track, func(pts int64, _ int64, au []byte) error {
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

			r.OnData(track, func(pts int64, _ int64, au []byte) error {
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

			r.OnData(track, func(pts int64, _ int64, au []byte) error {
				(*subStream).WriteUnit(medi, medi.Formats[0], &unit.Unit{
					PTS:     multiplyAndDivide(td.Decode(pts), 8000, 90000),
					Payload: unit.PayloadG711(au),
				})
				return nil
			})

		case mpegps.StreamTypeAAC:
			conf, ok := aacConfigs[track]
			if !ok {
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

			r.OnData(track, func(pts int64, _ int64, au []byte) error {
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
		return nil, fmt.Errorf("no supported tracks found")
	}

	return medias, nil
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

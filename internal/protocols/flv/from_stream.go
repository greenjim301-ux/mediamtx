package flv

import (
	"fmt"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"

	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

var errNoSupportedCodecsFrom = fmt.Errorf(
	"the stream doesn't contain any supported codec, which are currently H264 and H265")

func multiplyAndDivide2(v, m, d time.Duration) time.Duration {
	secs := v / d
	dec := v % d
	return (secs*m + dec*m/d)
}

func timestampToDuration(t int64, clockRate int) time.Duration {
	return multiplyAndDivide2(time.Duration(t), time.Second, time.Duration(clockRate))
}

// FromStream maps a MediaMTX stream to a video-only FLV stream.
//
// Only the first H264 or H265 track is used; any other track (including audio) is ignored.
// Nothing is written until the first key frame is received: there is no GOP cache, so a
// reader that attaches mid-stream waits for the next key frame before any data is sent.
func FromStream(desc *description.Session, r *stream.Reader, w *Writer) error {
	for _, media := range desc.Medias {
		for _, forma := range media.Formats {
			switch forma := forma.(type) {
			case *format.H264:
				sps, pps := forma.SPS, forma.PPS

				var dtsExtractor *h264.DTSExtractor
				headerSent := false

				r.OnData(media, forma, func(u *unit.Unit) error {
					if u.NilPayload() {
						return nil
					}

					au := u.Payload.(unit.PayloadH264) //nolint:forcetypeassert

					idrPresent := false
					nonIDRPresent := false

					for _, nalu := range au {
						switch h264.NALUType(nalu[0] & 0x1F) {
						case h264.NALUTypeIDR:
							idrPresent = true
						case h264.NALUTypeNonIDR:
							nonIDRPresent = true
						}
					}

					// wait until we receive an IDR: there's no GOP cache, so
					// decoding must start from a key frame.
					if dtsExtractor == nil {
						if !idrPresent {
							return nil
						}
						dtsExtractor = &h264.DTSExtractor{}
						dtsExtractor.Initialize()
					} else if !idrPresent && !nonIDRPresent {
						return nil
					}

					dts, err := dtsExtractor.Extract(au, u.PTS)
					if err != nil {
						return err
					}

					ptsDur := timestampToDuration(u.PTS, forma.ClockRate())
					dtsDur := timestampToDuration(dts, forma.ClockRate())

					if !headerSent {
						configBody, cfgErr := h264DecoderConfig(sps, pps)
						if cfgErr != nil {
							return cfgErr
						}

						cfgErr = w.writeTag(dtsDur, configBody)
						if cfgErr != nil {
							return cfgErr
						}

						headerSent = true
					}

					body, err := h264AU(au, idrPresent, ptsDur-dtsDur)
					if err != nil {
						return err
					}

					return w.writeTag(dtsDur, body)
				})

				return nil

			case *format.H265:
				vps, sps, pps := forma.VPS, forma.SPS, forma.PPS

				var dtsExtractor *h265.DTSExtractor
				headerSent := false

				r.OnData(media, forma, func(u *unit.Unit) error {
					if u.NilPayload() {
						return nil
					}

					au := u.Payload.(unit.PayloadH265) //nolint:forcetypeassert

					random := h265.IsRandomAccess(au)

					// wait until we receive a random access unit: there's no GOP
					// cache, so decoding must start from a key frame.
					if dtsExtractor == nil {
						if !random {
							return nil
						}
						dtsExtractor = &h265.DTSExtractor{}
						dtsExtractor.Initialize()
					}

					dts, err := dtsExtractor.Extract(au, u.PTS)
					if err != nil {
						return err
					}

					ptsDur := timestampToDuration(u.PTS, forma.ClockRate())
					dtsDur := timestampToDuration(dts, forma.ClockRate())

					if !headerSent {
						configBody, cfgErr := h265DecoderConfig(vps, sps, pps)
						if cfgErr != nil {
							return cfgErr
						}

						cfgErr = w.writeTag(dtsDur, configBody)
						if cfgErr != nil {
							return cfgErr
						}

						headerSent = true
					}

					body, err := h265AU(au, random, ptsDur-dtsDur)
					if err != nil {
						return err
					}

					return w.writeTag(dtsDur, body)
				})

				return nil
			}
		}
	}

	return errNoSupportedCodecsFrom
}

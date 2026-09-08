package flv

import (
	"fmt"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"

	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

var errNoSupportedCodecsFrom = fmt.Errorf(
	"the stream doesn't contain any supported codec, which are currently " +
		"H264, H265, MPEG-4 Audio (AAC), G711 (PCMA, PCMU)")

func multiplyAndDivide2(v, m, d time.Duration) time.Duration {
	secs := v / d
	dec := v % d
	return (secs*m + dec*m/d)
}

func timestampToDuration(t int64, clockRate int) time.Duration {
	return multiplyAndDivide2(time.Duration(t), time.Second, time.Duration(clockRate))
}

// FromStream maps a MediaMTX stream to a FLV stream.
//
// At most one video track (H264 or H265) and one audio track (AAC or G711) are used,
// since FLV doesn't support multiple tracks of the same type.
//
// Nothing is written until the first key frame is received: there is no GOP cache, so a
// reader that attaches mid-stream waits for the next key frame before receiving any data.
// When a video track is present, audio is held back too, in order to start the stream
// with a decodable video sample.
func FromStream(desc *description.Session, r *stream.Reader, w *Writer) error {
	var videoMedia *description.Media
	var videoFormat format.Format
	var audioMedia *description.Media
	var audioFormat format.Format

	for _, media := range desc.Medias {
		for _, forma := range media.Formats {
			switch forma := forma.(type) {
			case *format.H264, *format.H265:
				if videoFormat == nil {
					videoMedia, videoFormat = media, forma
				}

			case *format.MPEG4Audio:
				if audioFormat == nil && forma.Config != nil {
					audioMedia, audioFormat = media, forma
				}

			case *format.G711:
				// FLV supports G711 at 8000 Hz only.
				if audioFormat == nil && forma.SampleRate == 8000 {
					audioMedia, audioFormat = media, forma
				}
			}
		}
	}

	if videoFormat == nil && audioFormat == nil {
		return errNoSupportedCodecsFrom
	}

	w.hasVideo = videoFormat != nil
	w.hasAudio = audioFormat != nil

	// when there's no video, audio can be sent immediately.
	videoStarted := videoFormat == nil

	switch forma := videoFormat.(type) {
	case *format.H264:
		setupH264(videoMedia, forma, r, w, &videoStarted)

	case *format.H265:
		setupH265(videoMedia, forma, r, w, &videoStarted)
	}

	switch forma := audioFormat.(type) {
	case *format.MPEG4Audio:
		setupMPEG4Audio(audioMedia, forma, r, w, &videoStarted)

	case *format.G711:
		setupG711(audioMedia, forma, r, w, &videoStarted)
	}

	return nil
}

func setupH264(
	media *description.Media,
	forma *format.H264,
	r *stream.Reader,
	w *Writer,
	videoStarted *bool,
) {
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

			// parameters are not always provided by the source description
			// (for instance with MPEG-TS and GB28181 streams),
			// therefore they are extracted from the stream too.
			case h264.NALUTypeSPS:
				sps = nalu

			case h264.NALUTypePPS:
				pps = nalu
			}
		}

		if len(sps) == 0 || len(pps) == 0 {
			return nil
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

			cfgErr = w.writeVideoTag(dtsDur, configBody)
			if cfgErr != nil {
				return cfgErr
			}

			headerSent = true
		}

		*videoStarted = true

		body, err := h264AU(au, idrPresent, ptsDur-dtsDur)
		if err != nil {
			return err
		}

		return w.writeVideoTag(dtsDur, body)
	})
}

func setupH265(
	media *description.Media,
	forma *format.H265,
	r *stream.Reader,
	w *Writer,
	videoStarted *bool,
) {
	vps, sps, pps := forma.VPS, forma.SPS, forma.PPS

	var dtsExtractor *h265.DTSExtractor
	headerSent := false

	r.OnData(media, forma, func(u *unit.Unit) error {
		if u.NilPayload() {
			return nil
		}

		au := u.Payload.(unit.PayloadH265) //nolint:forcetypeassert

		// parameters are not always provided by the source description
		// (for instance with MPEG-TS and GB28181 streams),
		// therefore they are extracted from the stream too.
		for _, nalu := range au {
			switch h265.NALUType((nalu[0] >> 1) & 0b111111) {
			case h265.NALUType_VPS_NUT:
				vps = nalu

			case h265.NALUType_SPS_NUT:
				sps = nalu

			case h265.NALUType_PPS_NUT:
				pps = nalu
			}
		}

		if len(vps) == 0 || len(sps) == 0 || len(pps) == 0 {
			return nil
		}

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

			cfgErr = w.writeVideoTag(dtsDur, configBody)
			if cfgErr != nil {
				return cfgErr
			}

			headerSent = true
		}

		*videoStarted = true

		body, err := h265AU(au, random, ptsDur-dtsDur)
		if err != nil {
			return err
		}

		return w.writeVideoTag(dtsDur, body)
	})
}

func setupMPEG4Audio(
	media *description.Media,
	forma *format.MPEG4Audio,
	r *stream.Reader,
	w *Writer,
	videoStarted *bool,
) {
	config := forma.Config
	headerSent := false

	r.OnData(media, forma, func(u *unit.Unit) error {
		if u.NilPayload() || !*videoStarted {
			return nil
		}

		if !headerSent {
			configBody, cfgErr := aacSequenceHeader(config)
			if cfgErr != nil {
				return cfgErr
			}

			cfgErr = w.writeAudioTag(timestampToDuration(u.PTS, forma.ClockRate()), configBody)
			if cfgErr != nil {
				return cfgErr
			}

			headerSent = true
		}

		for i, au := range u.Payload.(unit.PayloadMPEG4Audio) { //nolint:forcetypeassert
			pts := u.PTS + int64(i)*mpeg4audio.SamplesPerAccessUnit

			err := w.writeAudioTag(timestampToDuration(pts, forma.ClockRate()), aacAU(au))
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func setupG711(
	media *description.Media,
	forma *format.G711,
	r *stream.Reader,
	w *Writer,
	videoStarted *bool,
) {
	r.OnData(media, forma, func(u *unit.Unit) error {
		if u.NilPayload() || !*videoStarted {
			return nil
		}

		return w.writeAudioTag(
			timestampToDuration(u.PTS, forma.ClockRate()),
			g711Samples(u.Payload.(unit.PayloadG711), forma.MULaw, forma.ChannelCount)) //nolint:forcetypeassert
	})
}

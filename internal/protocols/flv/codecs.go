package flv

import (
	"bytes"
	"fmt"
	"time"

	"github.com/abema/go-mp4"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
)

const (
	videoCodecH264 = 7

	avcPacketTypeSequenceHeader = 0
	avcPacketTypeNALU           = 1

	frameTypeKey   = 1
	frameTypeInter = 2

	// enhanced (extended) FLV video tag header, used for HEVC.
	// see https://github.com/veovera/enhanced-rtmp
	videoExIsExHeader              = 0x80
	videoExPacketTypeSequenceStart = 0x00
	videoExPacketTypeCodedFrames   = 0x01

	audioCodecPCMA       = 7
	audioCodecPCMU       = 8
	audioCodecMPEG4Audio = 10

	audioRate5512  = 0
	audioRate44100 = 3

	audioDepth16 = 1

	aacPacketTypeSequenceHeader = 0
	aacPacketTypeAU             = 1
)

var videoFourCCHEVC = [4]byte{'h', 'v', 'c', '1'}

func compositionTime(ptsDelta time.Duration) int32 {
	return int32(ptsDelta / time.Millisecond)
}

// h264DecoderConfig builds a legacy (codec ID 7) AVC sequence header tag payload.
func h264DecoderConfig(sps, pps []byte) ([]byte, error) {
	if len(sps) < 4 {
		return nil, fmt.Errorf("invalid SPS")
	}

	var parsedSPS h264.SPS
	err := parsedSPS.Unmarshal(sps)
	if err != nil {
		return nil, fmt.Errorf("invalid SPS: %w", err)
	}

	conf := &mp4.AVCDecoderConfiguration{
		AnyTypeBox:                 mp4.AnyTypeBox{Type: mp4.BoxTypeAvcC()},
		ConfigurationVersion:       1,
		Profile:                    parsedSPS.ProfileIdc,
		ProfileCompatibility:       sps[2],
		Level:                      parsedSPS.LevelIdc,
		Reserved:                   0b111111,
		LengthSizeMinusOne:         3,
		Reserved2:                  0b111,
		NumOfSequenceParameterSets: 1,
		SequenceParameterSets: []mp4.AVCParameterSet{{
			Length:  uint16(len(sps)),
			NALUnit: sps,
		}},
		NumOfPictureParameterSets: 1,
		PictureParameterSets: []mp4.AVCParameterSet{{
			Length:  uint16(len(pps)),
			NALUnit: pps,
		}},
	}

	var buf bytes.Buffer
	_, err = mp4.Marshal(&buf, conf, mp4.Context{})
	if err != nil {
		return nil, err
	}
	record := buf.Bytes()

	body := make([]byte, 5+len(record))
	body[0] = (frameTypeKey << 4) | videoCodecH264
	body[1] = avcPacketTypeSequenceHeader
	// body[2:5] = composition time, always zero for the sequence header
	copy(body[5:], record)

	return body, nil
}

// h264AU builds a legacy (codec ID 7) AVC NALU tag payload.
func h264AU(au [][]byte, isKeyFrame bool, ptsDelta time.Duration) ([]byte, error) {
	payload, err := h264.AVCC(au).Marshal()
	if err != nil {
		return nil, err
	}

	ft := uint8(frameTypeInter)
	if isKeyFrame {
		ft = frameTypeKey
	}

	ct := compositionTime(ptsDelta)

	body := make([]byte, 5+len(payload))
	body[0] = (ft << 4) | videoCodecH264
	body[1] = avcPacketTypeNALU
	body[2] = byte(ct >> 16)
	body[3] = byte(ct >> 8)
	body[4] = byte(ct)
	copy(body[5:], payload)

	return body, nil
}

// h265DecoderConfig builds an Enhanced FLV (fourCC "hvc1") sequence start tag payload.
func h265DecoderConfig(vps, sps, pps []byte) ([]byte, error) {
	if len(sps) < 13 {
		return nil, fmt.Errorf("invalid SPS")
	}

	var parsedSPS h265.SPS
	err := parsedSPS.Unmarshal(sps)
	if err != nil {
		return nil, fmt.Errorf("invalid SPS: %w", err)
	}

	conf := &mp4.HvcC{
		ConfigurationVersion:        1,
		GeneralProfileIdc:           parsedSPS.ProfileTierLevel.GeneralProfileIdc,
		GeneralProfileCompatibility: parsedSPS.ProfileTierLevel.GeneralProfileCompatibilityFlag,
		GeneralConstraintIndicator: [6]uint8{
			sps[7], sps[8], sps[9], sps[10], sps[11], sps[12],
		},
		GeneralLevelIdc:      parsedSPS.ProfileTierLevel.GeneralLevelIdc,
		Reserved1:            0b1111,
		Reserved2:            0b111111,
		Reserved3:            0b111111,
		ChromaFormatIdc:      uint8(parsedSPS.ChromaFormatIdc),
		Reserved4:            0b11111,
		BitDepthLumaMinus8:   uint8(parsedSPS.BitDepthLumaMinus8),
		Reserved5:            0b11111,
		BitDepthChromaMinus8: uint8(parsedSPS.BitDepthChromaMinus8),
		NumTemporalLayers:    1,
		LengthSizeMinusOne:   3,
		NumOfNaluArrays:      3,
		NaluArrays: []mp4.HEVCNaluArray{
			{
				NaluType: byte(h265.NALUType_VPS_NUT),
				NumNalus: 1,
				Nalus:    []mp4.HEVCNalu{{Length: uint16(len(vps)), NALUnit: vps}},
			},
			{
				NaluType: byte(h265.NALUType_SPS_NUT),
				NumNalus: 1,
				Nalus:    []mp4.HEVCNalu{{Length: uint16(len(sps)), NALUnit: sps}},
			},
			{
				NaluType: byte(h265.NALUType_PPS_NUT),
				NumNalus: 1,
				Nalus:    []mp4.HEVCNalu{{Length: uint16(len(pps)), NALUnit: pps}},
			},
		},
	}

	var buf bytes.Buffer
	_, err = mp4.Marshal(&buf, conf, mp4.Context{})
	if err != nil {
		return nil, err
	}
	record := buf.Bytes()

	body := make([]byte, 5+len(record))
	body[0] = videoExIsExHeader | videoExPacketTypeSequenceStart
	copy(body[1:5], videoFourCCHEVC[:])
	copy(body[5:], record)

	return body, nil
}

// as per specification, rate, depth and channel count of AAC tags are fixed:
// real values are stored inside the AudioSpecificConfig.
const aacTagHeader = byte((audioCodecMPEG4Audio << 4) | (audioRate44100 << 2) | (audioDepth16 << 1) | 1)

// aacSequenceHeader builds an AAC sequence header tag payload.
func aacSequenceHeader(config *mpeg4audio.AudioSpecificConfig) ([]byte, error) {
	enc, err := config.Marshal()
	if err != nil {
		return nil, err
	}

	body := make([]byte, 2+len(enc))
	body[0] = aacTagHeader
	body[1] = aacPacketTypeSequenceHeader
	copy(body[2:], enc)

	return body, nil
}

// aacAU builds an AAC access unit tag payload.
func aacAU(au []byte) []byte {
	body := make([]byte, 2+len(au))
	body[0] = aacTagHeader
	body[1] = aacPacketTypeAU
	copy(body[2:], au)

	return body
}

// g711Samples builds a G711 (A-law or mu-law) tag payload.
func g711Samples(samples []byte, muLaw bool, channelCount int) []byte {
	codec := byte(audioCodecPCMA)
	if muLaw {
		codec = audioCodecPCMU
	}

	// FLV is not able to represent the 8000 Hz rate of G711;
	// the lowest available value is used, like other implementations do.
	header := (codec << 4) | (audioRate5512 << 2) | (audioDepth16 << 1)
	if channelCount == 2 {
		header |= 1
	}

	body := make([]byte, 1+len(samples))
	body[0] = header
	copy(body[1:], samples)

	return body
}

// h265AU builds an Enhanced FLV (fourCC "hvc1") coded-frames tag payload.
func h265AU(au [][]byte, isKeyFrame bool, ptsDelta time.Duration) ([]byte, error) {
	// NALU length-prefixing is codec-agnostic; h264.AVCC works for HEVC too.
	payload, err := h264.AVCC(au).Marshal()
	if err != nil {
		return nil, err
	}

	ft := uint8(frameTypeInter)
	if isKeyFrame {
		ft = frameTypeKey
	}

	ct := compositionTime(ptsDelta)

	body := make([]byte, 8+len(payload))
	body[0] = videoExIsExHeader | (ft << 4) | videoExPacketTypeCodedFrames
	copy(body[1:5], videoFourCCHEVC[:])
	body[5] = byte(ct >> 16)
	body[6] = byte(ct >> 8)
	body[7] = byte(ct)
	copy(body[8:], payload)

	return body, nil
}

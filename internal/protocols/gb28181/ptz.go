package gb28181

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// front-end command codes, as defined by GB/T 28181 appendix A.3.1.
const (
	ptzCmdStop    = 0x00
	ptzCmdRight   = 0x01
	ptzCmdLeft    = 0x02
	ptzCmdDown    = 0x04
	ptzCmdUp      = 0x08
	ptzCmdZoomIn  = 0x10
	ptzCmdZoomOut = 0x20

	fiCmdFocusFar  = 0x41
	fiCmdFocusNear = 0x42
	fiCmdIrisIn    = 0x44
	fiCmdIrisOut   = 0x48

	presetCmdSet    = 0x81
	presetCmdGoto   = 0x82
	presetCmdRemove = 0x83
)

// PTZCommand is a pan-tilt-zoom command.
type PTZCommand string

// PTZ commands.
const (
	PTZCommandStop         PTZCommand = "stop"
	PTZCommandUp           PTZCommand = "up"
	PTZCommandDown         PTZCommand = "down"
	PTZCommandLeft         PTZCommand = "left"
	PTZCommandRight        PTZCommand = "right"
	PTZCommandUpLeft       PTZCommand = "upleft"
	PTZCommandUpRight      PTZCommand = "upright"
	PTZCommandDownLeft     PTZCommand = "downleft"
	PTZCommandDownRight    PTZCommand = "downright"
	PTZCommandZoomIn       PTZCommand = "zoomin"
	PTZCommandZoomOut      PTZCommand = "zoomout"
	PTZCommandFocusFar     PTZCommand = "focusfar"
	PTZCommandFocusNear    PTZCommand = "focusnear"
	PTZCommandIrisIn       PTZCommand = "irisin"
	PTZCommandIrisOut      PTZCommand = "irisout"
	PTZCommandPresetSet    PTZCommand = "presetset"
	PTZCommandPresetGoto   PTZCommand = "presetgoto"
	PTZCommandPresetRemove PTZCommand = "presetremove"
)

var reRawPTZCommand = regexp.MustCompile(`^[0-9A-Fa-f]{16}$`)

// EncodePTZCommand encodes a PTZ command into the 8-byte command word
// defined by GB/T 28181 appendix A.3.1, returned in hexadecimal form.
//
// speed is the movement speed (0-255), preset is the preset number (0-255).
// Both are ignored by commands that don't use them.
func EncodePTZCommand(cmd PTZCommand, speed uint8, preset uint8) (string, error) {
	var cmdCode byte
	var param1 byte
	var param2 byte
	var param3 byte

	switch cmd {
	case PTZCommandStop:
		cmdCode = ptzCmdStop

	case PTZCommandUp:
		cmdCode = ptzCmdUp
		param2 = speed

	case PTZCommandDown:
		cmdCode = ptzCmdDown
		param2 = speed

	case PTZCommandLeft:
		cmdCode = ptzCmdLeft
		param1 = speed

	case PTZCommandRight:
		cmdCode = ptzCmdRight
		param1 = speed

	case PTZCommandUpLeft:
		cmdCode = ptzCmdUp | ptzCmdLeft
		param1 = speed
		param2 = speed

	case PTZCommandUpRight:
		cmdCode = ptzCmdUp | ptzCmdRight
		param1 = speed
		param2 = speed

	case PTZCommandDownLeft:
		cmdCode = ptzCmdDown | ptzCmdLeft
		param1 = speed
		param2 = speed

	case PTZCommandDownRight:
		cmdCode = ptzCmdDown | ptzCmdRight
		param1 = speed
		param2 = speed

	case PTZCommandZoomIn:
		cmdCode = ptzCmdZoomIn
		param3 = speed

	case PTZCommandZoomOut:
		cmdCode = ptzCmdZoomOut
		param3 = speed

	case PTZCommandFocusFar:
		cmdCode = fiCmdFocusFar
		param1 = speed

	case PTZCommandFocusNear:
		cmdCode = fiCmdFocusNear
		param1 = speed

	case PTZCommandIrisIn:
		cmdCode = fiCmdIrisIn
		param2 = speed

	case PTZCommandIrisOut:
		cmdCode = fiCmdIrisOut
		param2 = speed

	case PTZCommandPresetSet:
		cmdCode = presetCmdSet
		param2 = preset

	case PTZCommandPresetGoto:
		cmdCode = presetCmdGoto
		param2 = preset

	case PTZCommandPresetRemove:
		cmdCode = presetCmdRemove
		param2 = preset

	default:
		return "", fmt.Errorf("invalid PTZ command: '%s'", cmd)
	}

	// the zoom speed is stored in the high nibble of the 7th byte.
	byte6 := (param3 & 0xF0)

	buf := []byte{0xA5, 0x0F, 0x01, cmdCode, param1, param2, byte6, 0}

	var checksum uint16
	for _, b := range buf[:7] {
		checksum += uint16(b)
	}
	buf[7] = byte(checksum % 256)

	return strings.ToUpper(hex.EncodeToString(buf)), nil
}

// IsRawPTZCommand returns whether a string is a raw PTZ command word,
// that can be passed to devices as-is.
func IsRawPTZCommand(v string) bool {
	return reRawPTZCommand.MatchString(v)
}

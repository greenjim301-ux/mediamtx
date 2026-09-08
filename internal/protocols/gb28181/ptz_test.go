package gb28181

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodePTZCommand(t *testing.T) {
	for _, ca := range []struct {
		name     string
		cmd      PTZCommand
		speed    uint8
		preset   uint8
		expected string
	}{
		{"stop", PTZCommandStop, 0, 0, "A50F010000000000"},
		{"up", PTZCommandUp, 0x64, 0, "A50F010800640000"},
		{"down", PTZCommandDown, 0x64, 0, "A50F010400640000"},
		{"left", PTZCommandLeft, 0x64, 0, "A50F010264000000"},
		{"right", PTZCommandRight, 0x64, 0, "A50F010164000000"},
		{"up left", PTZCommandUpLeft, 0x10, 0, "A50F010A10100000"},
		{"zoom in", PTZCommandZoomIn, 0x40, 0, "A50F011000004000"},
		{"zoom out", PTZCommandZoomOut, 0x40, 0, "A50F012000004000"},
		{"focus far", PTZCommandFocusFar, 0x20, 0, "A50F014120000000"},
		{"iris in", PTZCommandIrisIn, 0x20, 0, "A50F014400200000"},
		{"preset goto", PTZCommandPresetGoto, 0, 5, "A50F018200050000"},
	} {
		t.Run(ca.name, func(t *testing.T) {
			cmd, err := EncodePTZCommand(ca.cmd, ca.speed, ca.preset)
			require.NoError(t, err)

			// verify the checksum, then compare the rest
			buf, err := hex.DecodeString(cmd)
			require.NoError(t, err)
			require.Equal(t, 8, len(buf))

			var checksum uint16
			for _, b := range buf[:7] {
				checksum += uint16(b)
			}
			require.Equal(t, byte(checksum%256), buf[7])

			require.Equal(t, ca.expected[:14], cmd[:14])
		})
	}
}

func TestEncodePTZCommandChecksum(t *testing.T) {
	// A5 + 0F + 01 + 08 + 00 + 64 + 00 = 0x121, whose low byte is 0x21
	cmd, err := EncodePTZCommand(PTZCommandUp, 0x64, 0)
	require.NoError(t, err)
	require.Equal(t, "A50F010800640021", cmd)
}

func TestEncodePTZCommandInvalid(t *testing.T) {
	_, err := EncodePTZCommand("nonexistent", 0, 0)
	require.Error(t, err)
}

func TestIsRawPTZCommand(t *testing.T) {
	require.True(t, IsRawPTZCommand("A50F010800640021"))
	require.True(t, IsRawPTZCommand("a50f010800640021"))
	require.False(t, IsRawPTZCommand("A50F0108006400"))
	require.False(t, IsRawPTZCommand("not hex at all!!"))
}

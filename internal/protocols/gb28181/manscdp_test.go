package gb28181

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeKeepalive(t *testing.T) {
	msg, err := DecodeMessage([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Notify>
<CmdType>Keepalive</CmdType>
<SN>123</SN>
<DeviceID>34020000001110000001</DeviceID>
<Status>OK</Status>
</Notify>
`))
	require.NoError(t, err)
	require.True(t, msg.IsNotify())
	require.False(t, msg.IsResponse())
	require.Equal(t, CmdTypeKeepalive, msg.CmdType)
	require.Equal(t, 123, msg.SN)
	require.Equal(t, "34020000001110000001", msg.DeviceID)
	require.Equal(t, "OK", msg.Status)
}

func TestDecodeCatalog(t *testing.T) {
	msg, err := DecodeMessage([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
<CmdType>Catalog</CmdType>
<SN>17</SN>
<DeviceID>34020000001110000001</DeviceID>
<SumNum>2</SumNum>
<DeviceList Num="2">
<Item>
<DeviceID>34020000001320000001</DeviceID>
<Name>Camera 1</Name>
<Manufacturer>Hikvision</Manufacturer>
<Model>IPC</Model>
<Owner>Owner</Owner>
<CivilCode>3402000000</CivilCode>
<Address>Address 1</Address>
<Parental>0</Parental>
<ParentID>34020000001110000001</ParentID>
<RegisterWay>1</RegisterWay>
<Secrecy>0</Secrecy>
<Status>ON</Status>
<Longitude>116.3</Longitude>
<Latitude>39.9</Latitude>
<Info>
<PTZType>1</PTZType>
</Info>
</Item>
<Item>
<DeviceID>34020000001320000002</DeviceID>
<Name>Camera 2</Name>
<Status>OFF</Status>
</Item>
</DeviceList>
</Response>
`))
	require.NoError(t, err)
	require.True(t, msg.IsResponse())
	require.Equal(t, CmdTypeCatalog, msg.CmdType)
	require.Equal(t, 2, msg.SumNum)
	require.Equal(t, 2, len(msg.DeviceList.Items))

	item := msg.DeviceList.Items[0]
	require.Equal(t, "34020000001320000001", item.DeviceID)
	require.Equal(t, "Camera 1", item.Name)
	require.Equal(t, "Hikvision", item.Manufacturer)
	require.Equal(t, 1, item.Info.PTZType)
	require.Equal(t, 116.3, item.Longitude)
	require.True(t, item.IsOnline())

	require.False(t, msg.DeviceList.Items[1].IsOnline())
}

func TestDecodeCatalogGB2312(t *testing.T) {
	// devices commonly encode chinese names with GB2312
	body := `<?xml version="1.0" encoding="GB2312"?>
<Response>
<CmdType>Catalog</CmdType>
<SN>1</SN>
<DeviceID>34020000001110000001</DeviceID>
<DeviceList Num="1">
<Item>
<DeviceID>34020000001320000001</DeviceID>
<Name>前门摄像机</Name>
</Item>
</DeviceList>
</Response>
`

	enc, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(body))
	require.NoError(t, err)

	msg, err := DecodeMessage(enc)
	require.NoError(t, err)
	require.Equal(t, "前门摄像机", msg.DeviceList.Items[0].Name)
}

func TestDecodeDeviceInfo(t *testing.T) {
	msg, err := DecodeMessage([]byte(`<?xml version="1.0"?>
<Response>
<CmdType>DeviceInfo</CmdType>
<SN>2</SN>
<DeviceID>34020000001110000001</DeviceID>
<DeviceType>DVR</DeviceType>
<Manufacturer>Dahua</Manufacturer>
<Model>NVR</Model>
<Firmware>V1.0</Firmware>
<Channel>16</Channel>
<Result>OK</Result>
</Response>
`))
	require.NoError(t, err)
	require.Equal(t, CmdTypeDeviceInfo, msg.CmdType)
	require.Equal(t, "DVR", msg.DeviceType)
	require.Equal(t, "Dahua", msg.Manufacturer)
	require.Equal(t, 16, msg.Channel)
}

func TestDecodeMessageErrors(t *testing.T) {
	_, err := DecodeMessage(nil)
	require.Error(t, err)

	_, err = DecodeMessage([]byte("not xml at all"))
	require.Error(t, err)
}

func TestEncodeQuery(t *testing.T) {
	buf := EncodeQuery(CmdTypeCatalog, 42, "34020000001110000001")
	require.Contains(t, string(buf), "<CmdType>Catalog</CmdType>")
	require.Contains(t, string(buf), "<SN>42</SN>")
	require.Contains(t, string(buf), "<DeviceID>34020000001110000001</DeviceID>")

	// it must be decodable
	msg, err := DecodeMessage(buf)
	require.NoError(t, err)
	require.Equal(t, CmdTypeCatalog, msg.CmdType)
	require.Equal(t, 42, msg.SN)
}

func TestEncodeDeviceControlPTZ(t *testing.T) {
	buf := EncodeDeviceControlPTZ(7, "34020000001320000001", "A50F0108640000D8")

	msg, err := DecodeMessage(buf)
	require.NoError(t, err)
	require.Equal(t, CmdTypeDeviceControl, msg.CmdType)
	require.Equal(t, 7, msg.SN)
	require.Contains(t, string(buf), "<PTZCmd>A50F0108640000D8</PTZCmd>")
}

func TestEncodeResponse(t *testing.T) {
	buf := EncodeResponse(CmdTypeKeepalive, 3, "34020000001110000001")
	msg, err := DecodeMessage(buf)
	require.NoError(t, err)
	require.True(t, msg.IsResponse())
	require.Equal(t, 3, msg.SN)
}

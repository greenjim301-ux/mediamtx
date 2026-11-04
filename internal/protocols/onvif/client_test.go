package onvif

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWSSecurity(t *testing.T) {
	header := GenerateWSSecurity("admin", "password123")
	require.Contains(t, header, "<wsse:Username>admin</wsse:Username>")
	require.Contains(t, header, "PasswordDigest")
	require.Contains(t, header, "<wsse:Nonce>")
	require.Contains(t, header, "<wsu:Created>")

	soap := WrapSOAP("admin", "password123", "<TestBody/>")
	require.Contains(t, soap, "<TestBody/>")
	require.Contains(t, soap, "Envelope")
}

func TestParseXAddrs(t *testing.T) {
	xmlData := []byte(`<?xml version="1.0" encoding="utf-8"?>
<Envelope xmlns="http://www.w3.org/2003/05/soap-envelope">
  <Body>
    <ProbeMatches xmlns="http://schemas.xmlsoap.org/ws/2005/04/discovery">
      <ProbeMatch>
        <XAddrs>http://192.168.1.100:80/onvif/device_service http://192.168.1.100:8000/onvif/device_service</XAddrs>
      </ProbeMatch>
    </ProbeMatches>
  </Body>
</Envelope>`)

	urls := ParseXAddrs(xmlData)
	require.Equal(t, []string{
		"http://192.168.1.100:80/onvif/device_service",
		"http://192.168.1.100:8000/onvif/device_service",
	}, urls)
}

func TestEmbedCredentials(t *testing.T) {
	res := embedCredentials("rtsp://192.168.1.100:554/live/ch0", "admin", "pass123")
	require.Equal(t, "rtsp://admin:pass123@192.168.1.100:554/live/ch0", res)

	// Already has credentials
	res2 := embedCredentials("rtsp://user:pass@192.168.1.100:554/live/ch0", "admin", "pass123")
	require.Equal(t, "rtsp://user:pass@192.168.1.100:554/live/ch0", res2)
}

func TestMockONVIFServer(t *testing.T) {
	var receivedMoveBody string
	var receivedStopBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		reqStr := string(buf[:n])

		switch {
		case strings.Contains(reqStr, "GetServices"):
			w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <GetServicesResponse xmlns="http://www.onvif.org/ver10/device/wsdl">
      <Service>
        <Namespace>http://www.onvif.org/ver10/media/wsdl</Namespace>
        <XAddr>` + "http://" + r.Host + `/onvif/media` + `</XAddr>
      </Service>
      <Service>
        <Namespace>http://www.onvif.org/ver20/ptz/wsdl</Namespace>
        <XAddr>` + "http://" + r.Host + `/onvif/ptz` + `</XAddr>
      </Service>
    </GetServicesResponse>
  </s:Body>
</s:Envelope>`))

		case strings.Contains(reqStr, "GetProfiles"):
			w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <GetProfilesResponse xmlns="http://www.onvif.org/ver10/media/wsdl">
      <Profiles token="Profile_Main" fixed="true">
        <Name>MainStream</Name>
      </Profiles>
    </GetProfilesResponse>
  </s:Body>
</s:Envelope>`))

		case strings.Contains(reqStr, "GetStreamUri"):
			w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <GetStreamUriResponse xmlns="http://www.onvif.org/ver10/media/wsdl">
      <MediaUri>
        <Uri>rtsp://192.168.1.100:554/Streaming/Channels/101</Uri>
      </MediaUri>
    </GetStreamUriResponse>
  </s:Body>
</s:Envelope>`))

		case strings.Contains(reqStr, "ContinuousMove"):
			receivedMoveBody = reqStr
			w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body><ContinuousMoveResponse xmlns="http://www.onvif.org/ver20/ptz/wsdl"/></s:Body>
</s:Envelope>`))

		case strings.Contains(reqStr, "Stop"):
			receivedStopBody = reqStr
			w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body><StopResponse xmlns="http://www.onvif.org/ver20/ptz/wsdl"/></s:Body>
</s:Envelope>`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	cli := NewClient()

	// Test discover using the test server URL as hostOrIP
	// Note: ts.URL is http://127.0.0.1:xxxx
	hostPort := strings.TrimPrefix(ts.URL, "http://")
	res, err := cli.Discover(context.Background(), hostPort, "admin", "123456")
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "Profile_Main", res.ProfileToken)
	require.True(t, res.HasPTZ)
	require.Equal(t, "rtsp://admin:123456@192.168.1.100:554/Streaming/Channels/101", res.RTSPURL)

	// Test PTZ ContinuousMove
	err = cli.ContinuousMove(context.Background(), res.PTZURL, res.ProfileToken, "admin", "123456", 0.5, 0.0, 0.0)
	require.NoError(t, err)
	require.Contains(t, receivedMoveBody, "ContinuousMove")
	require.Contains(t, receivedMoveBody, `x="0.5000"`)

	// Test PTZ Stop
	err = cli.Stop(context.Background(), res.PTZURL, res.ProfileToken, "admin", "123456", true, true)
	require.NoError(t, err)
	require.Contains(t, receivedStopBody, "Stop")
	require.Contains(t, receivedStopBody, "<PanTilt>true</PanTilt>")
}

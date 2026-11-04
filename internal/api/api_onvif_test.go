package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/test"
)

func TestAPIOnvifProbeAndPTZ(t *testing.T) {
	// Mock ONVIF device HTTP server
	var lastPtzBody string
	onvifServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
        <Uri>rtsp://192.168.1.50:554/live/ch0</Uri>
      </MediaUri>
    </GetStreamUriResponse>
  </s:Body>
</s:Envelope>`))

		case strings.Contains(reqStr, "ContinuousMove") || strings.Contains(reqStr, "Stop"):
			lastPtzBody = reqStr
			w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body><Response/></s:Body>
</s:Envelope>`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer onvifServer.Close()

	hostPort := strings.TrimPrefix(onvifServer.URL, "http://")

	cnf := tempConf(t, "api: yes\n"+
		"paths:\n"+
		"  onvif_cam:\n"+
		"    source: rtsp://admin:pass@192.168.1.50:554/live/ch0\n"+
		"    onvifDeviceIP: "+hostPort+"\n"+
		"    onvifUser: admin\n"+
		"    onvifPass: pass\n"+
		"    onvifProfileToken: Profile_Main\n"+
		"    onvifPTZURL: "+onvifServer.URL+"/onvif/ptz\n")

	api := API{
		Address:      "localhost:9997",
		ReadTimeout:  conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second),
		AuthManager:  test.NilAuthManager,
		Parent:       &testParent{conf: cnf},
	}
	err := api.Initialize()
	require.NoError(t, err)
	defer api.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	// 1. Test POST /v3/onvif/probe
	probeReq := ONVIFProbeRequest{
		IP:   hostPort,
		User: "admin",
		Pass: "pass",
	}
	probeBody, _ := json.Marshal(probeReq)
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v3/onvif/probe", bytes.NewReader(probeBody))
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	var probeRes map[string]any
	err = json.NewDecoder(res.Body).Decode(&probeRes)
	require.NoError(t, err)
	require.Equal(t, "Profile_Main", probeRes["profileToken"])
	require.Equal(t, true, probeRes["hasPtz"])
	require.Equal(t, "rtsp://admin:pass@192.168.1.50:554/live/ch0", probeRes["rtspUrl"])

	// 2. Test POST /v3/onvif/ptz/onvif_cam (ContinuousMove: up)
	ptzReq := ONVIFPTZRequest{
		Command: "up",
	}
	ptzBody, _ := json.Marshal(ptzReq)
	req2, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v3/onvif/ptz/onvif_cam", bytes.NewReader(ptzBody))
	req2.Header.Set("Content-Type", "application/json")
	res2, err := hc.Do(req2)
	require.NoError(t, err)
	defer res2.Body.Close()
	require.Equal(t, http.StatusOK, res2.StatusCode)
	require.Contains(t, lastPtzBody, "ContinuousMove")
	require.Contains(t, lastPtzBody, `y="0.5000"`)

	// 3. Test POST /v3/onvif/ptz/onvif_cam (Stop)
	ptzStopReq := ONVIFPTZRequest{
		Command: "stop",
	}
	ptzStopBody, _ := json.Marshal(ptzStopReq)
	req3, _ := http.NewRequest(http.MethodPost, "http://localhost:9997/v3/onvif/ptz/onvif_cam", bytes.NewReader(ptzStopBody))
	req3.Header.Set("Content-Type", "application/json")
	res3, err := hc.Do(req3)
	require.NoError(t, err)
	defer res3.Body.Close()
	require.Equal(t, http.StatusOK, res3.StatusCode)
	require.Contains(t, lastPtzBody, "Stop")
}

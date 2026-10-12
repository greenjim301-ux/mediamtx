package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/stretchr/testify/require"
)

const managementTestConfig = "api: true\napiAddress: 127.0.0.1:19997\nrtspAddress: 127.0.0.1:19554\nrtspTransports: [tcp]\nrtmp: false\nhls: false\nwebrtc: false\nsrt: false\nmoq: false\nhttpflv: true\nhttpflvAddress: 127.0.0.1:19891\ngb28181: false\n# Keep this secret\ngb28181Password: original-secret\npaths:\n  all_others:\n"

func managementRequest(t *testing.T, change string, status int) {
	t.Helper()
	res, err := http.Post("http://127.0.0.1:19997/v3/management/apply", "application/json", bytes.NewBufferString(change))
	require.NoError(t, err)
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, status, res.StatusCode, string(b))
}

func TestManagementPersistence(t *testing.T) {
	p, ok := newInstance(t, managementTestConfig)
	require.True(t, ok)
	t.Cleanup(func() {
		if p != nil {
			p.Close()
		}
	})
	path := p.confPath
	managementRequest(t, `{"action":"add","name":"camera/front","path":{"source":"rtsp://user:secret@127.0.0.1:19555/stream","sourceOnDemand":true}}`, 200)
	require.Eventually(t, func() bool { _, exists := p.APIConfigSnapshot().Paths["camera/front"]; return exists }, 3*time.Second, 20*time.Millisecond)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "# Keep this secret")
	require.Contains(t, string(raw), "original-secret")
	onDisk, _, err := conf.Load(path, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "rtsp://user:secret@127.0.0.1:19555/stream", onDisk.Paths["camera/front"].Source)
	// Failed validation and duplicate sources must not change the file.
	managementRequest(t, `{"action":"add","name":"bad","path":{"source":"http://example.com/stream"}}`, 400)
	managementRequest(t, `{"action":"add","name":"camera/front","path":{"source":"rtsp://127.0.0.1/other"}}`, 400)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, unchanged)
	managementRequest(t, `{"action":"global","global":{"gb28181MediaPortRange":[30000,29999]}}`, 400)
	managementRequest(t, `{"action":"global","global":{"gb28181Realm":"4401000000"}}`, 200)
	require.Eventually(t, func() bool { return p.APIConfigSnapshot().GB28181Realm == "4401000000" }, 3*time.Second, 20*time.Millisecond)
	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), "original-secret")
	p.Close()
	p = nil
	// Cold restart must preserve both the source and the global change.
	p, ok = New([]string{path})
	require.True(t, ok)
	require.Equal(t, "4401000000", p.APIConfigSnapshot().GB28181Realm)
	require.Contains(t, p.APIConfigSnapshot().Paths, "camera/front")
	managementRequest(t, `{"action":"delete","name":"camera/front"}`, 200)
	require.Eventually(t, func() bool { _, exists := p.APIConfigSnapshot().Paths["camera/front"]; return !exists }, 3*time.Second, 20*time.Millisecond)
	onDisk, _, err = conf.Load(path, nil, nil)
	require.NoError(t, err)
	require.NotContains(t, onDisk.Paths, "camera/front")
}

func TestManagementUIAndPreview(t *testing.T) {
	source, err := filepath.Abs("../staticsources/file/testdata/h264.mp4")
	require.NoError(t, err)
	p, ok := newInstance(t, managementTestConfig)
	require.True(t, ok)
	defer p.Close()
	body, err := json.Marshal(map[string]any{"action": "add", "name": "files/demo", "path": map[string]any{"source": "file://" + source}})
	require.NoError(t, err)
	managementRequest(t, string(body), 200)
	require.Eventually(t, func() bool { _, exists := p.APIConfigSnapshot().Paths["files/demo"]; return exists }, 3*time.Second, 20*time.Millisecond)
	res, err := http.Get("http://127.0.0.1:19997/admin/")
	require.NoError(t, err)
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 200, res.StatusCode)
	require.Contains(t, string(b), "MediaMTX")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:19997/v3/preview/files/demo.flv", nil)
	require.NoError(t, err)
	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	header := make([]byte, 13)
	_, err = io.ReadFull(res.Body, header)
	require.NoError(t, err)
	require.Equal(t, []byte("FLV"), header[:3])
	require.Equal(t, byte(1), header[4])
}

func TestManagementAuthorization(t *testing.T) {
	cfg := managementTestConfig + "authInternalUsers:\n- user: admin\n  pass: test-password\n  permissions:\n  - action: api\n"
	p, ok := newInstance(t, cfg)
	require.True(t, ok)
	defer p.Close()
	managementRequest(t, `{"action":"delete","name":"all_others"}`, 401)
	res, err := http.Get("http://127.0.0.1:19997/admin/")
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	req, err := http.NewRequest("GET", "http://127.0.0.1:19997/v3/preview/missing.flv", nil)
	require.NoError(t, err)
	req.SetBasicAuth("admin", "test-password")
	res, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, 401, res.StatusCode, "API permission must not bypass stream read permission")
}

func TestManagementEnableGB28181(t *testing.T) {
	p, ok := newInstance(t, managementTestConfig)
	require.True(t, ok)
	defer p.Close()
	managementRequest(t, `{"action":"global","global":{"gb28181":true,"gb28181Address":"127.0.0.1:19560","gb28181SIPIP":"127.0.0.1"}}`, 200)
	require.Eventually(t, func() bool {
		res, err := http.Get("http://127.0.0.1:19997/v3/gb28181/devices/list")
		if err != nil {
			return false
		}
		defer res.Body.Close()
		return res.StatusCode == 200
	}, 5*time.Second, 30*time.Millisecond)
	require.True(t, p.APIConfigSnapshot().GB28181)
	managementRequest(t, `{"action":"global","global":{"gb28181":false}}`, 200)
	require.Eventually(t, func() bool { return !p.APIConfigSnapshot().GB28181 }, 3*time.Second, 20*time.Millisecond)
}

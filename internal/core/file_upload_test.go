package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/fileurl"
	"github.com/stretchr/testify/require"
)

func postVideo(t *testing.T, name, filename string, data []byte, extra bool, status int) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	if extra {
		require.NoError(t, writer.WriteField("extra", "invalid"))
	}
	require.NoError(t, writer.Close())
	res, err := http.Post("http://127.0.0.1:19997/v3/management/files/upload/"+name, writer.FormDataContentType(), &body)
	require.NoError(t, err)
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, status, res.StatusCode, string(b))
}
func uploadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../staticsources/file/testdata/h264.mp4")
	require.NoError(t, err)
	return b
}
func uploadFiles(t *testing.T, p *Core) []os.DirEntry {
	t.Helper()
	dir, _, err := p.APIManagementUploadSettings()
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return entries
}

func TestUploadedSourceLifecycle(t *testing.T) {
	p, ok := newInstance(t, managementTestConfig+"apiFileUploadDirectory: uploads with spaces\n")
	require.True(t, ok)
	t.Cleanup(func() {
		if p != nil {
			p.Close()
		}
	})
	postVideo(t, "files/uploaded", "../../demo.mp4", uploadFixture(t), false, 200)
	require.Eventually(t, func() bool { return p.APIConfigSnapshot().Paths["files/uploaded"] != nil }, 3*time.Second, 20*time.Millisecond)
	c := p.APIConfigSnapshot().Paths["files/uploaded"]
	require.Equal(t, "demo.mp4", c.FileUploadOriginalName)
	path, err := fileurl.Path(c.Source)
	require.NoError(t, err)
	require.Equal(t, c.FileUploadDirectory, filepath.Dir(path))
	require.FileExists(t, path)
	require.Len(t, uploadFiles(t, p), 1)
	postVideo(t, "files/uploaded", "other.mp4", uploadFixture(t), false, 400)
	require.Len(t, uploadFiles(t, p), 1)
	// Directory changes apply only to subsequent uploads; old ownership remains recorded.
	managementRequest(t, `{"action":"storage","global":{"apiFileUploadDirectory":"new uploads","apiFileUploadMaxSize":"1G"}}`, 200)
	require.Eventually(t, func() bool { return p.APIConfigSnapshot().APIFileUploadDirectory == "new uploads" }, 3*time.Second, 20*time.Millisecond)
	require.FileExists(t, path)
	configPath := p.confPath
	p.Close()
	p = nil
	p, ok = New([]string{configPath})
	require.True(t, ok)
	require.Equal(t, c.FileUploadDirectory, p.APIConfigSnapshot().Paths["files/uploaded"].FileUploadDirectory)
	managementRequest(t, `{"action":"delete","name":"files/uploaded"}`, 200)
	require.NoFileExists(t, path)
	onDisk, _, err := conf.Load(configPath, nil, nil)
	require.NoError(t, err)
	require.NotContains(t, onDisk.Paths, "files/uploaded")
}

func TestManagementPreservesLegacyBooleans(t *testing.T) {
	cfg := strings.NewReplacer(
		"api: true", "api: yes",
		"rtmp: false", "rtmp: no",
		"hls: false", "hls: OFF",
		"webrtc: false", "webrtc: No",
		"srt: false", "srt: off",
		"httpflv: true", "httpflv: ON",
		"gb28181Password: original-secret", `gb28181Password: "yes"`,
		"  all_others:\n", "  ~^gb28181/(.+)$:\n    source: gb28181://$G1\n    sourceOnDemand: yes # Preserve this comment\n  all_others:\n",
	).Replace(managementTestConfig)
	p, ok := newInstance(t, cfg)
	require.True(t, ok)
	defer p.Close()
	postVideo(t, "files/legacy", "demo.mp4", uploadFixture(t), false, 200)
	managementRequest(t, `{"action":"storage","global":{"apiFileUploadDirectory":"new uploads"}}`, 200)
	managementRequest(t, `{"action":"add","name":"camera/legacy","path":{"source":"rtsp://127.0.0.1:19555/stream","sourceOnDemand":true}}`, 200)
	managementRequest(t, `{"action":"delete","name":"files/legacy"}`, 200)
	managementRequest(t, `{"action":"global","global":{"gb28181Realm":"4401000000"}}`, 200)
	require.Eventually(t, func() bool {
		return p.APIConfigSnapshot().GB28181Realm == "4401000000"
	}, 3*time.Second, 20*time.Millisecond)
	onDisk, _, err := conf.Load(p.confPath, nil, nil)
	require.NoError(t, err)
	require.True(t, onDisk.API)
	require.True(t, onDisk.HTTPFLV)
	require.False(t, onDisk.RTMP)
	require.False(t, onDisk.HLS)
	require.False(t, onDisk.WebRTC)
	require.False(t, onDisk.SRT)
	require.True(t, onDisk.Paths["~^gb28181/(.+)$"].SourceOnDemand)
	require.Equal(t, "yes", onDisk.GB28181Password)
	raw, err := os.ReadFile(p.confPath)
	require.NoError(t, err)
	require.Contains(t, string(raw), "# Preserve this comment")
}

func TestManagementFullDefaultConfiguration(t *testing.T) {
	raw, err := os.ReadFile("../../mediamtx.yml")
	require.NoError(t, err)
	cfg := strings.NewReplacer(
		"apiAddress: :9997", "apiAddress: 127.0.0.1:19997",
		"rtspAddress: :8554", "rtspAddress: 127.0.0.1:19554",
		"rtspTransports: [udp, multicast, tcp]", "rtspTransports: [tcp]",
		"httpflvAddress: :8891", "httpflvAddress: 127.0.0.1:19891",
		"rtmp: yes", "rtmp: false",
		"rtmp: true", "rtmp: false",
	).Replace(string(raw))
	p, ok := newInstance(t, cfg)
	require.True(t, ok)
	defer p.Close()
	initial, _, err := conf.Load(p.confPath, nil, nil)
	require.NoError(t, err)
	postVideo(t, "files/default-config", "demo.mp4", uploadFixture(t), false, 200)
	managementRequest(t, `{"action":"add","name":"camera/default-config","path":{"source":"rtsp://127.0.0.1:19555/stream","sourceOnDemand":true}}`, 200)
	managementRequest(t, `{"action":"storage","global":{"apiFileUploadDirectory":"new uploads","apiFileUploadMaxSize":"1G"}}`, 200)
	managementRequest(t, `{"action":"delete","name":"files/default-config"}`, 200)
	managementRequest(t, `{"action":"delete","name":"camera/default-config"}`, 200)
	managementRequest(t, `{"action":"global","global":{"gb28181MediaPortRange":[31000,31999]}}`, 200)
	require.Eventually(t, func() bool {
		return p.APIConfigSnapshot().GB28181MediaPortRange[0] == 31000
	}, 3*time.Second, 20*time.Millisecond)
	onDisk, _, err := conf.Load(p.confPath, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []uint{31000, 31999}, onDisk.GB28181MediaPortRange)
	require.Equal(t, "new uploads", onDisk.APIFileUploadDirectory)
	require.Equal(t, conf.StringSize(1024*1024*1024), onDisk.APIFileUploadMaxSize)
	// Every other field in the complete distributed configuration must survive
	// upload, add, storage, delete and numeric global changes unchanged.
	onDisk.GB28181MediaPortRange = initial.GB28181MediaPortRange
	onDisk.APIFileUploadDirectory = initial.APIFileUploadDirectory
	onDisk.APIFileUploadMaxSize = initial.APIFileUploadMaxSize
	want, err := json.Marshal(initial)
	require.NoError(t, err)
	got, err := json.Marshal(onDisk)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}

func TestFailedUploadsCleanUp(t *testing.T) {
	for _, ca := range []struct {
		name, max string
		data      []byte
		extra     bool
		status    int
	}{
		{"bad container", "2G", []byte("not MP4"), false, 400},
		{"size limit", "100B", uploadFixture(t), false, 413},
		{"extra multipart field", "2G", uploadFixture(t), true, 400},
	} {
		t.Run(ca.name, func(t *testing.T) {
			p, ok := newInstance(t, managementTestConfig+"apiFileUploadMaxSize: "+ca.max+"\n")
			require.True(t, ok)
			defer p.Close()
			before, err := os.ReadFile(p.confPath)
			require.NoError(t, err)
			postVideo(t, "files/failed", "test.mp4", ca.data, ca.extra, ca.status)
			require.Empty(t, uploadFiles(t, p))
			after, err := os.ReadFile(p.confPath)
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.NotContains(t, p.APIConfigSnapshot().Paths, "files/failed")
		})
	}
}

func TestReadOnlyConfigUploadCleanup(t *testing.T) {
	p, ok := newInstance(t, managementTestConfig)
	require.True(t, ok)
	defer p.Close()
	require.NoError(t, os.Chmod(p.confPath, 0400))
	defer os.Chmod(p.confPath, 0600)
	postVideo(t, "files/failed", "test.mp4", uploadFixture(t), false, 400)
	require.Empty(t, uploadFiles(t, p))
	require.NotContains(t, p.APIConfigSnapshot().Paths, "files/failed")
}

func TestUploadDeletionProtectsOtherFiles(t *testing.T) {
	p, ok := newInstance(t, managementTestConfig)
	require.True(t, ok)
	defer p.Close()
	postVideo(t, "files/owned", "test.mp4", uploadFixture(t), false, 200)
	require.Eventually(t, func() bool { return p.APIConfigSnapshot().Paths["files/owned"] != nil }, 3*time.Second, 20*time.Millisecond)
	source := p.APIConfigSnapshot().Paths["files/owned"].Source
	path, err := fileurl.Path(source)
	require.NoError(t, err)
	b, err := json.Marshal(map[string]any{"action": "add", "name": "files/shared", "path": map[string]any{"source": source}})
	require.NoError(t, err)
	managementRequest(t, string(b), 200)
	require.Eventually(t, func() bool { return p.APIConfigSnapshot().Paths["files/shared"] != nil }, 3*time.Second, 20*time.Millisecond)
	managementRequest(t, `{"action":"delete","name":"files/owned"}`, 400)
	require.FileExists(t, path)
	managementRequest(t, `{"action":"delete","name":"files/shared"}`, 200)
	require.FileExists(t, path, "manual file sources do not own their files")
	require.Eventually(t, func() bool { return p.APIConfigSnapshot().Paths["files/shared"] == nil }, 3*time.Second, 20*time.Millisecond)
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0600))
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(outside, path))
	managementRequest(t, `{"action":"delete","name":"files/owned"}`, 400)
	content, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "keep", string(content))
	require.Contains(t, p.APIConfigSnapshot().Paths, "files/owned")
}

func TestCanceledUploadCleansUp(t *testing.T) {
	p, ok := newInstance(t, managementTestConfig)
	require.True(t, ok)
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan error, 1)
	go func() { done <- p.APIManagementUpload(ctx, "files/canceled", "test.mp4", reader) }()
	_, err := writer.Write([]byte("partial"))
	require.NoError(t, err)
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("canceled upload did not stop")
	}
	require.Empty(t, uploadFiles(t, p))
	require.NotContains(t, p.APIConfigSnapshot().Paths, "files/canceled")
}

func TestStreamingUploadRefreshesReadDeadline(t *testing.T) {
	p, ok := newInstance(t, managementTestConfig+"readTimeout: 1s\n")
	require.True(t, ok)
	defer p.Close()
	reader, writer := io.Pipe()
	mw := multipart.NewWriter(writer)
	data := uploadFixture(t)
	// Valid MP4 padding makes the streamed request last beyond the timeout.
	padding := make([]byte, 32*1024+8)
	binary.BigEndian.PutUint32(padding, uint32(len(padding)))
	copy(padding[4:8], "free")
	data = append(data, padding...)
	writeDone := make(chan error, 1)
	go func() {
		part, err := mw.CreateFormFile("file", "slow.mp4")
		if err == nil {
			for off := 0; off < len(data); off += 4096 {
				_, err = part.Write(data[off:min(off+4096, len(data))])
				if err != nil {
					break
				}
				time.Sleep(150 * time.Millisecond)
			}
		}
		if err == nil {
			err = mw.Close()
		}
		writer.CloseWithError(err)
		writeDone <- err
	}()
	req, err := http.NewRequest("POST", "http://127.0.0.1:19997/v3/management/files/upload/files/slow", reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	res, err := client.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, 200, res.StatusCode, string(b))
	require.NoError(t, <-writeDone)
	require.Greater(t, time.Since(start), time.Second)
}

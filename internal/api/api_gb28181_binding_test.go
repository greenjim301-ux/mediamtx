package api

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/stretchr/testify/require"
)

func TestGB28181PathBinding(t *testing.T) {
	const id = "34020000001320000001"
	for _, ca := range []struct {
		name, paths          string
		configured, conflict bool
		configName           string
	}{
		{"default rule", "  ~^gb28181/(.+)$:\n    source: gb28181://$G1\n    sourceOnDemand: true\n  all_others:\n", true, false, "~^gb28181/(.+)$"},
		{"explicit same channel", "  gb28181/" + id + ":\n    source: gb28181://platform/" + id + "\n", true, false, "gb28181/" + id},
		{"explicit source overrides regex", "  gb28181/" + id + ":\n    source: file:///data/demo.mp4\n  ~^gb28181/(.+)$:\n    source: gb28181://$G1\n    sourceOnDemand: true\n", false, true, "gb28181/" + id},
		{"different GB channel", "  gb28181/" + id + ":\n    source: gb28181://34020000001320000002\n", false, true, "gb28181/" + id},
		{"fallback publisher", "  all_others:\n", false, false, "all_others"},
		{"explicit publisher", "  gb28181/" + id + ":\n", false, true, "gb28181/" + id},
		{"no matching config", "  other:\n", false, false, ""},
		{"Go named capture", "  '~^gb28181/(?P<channel>.+)$':\n    source: gb28181://$G1\n    sourceOnDemand: true\n", true, false, "~^gb28181/(?P<channel>.+)$"},
		{"regex priority", "  ~^gb28181/(.+)$:\n    source: gb28181://$G1\n    sourceOnDemand: true\n  ~^gb28181/(3402.+)$:\n    source: rtsp://127.0.0.1/other\n    sourceOnDemand: true\n", true, false, "~^gb28181/(.+)$"},
	} {
		t.Run(ca.name, func(t *testing.T) {
			file := test.CreateTempFile(t, []byte("paths:\n"+ca.paths))
			c, _, err := conf.Load(file, nil, nil)
			require.NoError(t, err)
			ch := defs.APIGB28181Channel{ID: id, Path: "gb28181/" + id}
			updateGB28181PathState(&ch, c.Paths)
			require.Equal(t, ca.configured, ch.PathConfigured)
			require.Equal(t, ca.conflict, ch.PathConflict)
			require.Equal(t, ca.configName, ch.PathConfigName)
		})
	}
}

// A directory response must carry the effective binding without adding paths.
type bindingGBServer struct{}

func (bindingGBServer) APIChannelsList() (*defs.APIGB28181ChannelList, error) {
	return &defs.APIGB28181ChannelList{Items: []defs.APIGB28181Channel{{ID: "34020000001320000001", Path: "gb28181/34020000001320000001"}}}, nil
}
func (bindingGBServer) APIDevicesList() (*defs.APIGB28181DeviceList, error)  { return nil, nil }
func (bindingGBServer) APIDevicesGet(string) (*defs.APIGB28181Device, error) { return nil, nil }
func (bindingGBServer) APIRefreshCatalog(string) error                       { return nil }
func (bindingGBServer) APIPTZControl(string, string) error                   { return nil }

func TestGB28181ChannelBindingResponse(t *testing.T) {
	file := test.CreateTempFile(t, []byte("paths:\n  ~^gb28181/(.+)$:\n    source: gb28181://$G1\n    sourceOnDemand: true\n  all_others:\n"))
	c, _, err := conf.Load(file, nil, nil)
	require.NoError(t, err)
	api := &API{Parent: &testParent{conf: c}, GB28181Server: bindingGBServer{}}
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", "/v3/gb28181/channels/list", nil)
	api.onGB28181ChannelsList(ctx)
	require.Equal(t, 200, w.Code)
	var result defs.APIGB28181ChannelList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result.Items, 1)
	require.True(t, result.Items[0].PathConfigured)
	require.False(t, result.Items[0].PathConflict)
	require.Equal(t, "~^gb28181/(.+)$", result.Items[0].PathConfigName)
	require.NotContains(t, c.Paths, "gb28181/34020000001320000001")
	require.Equal(t, "gb28181://$G1", c.Paths["~^gb28181/(.+)$"].Source)
}

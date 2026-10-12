package core

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"
)

func TestFileSourceOutputs(t *testing.T) {
	for _, name := range []string{"h264.mp4", "h265.mp4", "h264.mkv", "h265.mkv"} {
		t.Run(name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("..", "staticsources", "file", "testdata", name))
			require.NoError(t, err)
			src := (&url.URL{Scheme: "file", Path: path}).String()
			p, ok := newInstance(t, fmt.Sprintf("rtspAddress: 127.0.0.1:18554\nrtspTransports: [tcp]\nrtmp: false\nhls: false\nwebrtc: false\nsrt: false\nhttpflv: true\nhttpflvAddress: 127.0.0.1:18891\npaths:\n  video:\n    source: %s\n", src))
			require.True(t, ok)
			defer p.Close()
			tr := gortsplib.ProtocolTCP
			c := gortsplib.Client{Protocol: &tr, Scheme: "rtsp", Host: "127.0.0.1:18554"}
			require.NoError(t, c.Start())
			defer c.Close()
			u, err := base.ParseURL("rtsp://127.0.0.1:18554/video")
			require.NoError(t, err)
			var describeErr error
			require.Eventually(t, func() bool { _, _, describeErr = c.Describe(u); return describeErr == nil }, 3*time.Second, 20*time.Millisecond)
			desc, _, err := c.Describe(u)
			require.NoError(t, err)
			require.Len(t, desc.Medias, 1)
			require.Equal(t, "video", string(desc.Medias[0].Type))
			require.NoError(t, c.SetupAll(desc.BaseURL, desc.Medias))
			packets := make(chan uint32, 200)
			c.OnPacketRTP(desc.Medias[0], desc.Medias[0].Formats[0], func(pkt *rtp.Packet) {
				if pkt.Marker {
					select {
					case packets <- pkt.Timestamp:
					default:
					}
				}
			})
			_, err = c.Play(nil)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:18891/video.flv", nil)
			require.NoError(t, err)
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, 200, res.StatusCode)
			header := make([]byte, 13)
			_, err = io.ReadFull(res.Body, header)
			require.NoError(t, err)
			require.Equal(t, []byte("FLV"), header[:3])
			require.Equal(t, byte(1), header[4])
			var previous uint32
			var first uint32
			// Over three file repeats on the same HTTP connection, DTS must advance.
			for i := 0; i < 35; i++ {
				h := make([]byte, 11)
				_, err = io.ReadFull(res.Body, h)
				require.NoError(t, err)
				require.Equal(t, byte(9), h[0])
				size := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
				require.Less(t, size, 1<<20)
				ts := uint32(h[7])<<24 | uint32(h[4])<<16 | uint32(h[5])<<8 | uint32(h[6])
				if i == 0 {
					first = ts
				} else {
					require.GreaterOrEqual(t, ts, previous)
				}
				previous = ts
				_, err = io.CopyN(io.Discard, res.Body, int64(size+4))
				require.NoError(t, err)
			}
			require.Greater(t, previous-first, uint32(2500))
			// The simultaneous RTSP reader is still attached and receiving frames.
			var stamps []uint32
			for len(stamps) < 25 {
				select {
				case ts := <-packets:
					stamps = append(stamps, ts)
				case <-ctx.Done():
					t.Fatal("RTSP stopped during file loop")
				}
			}
			require.Greater(t, uint32(stamps[len(stamps)-1]-stamps[0]), uint32(180000))
		})
	}
}

package file

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestContainers(t *testing.T) {
	for _, name := range []string{"h264.mp4", "h265.mp4", "h264.mkv", "h265.mkv"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", name))
			require.NoError(t, err)
			defer f.Close()
			st, err := f.Stat()
			require.NoError(t, err)
			var v *video
			if filepath.Ext(name) == ".mp4" {
				v, err = readMP4(context.Background(), f, st.Size())
			} else {
				v, err = readMKV(context.Background(), f, st.Size())
			}
			require.NoError(t, err)
			require.NoError(t, v.finalize())
			require.Len(t, v.samples, 10)
			require.InDelta(t, float64(time.Second), float64(v.duration), float64(250*time.Millisecond))
			reordered := false
			for i, sa := range v.samples {
				p, er := v.payload(f, sa)
				require.NoError(t, er)
				require.NotNil(t, p)
				if i > 0 {
					require.Greater(t, sa.dts, v.samples[i-1].dts)
					if sa.pts < v.samples[i-1].pts {
						reordered = true
					}
				}
			}
			require.True(t, reordered, "fixture must exercise B frames")
			last := v.samples[len(v.samples)-1]
			require.GreaterOrEqual(t, v.duration+v.samples[0].dts, last.dts+last.duration)
		})
	}
}

func TestLacing(t *testing.T) {
	for _, ca := range []struct {
		mode  byte
		b     []byte
		sizes []int
		pos   int
	}{
		{0, []byte{1, 2, 3}, []int{3}, 0},
		{1, []byte{2, 2, 3, 10, 11, 12, 13, 14, 15}, []int{2, 3, 1}, 3},
		{2, []byte{1, 10, 11, 12, 13}, []int{2, 2}, 1},
		{3, []byte{2, 0x82, 0xc0, 10, 11, 12, 13, 14, 15}, []int{2, 3, 1}, 3},
	} {
		sizes, pos, err := laceSizes(ca.b, 0, ca.mode)
		require.NoError(t, err)
		require.Equal(t, ca.sizes, sizes)
		require.Equal(t, ca.pos, pos)
	}
	for _, b := range [][]byte{{}, {1}, {2, 255}, {2, 0x82, 0xff}} {
		_, _, err := laceSizes(b, 0, 1)
		require.Error(t, err)
	}
}

func TestMalformedFiles(t *testing.T) {
	for _, ext := range []string{"mp4", "mkv"} {
		f, err := os.CreateTemp(t.TempDir(), "bad."+ext)
		require.NoError(t, err)
		defer f.Close()
		_, err = f.Write([]byte{0, 0, 0, 100, 'm', 'o', 'o', 'v'})
		require.NoError(t, err)
		if ext == "mp4" {
			_, err = readMP4(context.Background(), f, 8)
		} else {
			_, err = readMKV(context.Background(), f, 8)
		}
		require.Error(t, err)
	}
}

func TestCodecConfigMalformed(t *testing.T) {
	for _, name := range []string{"h264", "h265"} {
		for n := 0; n < 30; n++ {
			_, _, err := codecConfig(name, make([]byte, n))
			require.Error(t, err)
		}
	}
}

func TestMP4Tables(t *testing.T) {
	// A count larger than the remaining table must not allocate or panic.
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[4:], 0xffffffff)
	_, err := table(b, 4)
	require.Error(t, err)
}

func TestWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, wait(ctx, time.Now().Add(time.Hour)), context.Canceled)
}

// Exercise malformed container metadata without requiring external media tools.
func FuzzContainer(f *testing.F) {
	for _, name := range []string{"h264.mp4", "h265.mp4", "h264.mkv", "h265.mkv"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(filepath.Ext(name) == ".mkv", data)
	}
	f.Fuzz(func(t *testing.T, mkv bool, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		file, err := os.CreateTemp(t.TempDir(), "container")
		require.NoError(t, err)
		defer file.Close()
		_, err = file.Write(data)
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var v *video
		if mkv {
			v, err = readMKV(ctx, file, int64(len(data)))
		} else {
			v, err = readMP4(ctx, file, int64(len(data)))
		}
		if err == nil && v.finalize() == nil {
			for _, sa := range v.samples {
				_, _ = v.payload(file, sa)
			}
		}
	})
}

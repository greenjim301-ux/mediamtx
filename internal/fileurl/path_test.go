package fileurl

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPath(t *testing.T) {
	for _, raw := range []string{"file:///tmp/a%20b.mp4", "file://localhost/tmp/a.MKV"} {
		_, err := Path(raw)
		require.NoError(t, err)
	}
	for _, raw := range []string{"file://remote/tmp/a.mp4", "file://relative.mp4", "file:///tmp/a.ts", "file:///tmp/a.mp4?x=1", "file:///tmp/a.mp4#x"} {
		_, err := Path(raw)
		require.Error(t, err)
	}
}

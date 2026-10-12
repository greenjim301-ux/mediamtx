// Package fileurl validates local video source URLs.
package fileurl

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Path validates a local file URL without opening the file.
func Path(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) {
		return "", fmt.Errorf("file source must be a local absolute URL, for example file:///data/video.mp4")
	}
	switch strings.ToLower(filepath.Ext(u.Path)) {
	case ".mp4", ".mkv":
	default:
		return "", fmt.Errorf("file source supports .mp4 and .mkv only")
	}
	return u.Path, nil
}

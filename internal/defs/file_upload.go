package defs

import "errors"

// ErrFileUploadTooLarge is returned when the configured upload limit is exceeded.
var ErrFileUploadTooLarge = errors.New("video file exceeds the configured upload size limit")

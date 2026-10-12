# Local video files

MediaMTX can read a local MP4 or Matroska (`.mkv`) file directly, without FFmpeg,
and publish its video as a continuously looping live stream.

```yaml
httpflv: true
httpflvAddress: :8891

paths:
  demo:
    source: file:///data/demo.mp4
```

Read the resulting stream with:

- RTSP: `rtsp://localhost:8554/demo`
- HTTP-FLV: `http://localhost:8891/demo.flv`

## Supported media

- Ordinary, non-fragmented MP4 and Matroska files.
- H.264 and H.265 video. The first supported video track is selected.
- Audio, subtitles and other tracks are ignored. Outputs contain video only.
- Media is remuxed, without decoding or re-encoding. H.265 HTTP-FLV uses
  Enhanced FLV and requires a compatible player and browser decoder.

Use an absolute local file URL, with URL-encoded special characters (for example
`file:///data/my%20video.mkv`). Remote file hosts are not supported. The file must
be a regular file and the video must start with a decodable key frame and valid
codec parameters. Encrypted/compressed Matroska video tracks, fragmented MP4,
and changing MP4 sample descriptions are unsupported.

## Playback behavior

By default each configured path continuously plays its file at normal speed,
even without viewers. All clients join the current position of that path; they
may wait for the next key frame. Client-side buffering can cause display latency
to differ. There are no per-client pause, seek or playback-position controls.

At EOF the file repeats while output timestamps keep advancing. The stream and
client connections remain active. A visual cut between the end and beginning
of the file is expected. Each path has its own playback timeline.

The existing `sourceOnDemand` setting also applies to file sources. If enabled,
playback starts with the first reader and restarts from the beginning after the
source has stopped due to lack of readers. Keep it disabled for continuous playback.

The service indexes the selected video before publishing and reads frame payloads
from disk as needed; it does not load the entire video into memory. The video index
is limited to 2,000,000 samples, individual frames to 32 MiB, and the MP4 `moov`
box to 64 MiB. Startup time and index memory depend on file length. Keep the file
unchanged while it is being streamed; replacing it requires restarting the source.

# Video fixtures

These synthetic fixtures contain 10 frames of a 64x48 test pattern at 10 fps,
including B frames. MP4 files also contain AAC audio; MKV files contain G.711
A-law (H.264) or mu-law (H.265) audio, which the file source must ignore.

FFmpeg was used only to generate the checked-in fixtures. Tests and the file
source do not invoke FFmpeg and require no installed media tools.

Generation (FFmpeg with libx264/libx265):

```sh
ffmpeg -f lavfi -i testsrc2=size=64x48:rate=10 -f lavfi -i sine=frequency=440:sample_rate=8000 -t 1 -c:v libx264 -threads 1 -g 10 -bf 2 -c:a pcm_alaw h264.mkv
ffmpeg -i h264.mkv -c:v copy -c:a aac h264.mp4
ffmpeg -f lavfi -i testsrc2=size=64x48:rate=10 -f lavfi -i sine=frequency=440:sample_rate=8000 -t 1 -c:v libx265 -x265-params pools=none:frame-threads=1:log-level=error:keyint=10:bframes=2 -c:a pcm_mulaw h265.mkv
ffmpeg -i h265.mkv -c:v copy -c:a aac -tag:v hvc1 h265.mp4
```

## mpeg
[![Status](https://github.com/gen2brain/mpeg/actions/workflows/test.yml/badge.svg)](https://github.com/gen2brain/mpeg/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/gen2brain/mpeg.svg)](https://pkg.go.dev/github.com/gen2brain/mpeg)
[![Go Report Card](https://goreportcard.com/badge/github.com/gen2brain/mpeg?branch=main)](https://goreportcard.com/report/github.com/gen2brain/mpeg) 

`MPEG-1` Video decoder, `MP2` Audio decoder and `MPEG-PS` Demuxer in pure Go.

### Why

This is a simple way to get video playback into your app or game.

`MPEG-1` is an old and inefficient codec, but it is still good enough for many use cases. The quality and compression ratio still holds up surprisingly well.
Decoding costs very little CPU time compared to modern video formats. All patents related to `MPEG-1` and `MP2` have expired, so it is entirely free now.

### Examples

- [frames](https://github.com/gen2brain/mpeg/blob/main/examples/frames) - extracts all frames from a video and saves them as JPEG
- [player-rl](https://github.com/gen2brain/mpeg/blob/main/examples/player-rl) - player using `raylib` with YUV->RGB conversion done on CPU
- [player-sdl](https://github.com/gen2brain/mpeg/blob/main/examples/player-sdl) - player using `SDL2` with accelerated YUV->RGB conversion
- [player-web](https://github.com/gen2brain/mpeg/blob/main/examples/player-web) - player using `WebGL` and `WebAudio`, see [live example](https://gen2brain.github.io/mpeg)

### Format

Most [MPEG-PS](https://en.wikipedia.org/wiki/MPEG_program_stream) (`.mpg`) files containing [MPEG-1](https://en.wikipedia.org/wiki/MPEG-1) video (`mpeg1video`) and [MPEG-1 Audio Layer II](https://en.wikipedia.org/wiki/MPEG-1_Audio_Layer_II) (`mp2`) streams should work.

Note that `.mpg` files can also contain [MPEG-2](https://en.wikipedia.org/wiki/MPEG-2) video, which this library does not support.

You can encode video in a suitable format with `FFmpeg`:
```
ffmpeg -i input.mp4 -c:v mpeg1video -q:v 16 -c:a mp2 -format mpeg output.mpg
```

`-q:v` sets a fixed video quality with a variable bitrate, where `0` is the highest.
You can use `-b:v` to set a fixed bitrate instead; e.g. `-b:v 2000k` for 2000 kbit/s.
Refer to the [FFmpeg documentation](https://ffmpeg.org/ffmpeg.html#Options) for more details.

### Credits

* [pl_mpeg](https://github.com/phoboslab/pl_mpeg) by Dominic Szablewski.
* [javampeg1video](https://sourceforge.net/projects/javampeg1video/) by Korandi Zoltan.
* [kjmp2](https://keyj.emphy.de/kjmp2/) by Martin J. Fiedler.

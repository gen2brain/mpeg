## mpeg
[![Status](https://github.com/gen2brain/mpeg/actions/workflows/test.yml/badge.svg)](https://github.com/gen2brain/mpeg/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/gen2brain/mpeg.svg)](https://pkg.go.dev/github.com/gen2brain/mpeg)

`MPEG-1` Video decoder, `MP2` Audio decoder and `MPEG-PS` Demuxer in pure Go.

### Why

This is a simple way to get video playback into your app or game.

`MPEG-1` is an old and inefficient codec, but it is still good enough for many use cases. The quality and compression ratio still holds up surprisingly well.
Decoding costs very little CPU time compared to modern video formats. All patents related to `MPEG-1` and `MP2` have expired, so it is entirely free now.

### Examples

- [frames](https://github.com/gen2brain/mpeg-examples/blob/main/frames) - extracts all frames from a video and saves them as JPEG
- [video](https://github.com/hajimehoshi/ebiten/tree/main/examples/video) - video example using `Ebitengine` with accelerated YUV->RGB conversion
- [player-rl](https://github.com/gen2brain/mpeg-examples/blob/main/player-rl) - player using `raylib` with YUV->RGB conversion done on CPU
- [player-sdl](https://github.com/gen2brain/mpeg-examples/blob/main/player-sdl) - player using `SDL2` with accelerated YUV->RGB conversion
- [player-sdl3](https://github.com/gen2brain/mpeg-examples/blob/main/player-sdl3) - player using `SDL3` with accelerated YUV->RGB conversion
- [player-web](https://github.com/gen2brain/mpeg-examples/blob/main/player-web) - player using `WebGL` and `WebAudio`, see [live example](https://gen2brain.github.io/mpeg)
- [player-xv](https://github.com/gen2brain/mpeg-examples/blob/main/player-xv) - player using `X11/XVideo` and `OSS`, accelerated

### Format

Most [MPEG-PS](https://en.wikipedia.org/wiki/MPEG_program_stream) (`.mpg`) files containing [MPEG-1](https://en.wikipedia.org/wiki/MPEG-1) video (`mpeg1video`) and [MPEG-1 Audio Layer II](https://en.wikipedia.org/wiki/MPEG-1_Audio_Layer_II) (`mp2`) streams should work.

Note that `.mpg` files can also contain [MPEG-2](https://en.wikipedia.org/wiki/MPEG-2) video, which this library does not support.

You can encode video in a suitable format with `FFmpeg`:
```
ffmpeg -i input.mp4 -c:v mpeg1video -q:v 0 -c:a mp2 -format mpeg output.mpg
```

`-q:v` sets a fixed video quality with a variable bitrate, where `0` is the highest.
You can use `-b:v` to set a fixed bitrate instead; e.g. `-b:v 2000k` for 2000 kbit/s.
Refer to the [FFmpeg documentation](https://ffmpeg.org/ffmpeg.html#Options) for more details.

If you have FFmpeg compiled with `libtwolame` (an optimised MP2 encoder), you can use `-c:a libtwolame -b:a 224k` instead of `-c:a mp2`.

If you just want to quickly test the library, try this file:
[https://gen2brain.github.io/mpeg/sintel.mpg](https://gen2brain.github.io/mpeg/sintel.mpg)

### Build tags

* `noasm` - do not use assembly optimizations

### Credits

* [pl_mpeg](https://github.com/phoboslab/pl_mpeg) by Dominic Szablewski.
* [javampeg1video](https://sourceforge.net/projects/javampeg1video/) by Korandi Zoltan.
* [kjmp2](https://keyj.emphy.de/kjmp2/) by Martin J. Fiedler.

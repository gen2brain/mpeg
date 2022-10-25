package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jfbus/httprs"
	"github.com/veandco/go-sdl2/sdl"

	"github.com/gen2brain/mpeg"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println(fmt.Sprintf("Usage: %s <file or url>", filepath.Base(os.Args[0])))
		os.Exit(1)
	}

	r, err := openFile(os.Args[1])
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	defer r.Close()

	mpg, err := mpeg.New(r)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	hasVideo := mpg.NumVideoStreams() > 0
	hasAudio := mpg.NumAudioStreams() > 0
	mpg.SetVideoEnabled(hasVideo)
	mpg.SetAudioEnabled(hasAudio)

	framerate := mpg.Framerate()
	samplerate := mpg.Samplerate()

	var flags uint32
	if hasVideo {
		flags = sdl.INIT_VIDEO
	}
	if hasAudio {
		flags |= sdl.INIT_AUDIO
	}

	err = sdl.Init(flags)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer sdl.Quit()

	var devId sdl.AudioDeviceID
	var texture *sdl.Texture
	var renderer *sdl.Renderer

	if hasAudio {
		spec := sdl.AudioSpec{
			Freq:     int32(samplerate),
			Format:   sdl.AUDIO_F32,
			Channels: 2,
			Samples:  mpeg.SamplesPerFrame,
		}

		devName := sdl.GetAudioDeviceName(0, false)
		devId, err = sdl.OpenAudioDevice(devName, false, &spec, nil, 0)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer sdl.CloseAudioDevice(devId)
		sdl.PauseAudioDevice(devId, false)

		duration := float64(spec.Samples) / float64(samplerate)
		mpg.SetAudioLeadTime(time.Duration(duration * float64(time.Second)))

		mpg.SetAudioCallback(func(m *mpeg.MPEG, samples *mpeg.Samples) {
			if samples == nil {
				return
			}

			err = sdl.QueueAudio(devId, samples.Bytes())
			if err != nil {
				fmt.Println(err)
			}
		})
	}

	if hasVideo {
		width := mpg.Width()
		height := mpg.Height()

		window, err := sdl.CreateWindow(filepath.Base(os.Args[1]), sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
			int32(width), int32(height), sdl.WINDOW_SHOWN|sdl.WINDOW_RESIZABLE)
		if err != nil {
			fmt.Println(err)
		}
		defer window.Destroy()

		renderer, err = sdl.CreateRenderer(window, -1, sdl.RENDERER_ACCELERATED|sdl.RENDERER_PRESENTVSYNC)
		if err != nil {
			fmt.Println(err)
		}
		defer renderer.Destroy()

		texture, err = renderer.CreateTexture(sdl.PIXELFORMAT_YV12, sdl.TEXTUREACCESS_STREAMING, int32(width), int32(height))
		if err != nil {
			fmt.Println(err)
		}
		defer texture.Destroy()

		mpg.SetVideoCallback(func(m *mpeg.MPEG, frame *mpeg.Frame) {
			if frame == nil {
				return
			}

			err = texture.UpdateYUV(nil, frame.Y.Data, frame.Y.Width, frame.Cb.Data, frame.Cb.Width, frame.Cr.Data, frame.Cr.Width)
			if err != nil {
				fmt.Println(err)
			}
		})
	}

	var pause bool
	var seekTo, lastTime, currentTime, elapsedTime float64

	running := true
	for running {
		seekTo = -1

		for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
			switch ev := event.(type) {
			case *sdl.QuitEvent:
				running = false
			case *sdl.MouseButtonEvent:
				if ev.Type == sdl.MOUSEBUTTONDOWN && ev.Clicks == 2 {
					err = toggleFullscreen(renderer)
					if err != nil {
						fmt.Println(err)
					}
				}
			case *sdl.KeyboardEvent:
				if ev.Type == sdl.KEYDOWN && (ev.Keysym.Sym == sdl.K_ESCAPE || ev.Keysym.Sym == sdl.K_q) {
					running = false
				} else if ev.Type == sdl.KEYDOWN && (ev.Keysym.Sym == sdl.K_SPACE || ev.Keysym.Sym == sdl.K_p) {
					pause = !pause
				} else if ev.Type == sdl.KEYDOWN && (ev.Keysym.Sym == sdl.K_f || ev.Keysym.Sym == sdl.K_F11) {
					err = toggleFullscreen(renderer)
					if err != nil {
						fmt.Println(err)
					}
				} else if ev.Type == sdl.KEYDOWN && ev.Keysym.Sym == sdl.K_RIGHT {
					seekTo = mpg.Time().Seconds() + 3
				} else if ev.Type == sdl.KEYDOWN && ev.Keysym.Sym == sdl.K_LEFT {
					seekTo = mpg.Time().Seconds() - 3
				}
			}
		}

		if !pause {
			currentTime = float64(sdl.GetTicks64()) / 1000
			elapsedTime = currentTime - lastTime
			if elapsedTime > 1.0/framerate {
				elapsedTime = 1.0 / framerate
			}
			lastTime = currentTime

			if seekTo != -1 {
				if hasAudio {
					sdl.ClearQueuedAudio(devId)
				}
				mpg.Seek(time.Duration(seekTo*float64(time.Second)), false)
			} else {
				mpg.Decode(time.Duration(elapsedTime * float64(time.Second)))
			}
		}

		if mpg.HasEnded() {
			running = false
		}

		if hasVideo && seekTo == -1 {
			err = renderer.Clear()
			if err != nil {
				fmt.Println(err)
			}

			err = renderer.Copy(texture, nil, nil)
			if err != nil {
				fmt.Println(err)
			}

			renderer.Present()
		}
	}
}

func openFile(arg string) (io.ReadSeekCloser, error) {
	var err error
	var r io.ReadSeekCloser

	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		res, err := http.Get(arg)
		if err != nil {
			return nil, err
		}

		r = httprs.NewHttpReadSeeker(res)
	} else {
		r, err = os.Open(arg)
		if err != nil {
			return nil, err
		}
	}

	return r, nil
}

func toggleFullscreen(renderer *sdl.Renderer) error {
	window, err := renderer.GetWindow()
	if err != nil {
		return err
	}

	isFullscreen := window.GetFlags()&sdl.WINDOW_FULLSCREEN_DESKTOP != 0
	if isFullscreen {
		err := window.SetFullscreen(0)
		if err != nil {
			return err
		}
		_, err = sdl.ShowCursor(1)
		if err != nil {
			return err
		}
	} else {
		err := window.SetFullscreen(sdl.WINDOW_FULLSCREEN_DESKTOP)
		if err != nil {
			return err
		}
		_, err = sdl.ShowCursor(0)
		if err != nil {
			return err
		}
	}

	return nil
}

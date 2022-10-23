package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	rl "github.com/gen2brain/raylib-go/raylib"
	"github.com/jfbus/httprs"

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

	width := int32(mpg.Width())
	height := int32(mpg.Height())
	framerate := mpg.Framerate()
	samplerate := mpg.Samplerate()

	var stream rl.AudioStream
	var texture rl.Texture2D
	var target rl.RenderTexture2D

	rl.SetConfigFlags(rl.FlagVsyncHint | rl.FlagWindowResizable)
	rl.InitWindow(width, height, filepath.Base(os.Args[1]))
	defer rl.CloseWindow()

	if hasAudio {
		rl.SetAudioStreamBufferSizeDefault(mpeg.SamplesPerFrame * 2)

		rl.InitAudioDevice()
		defer rl.CloseAudioDevice()

		stream = rl.LoadAudioStream(uint32(samplerate), 32, 2)
		defer rl.UnloadAudioStream(stream)
		rl.PlayAudioStream(stream)

		mpg.SetAudioLeadTime(float64(mpeg.SamplesPerFrame*2) / float64(samplerate))
		mpg.SetAudioCallback(func(m *mpeg.MPEG, samples *mpeg.Samples) {
			if samples == nil {
				return
			}

			if rl.IsAudioStreamProcessed(stream) {
				rl.UpdateAudioStream(stream, samples.Interleaved, int32(len(samples.Interleaved)))
			}
		})
	}

	if hasVideo {
		imFrame := &rl.Image{}
		imFrame.Width = width
		imFrame.Height = height
		imFrame.Format = rl.UncompressedR8g8b8a8
		imFrame.Mipmaps = 1
		defer rl.UnloadImage(imFrame)

		texture = rl.LoadTextureFromImage(imFrame)
		defer rl.UnloadTexture(texture)

		target = rl.LoadRenderTexture(width, height)
		defer rl.UnloadRenderTexture(target)

		mpg.SetVideoCallback(func(m *mpeg.MPEG, frame *mpeg.Frame) {
			if frame == nil {
				return
			}

			rl.UpdateTexture(texture, frame.Pixels())
		})
	}

	var pause, fullscreen bool
	var seekTo, lastTime, currentTime, elapsedTime float64
	var winPos rl.Vector2

	running := true
	for running {
		seekTo = -1

		if rl.IsKeyPressed(rl.KeyQ) || rl.WindowShouldClose() {
			running = false
		} else if rl.IsKeyPressed(rl.KeySpace) || rl.IsKeyPressed(rl.KeyP) {
			pause = !pause
		} else if rl.IsKeyPressed(rl.KeyF) || rl.IsKeyPressed(rl.KeyF11) {
			if !fullscreen {
				winPos = rl.GetWindowPosition()
			}
			fullscreen = toggleFullscreen(fullscreen, winPos, width, height)
		} else if rl.IsKeyPressed(rl.KeyRight) {
			seekTo = mpg.Time() + 3
		} else if rl.IsKeyPressed(rl.KeyLeft) {
			seekTo = mpg.Time() - 3
		}

		if !pause {
			currentTime = rl.GetTime()
			elapsedTime = currentTime - lastTime
			if elapsedTime > 1.0/framerate {
				elapsedTime = 1.0 / framerate
			}
			lastTime = currentTime

			if seekTo != -1 {
				mpg.Seek(seekTo, false)
			} else {
				mpg.Decode(elapsedTime)
			}
		}

		if mpg.HasEnded() {
			running = false
		}

		if hasVideo {
			rl.BeginDrawing()
			rl.ClearBackground(rl.White)

			rl.BeginTextureMode(target)
			rl.DrawTexture(texture, 0, 0, rl.White)
			rl.EndTextureMode()

			rl.DrawTexturePro(
				target.Texture,
				rl.NewRectangle(0, 0, float32(target.Texture.Width), float32(-target.Texture.Height)),
				rl.NewRectangle(0, 0, float32(rl.GetScreenWidth()), float32(rl.GetScreenHeight())),
				rl.NewVector2(0, 0),
				0,
				rl.White,
			)

			rl.EndDrawing()
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

func toggleFullscreen(fullscreen bool, winPos rl.Vector2, w, h int32) bool {
	if fullscreen {
		rl.ClearWindowState(rl.FlagWindowUndecorated | rl.FlagWindowTopmost)
		rl.SetWindowPosition(int(winPos.X), int(winPos.Y))
		rl.SetWindowSize(int(w), int(h))
		rl.ShowCursor()
		return false
	} else {
		rl.SetWindowState(rl.FlagWindowUndecorated | rl.FlagWindowTopmost)
		d := rl.GetCurrentMonitor()
		rl.SetWindowSize(rl.GetMonitorWidth(d), rl.GetMonitorHeight(d))
		rl.HideCursor()
		return true
	}
}

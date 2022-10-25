package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/jfbus/httprs"

	"github.com/gen2brain/mpeg"
)

var errEnd = errors.New("end")

type app struct {
	mpg *mpeg.MPEG

	width  int
	height int

	pause  bool
	seekTo float64

	img    *ebiten.Image
	player *audio.Player
}

func newApp(m *mpeg.MPEG) (*app, error) {
	a := &app{}
	a.mpg = m

	a.width = a.mpg.Width()
	a.height = a.mpg.Height()

	hasVideo := a.mpg.NumVideoStreams() > 0
	hasAudio := a.mpg.NumAudioStreams() > 0
	a.mpg.SetVideoEnabled(hasVideo)
	a.mpg.SetAudioEnabled(hasAudio)

	a.img = ebiten.NewImage(a.width, a.height)

	if hasAudio {
		samplerate := a.mpg.Samplerate()
		audioContext := audio.NewContext(samplerate)

		a.mpg.SetAudioFormat(mpeg.AudioS16)

		duration := time.Duration((float64(mpeg.SamplesPerFrame) / float64(samplerate)) * float64(time.Second))
		a.mpg.SetAudioLeadTime(duration)

		var err error
		a.player, err = audioContext.NewPlayer(a.mpg.Audio().Reader())
		a.player.SetBufferSize(duration)
		if err != nil {
			return nil, err
		}

		a.player.Play()

		a.mpg.SetAudioCallback(func(m *mpeg.MPEG, samples *mpeg.Samples) {
			if !a.player.IsPlaying() {
				a.player.Play()
			}
		})
	}

	if hasVideo {
		a.mpg.SetVideoCallback(func(m *mpeg.MPEG, frame *mpeg.Frame) {
			if frame != nil {
				a.img.WritePixels(frame.RGBA().Pix)
			}
		})
	}

	return a, nil
}

func (a *app) Update() error {
	a.seekTo = -1

	if ebiten.IsKeyPressed(ebiten.KeyEscape) || ebiten.IsKeyPressed(ebiten.KeyQ) {
		return errEnd
	} else if ebiten.IsKeyPressed(ebiten.KeyF) || ebiten.IsKeyPressed(ebiten.KeyF11) {
		toggleFullscreen()
	} else if ebiten.IsKeyPressed(ebiten.KeyRight) {
		a.seekTo = a.mpg.Time().Seconds() + 3
	} else if ebiten.IsKeyPressed(ebiten.KeyLeft) {
		a.seekTo = a.mpg.Time().Seconds() - 3
	} else if ebiten.IsKeyPressed(ebiten.KeySpace) || ebiten.IsKeyPressed(ebiten.KeyP) {
		a.pause = !a.pause
		if a.pause {
			a.player.Pause()
		}
	}

	if !a.pause {
		if a.seekTo != -1 {
			d := time.Duration(a.seekTo * float64(time.Second))
			err := a.player.Seek(d)
			if err != nil {
				return err
			}

			a.mpg.Seek(d, false)
		} else {
			a.mpg.Decode(time.Duration((1 / float64(ebiten.TPS())) * float64(time.Second)))
		}
	}

	if a.mpg.HasEnded() {
		return errEnd
	}

	return nil
}

func (a *app) Draw(screen *ebiten.Image) {
	screen.DrawImage(a.img, nil)
}

func (a *app) Layout(outsideWidth, outsideHeight int) (int, int) {
	return a.width, a.height
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println(fmt.Sprintf("Usage: %s <file or url>", filepath.Base(os.Args[0])))
		os.Exit(1)
	}

	file, err := openFile(os.Args[1])
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	defer file.Close()

	mpg, err := mpeg.New(file)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	game, err := newApp(mpg)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	ebiten.SetWindowTitle(filepath.Base(os.Args[1]))
	ebiten.SetWindowSize(mpg.Width(), mpg.Height())
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)

	if err := ebiten.RunGame(game); err != nil {
		if !errors.Is(err, errEnd) {
			fmt.Println(err.Error())
			os.Exit(1)
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

func toggleFullscreen() {
	if ebiten.IsFullscreen() {
		ebiten.SetFullscreen(false)
		ebiten.SetCursorMode(ebiten.CursorModeVisible)
	} else {
		ebiten.SetFullscreen(true)
		ebiten.SetCursorMode(ebiten.CursorModeHidden)
	}
}

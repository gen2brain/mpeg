package main

import (
	"fmt"
	"image/jpeg"
	"os"

	"github.com/gen2brain/mpeg"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: frames <file.mpg>")
		os.Exit(1)
	}

	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	mpg, err := mpeg.New(file)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	err = os.MkdirAll("images", 0755)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	for {
		frame := mpg.DecodeVideo()
		if frame != nil {
			w, err := os.Create(fmt.Sprintf("images/frame-%05.2f.jpg", frame.Time))
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			err = jpeg.Encode(w, frame.YCbCr(), nil)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			err = w.Close()
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		}

		if mpg.HasEnded() {
			break
		}
	}
}

module player-sdl

replace github.com/gen2brain/mpeg => ../../

go 1.19

require github.com/gen2brain/mpeg v0.0.0-00010101000000-000000000000

require (
	github.com/jfbus/httprs v0.0.0-20190827093123-b0af8319bb15
	github.com/veandco/go-sdl2 v0.4.25
)

require (
	github.com/mitchellh/copystructure v1.2.0 // indirect
	github.com/mitchellh/reflectwalk v1.0.2 // indirect
)

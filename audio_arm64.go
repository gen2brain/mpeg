//go:build arm64 && !noasm

package mpeg

//go:noescape
func synthWindowNEON(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)

func synthWindow(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int) {
	synthWindowNEON(u, d, v, vPos)
}

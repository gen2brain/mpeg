//go:build amd64 && !noasm

package mpeg

//go:noescape
func synthWindowSSE2(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)

//go:noescape
func synthWindowAVX2(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)

func synthWindow(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int) {
	if isAVX2 {
		synthWindowAVX2(u, d, v, vPos)
	} else {
		synthWindowSSE2(u, d, v, vPos)
	}
}

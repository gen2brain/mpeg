//go:build (!amd64 && !arm64) || noasm

package mpeg

// synthWindow applies the audio synthesis window, building the 32 output
// samples u from the V ring buffer and window table d. Reslicing to 32-wide
// spans keeps the inner multiply-accumulate bounds-check free.
func synthWindow(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int) {
	for i := range u {
		u[i] = 0
	}

	dIndex := 512 - (vPos >> 1)
	vIndex := (vPos % 128) >> 1
	for vIndex < 1024 {
		dd := d[dIndex : dIndex+32 : dIndex+32]
		vv := v[vIndex : vIndex+32 : vIndex+32]
		for i := 0; i < 32; i++ {
			u[i] += dd[i] * vv[i]
		}

		vIndex += 128
		dIndex += 64
	}

	dIndex -= 512 - 32
	vIndex = (128 - 32 + 1024) - vIndex
	for vIndex < 1024 {
		dd := d[dIndex : dIndex+32 : dIndex+32]
		vv := v[vIndex : vIndex+32 : vIndex+32]
		for i := 0; i < 32; i++ {
			u[i] += dd[i] * vv[i]
		}

		vIndex += 128
		dIndex += 64
	}
}

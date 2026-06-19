package mpeg

import "testing"

type synthWindowFunc func(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int)

// synthWindowRef is the scalar oracle. As a plain multiply-add it is FMA-
// contracted on arm64 but not amd64, matching each platform's asm kernel.
func synthWindowRef(u *[32]float32, d *[1024]float32, v *[1024]float32, vPos int) {
	for i := range u {
		u[i] = 0
	}
	dIndex := 512 - (vPos >> 1)
	vIndex := (vPos % 128) >> 1
	for vIndex < 1024 {
		for i := 0; i < 32; i++ {
			u[i] += d[dIndex+i] * v[vIndex+i]
		}
		vIndex += 128
		dIndex += 64
	}
	dIndex -= 512 - 32
	vIndex = (128 - 32 + 1024) - vIndex
	for vIndex < 1024 {
		for i := 0; i < 32; i++ {
			u[i] += d[dIndex+i] * v[vIndex+i]
		}
		vIndex += 128
		dIndex += 64
	}
}

// runSynthWindowParity checks fn against the oracle for every vPos the decoder
// produces (always a multiple of 64). tol=0 demands bit-exactness; a small tol
// tolerates an FMA kernel's last bit (AVX2), whose exact output TestAudioGolden locks.
func runSynthWindowParity(t *testing.T, fn synthWindowFunc, tol float32) {
	t.Helper()

	var d, v [1024]float32
	for i := range d {
		d[i] = float32((i*7)%101-50) * 0.013
	}
	for i := range v {
		v[i] = float32((i*13)%97-48) * 0.011
	}

	abs := func(f float32) float32 {
		if f < 0 {
			return -f
		}
		return f
	}

	for vPos := 0; vPos < 1024; vPos += 64 {
		var got, want [32]float32
		fn(&got, &d, &v, vPos)
		synthWindowRef(&want, &d, &v, vPos)
		for i := range want {
			if abs(got[i]-want[i]) > tol*(1+abs(want[i])) {
				t.Fatalf("vPos=%d lane %d: got %v want %v", vPos, i, got[i], want[i])
			}
		}
	}
}

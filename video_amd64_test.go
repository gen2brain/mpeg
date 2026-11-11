//go:build amd64 && !noasm

package mpeg

import "testing"

// TestCopyMacroblockParitySSE2 and TestCopyMacroblockParityAVX2 verify each
// assembly backend directly, independent of which one this host selects at
// runtime.

func TestCopyMacroblockParitySSE2(t *testing.T) {
	runParitySweep(t, copyMacroblockSSE2)
}

func TestCopyMacroblockParityAVX2(t *testing.T) {
	if !isAVX2 {
		t.Skip("CPU does not support AVX2")
	}
	runParitySweep(t, copyMacroblockAVX2)
}

func TestIdctAVX2(t *testing.T) {
	if !isAVX2 {
		t.Skip("CPU does not support AVX2")
	}

	x := uint32(1)
	next := func() int {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5

		return int(x)
	}

	const stride = 24
	for iter := range 4000 {
		var want, got [64]int32
		maxIndex := 64
		count := next()%64 + 1
		if iter%3 == 0 {
			maxIndex, count = 9, next()%9+1
		}
		for range count {
			n := int(videoZigZag[next()%maxIndex]) & 63
			level := next()%4096 - 2048
			if iter%7 == 0 {
				level = []int{-2048, 2047}[next()&1]
			}
			want[n] = int32(level * int(videoPremultiplierMatrix[n]))
		}
		got = want

		for _, add := range []bool{false, true} {
			dw := make([]byte, 8*stride)
			for i := range dw {
				dw[i] = byte(next())
			}
			dg := append([]byte{}, dw...)
			w, g := want, got

			idctBlockGo(&w, maxIndex, dw, 0, stride-8, add)
			idctAVX2(&g[0], &dg[0], stride, add)
			if string(dg) != string(dw) {
				t.Fatalf("iter %d add=%v: output differs\ngot  %v\nwant %v", iter, add, dg, dw)
			}
			if g != [64]int32{} {
				t.Fatalf("iter %d add=%v: block not cleared", iter, add)
			}
		}
	}
}

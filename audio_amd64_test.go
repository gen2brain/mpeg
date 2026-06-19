//go:build amd64 && !noasm

package mpeg

import "testing"

func TestSynthWindowSSE2(t *testing.T) {
	runSynthWindowParity(t, synthWindowSSE2, 0)
}

func TestSynthWindowAVX2(t *testing.T) {
	if !isAVX2 {
		t.Skip("CPU does not support AVX2")
	}
	// FMA kernel: tolerate the last bit vs the non-FMA oracle.
	runSynthWindowParity(t, synthWindowAVX2, 1e-5)
}

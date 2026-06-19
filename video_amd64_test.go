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

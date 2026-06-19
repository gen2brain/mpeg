//go:build amd64 && !noasm

package mpeg

// isAVX2 reports whether AVX2 is available; it gates the AVX2 asm kernels.
var isAVX2 bool

func init() {
	isAVX2 = hasAVX2()
}

func hasAVX2() bool

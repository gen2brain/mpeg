//go:build amd64 && !noasm

package mpeg

var isAVX2 bool

func init() {
	isAVX2 = hasAVX2()
}

func hasAVX2() bool

//go:noescape
func copyMacroblockSSE2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)

//go:noescape
func copyMacroblockAVX2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame) {
	if isAVX2 {
		copyMacroblockAVX2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d)
	} else {
		copyMacroblockSSE2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d)
	}
}

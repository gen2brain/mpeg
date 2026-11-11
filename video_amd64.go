//go:build amd64 && !noasm

package mpeg

//go:noescape
func copyMacroblockSSE2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool)

//go:noescape
func copyMacroblockAVX2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool)

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool) {
	if isAVX2 {
		copyMacroblockAVX2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d, avg)
	} else {
		copyMacroblockSSE2(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d, avg)
	}
}

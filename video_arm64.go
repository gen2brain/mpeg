//go:build arm64 && !noasm

package mpeg

//go:noescape
func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame) {
	if !macroblockInBounds(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d) {
		return // corrupt stream
	}
	copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d)
}

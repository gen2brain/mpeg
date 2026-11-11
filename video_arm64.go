//go:build arm64 && !noasm

package mpeg

//go:noescape
func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool)

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool) {
	copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d, avg)
}

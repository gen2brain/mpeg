//go:build arm64 && !noasm

package mpeg

//go:noescape
func copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool)

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool) {
	copyMacroblockNEON(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, s, d, avg)
}

//go:noescape
func idctNEON(block *int32, dst *uint8, stride int, add bool)

func idctBlock(block *[64]int32, maxIndex int, dest []byte, index, scan int, add bool) {
	_ = dest[index+7*(scan+8)+7]
	idctNEON(&block[0], &dest[index], scan+8, add)
}

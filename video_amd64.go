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

//go:noescape
func idctAVX2(block *int32, dst *uint8, stride int, add bool)

//go:noescape
func idctSSE2(block *int32, dst *uint8, stride int, add bool)

func idctBlock(block *[64]int32, maxIndex int, dest []byte, index, scan int, add bool) {
	_ = dest[index+7*(scan+8)+7]
	if isAVX2 {
		idctAVX2(&block[0], &dest[index], scan+8, add)
	} else {
		idctSSE2(&block[0], &dest[index], scan+8, add)
	}
}

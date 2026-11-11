//go:build riscv64 && riscv64.rva23u64 && !noasm

package mpeg

//go:noescape
func copyBlockRVV(dst, src *uint8, stride, size, mode int, avg bool)

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool) {
	mode := motionH&1 | motionV&1<<1
	si := ((mbRow<<4)+motionV>>1)*lumaWidth + (mbCol << 4) + motionH>>1
	di := (mbRow<<4)*lumaWidth + (mbCol << 4)
	copyBlockRVV(&d.Y.Data[di], &s.Y.Data[si], lumaWidth, 16, mode, avg)

	cmH, cmV := motionH/2, motionV/2
	mode = cmH&1 | cmV&1<<1
	si = ((mbRow<<3)+cmV>>1)*chromaWidth + (mbCol << 3) + cmH>>1
	di = (mbRow<<3)*chromaWidth + (mbCol << 3)
	copyBlockRVV(&d.Cb.Data[di], &s.Cb.Data[si], chromaWidth, 8, mode, avg)
	copyBlockRVV(&d.Cr.Data[di], &s.Cr.Data[si], chromaWidth, 8, mode, avg)
}

func idctBlock(block *[64]int32, maxIndex int, dest []byte, index, scan int, add bool) {
	idctBlockGo(block, maxIndex, dest, index, scan, add)
}

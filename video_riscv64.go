//go:build riscv64 && riscv64.rva23u64 && !noasm

package mpeg

//go:noescape
func copyBlocksRVV(d0, s0 *uint8, st0, n0, m0 int, d1, s1 *uint8, st1, n1, m1 int, d2, s2 *uint8, st2, n2, m2 int, avg bool)

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool) {
	si := ((mbRow<<4)+motionV>>1)*lumaWidth + (mbCol << 4) + motionH>>1
	di := (mbRow<<4)*lumaWidth + (mbCol << 4)
	cmH, cmV := motionH/2, motionV/2
	csi := ((mbRow<<3)+cmV>>1)*chromaWidth + (mbCol << 3) + cmH>>1
	cdi := (mbRow<<3)*chromaWidth + (mbCol << 3)
	mode := cmH&1 | cmV&1<<1
	copyBlocksRVV(&d.Y.Data[di], &s.Y.Data[si], lumaWidth, 16, motionH&1|motionV&1<<1,
		&d.Cb.Data[cdi], &s.Cb.Data[csi], chromaWidth, 8, mode,
		&d.Cr.Data[cdi], &s.Cr.Data[csi], chromaWidth, 8, mode, avg)
}

//go:noescape
func idctRVV(block *int32, dst *uint8, stride int, add bool)

func idctBlock(block *[64]int32, maxIndex int, dest []byte, index, scan int, add bool) {
	_ = dest[index+7*(scan+8)+7]
	idctRVV(&block[0], &dest[index], scan+8, add)
}

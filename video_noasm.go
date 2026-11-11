//go:build (!amd64 && !arm64 && !(riscv64 && riscv64.rva23u64)) || noasm

package mpeg

import "encoding/binary"

// SWAR (SIMD-within-a-register) constants for processing 8 packed bytes at a time.
const (
	loByteMask = 0x00ff00ff00ff00ff // selects the even byte of each 16-bit lane
	avgMask    = 0x7f7f7f7f7f7f7f7f // clears the carry bit of each byte after >>1
	twoPerLane = 0x0002000200020002 // rounding bias for the bilinear average
)

// roundAvg returns the per-byte rounding average (a+b+1)>>1 of 8 packed bytes.
func roundAvg(a, b uint64) uint64 {
	return (a | b) - (((a ^ b) >> 1) & avgMask)
}

// bilinAvg returns the per-byte bilinear average (a+b+c+d+2)>>2 of 8 packed
// bytes. The bytes are spread into 16-bit lanes so the four-way sum cannot
// overflow, then narrowed back.
func bilinAvg(a, b, c, d uint64) uint64 {
	lo := ((a & loByteMask) + (b & loByteMask) + (c & loByteMask) + (d & loByteMask) + twoPerLane) >> 2 & loByteMask
	hi := (((a >> 8) & loByteMask) + ((b >> 8) & loByteMask) + ((c >> 8) & loByteMask) + ((d >> 8) & loByteMask) + twoPerLane) >> 2 & loByteMask
	return lo | hi<<8
}

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool) {
	hp := motionH >> 1
	vp := motionV >> 1
	lsi := ((mbRow<<4)+vp)*lumaWidth + (mbCol << 4) + hp
	ldi := (mbRow<<4)*lumaWidth + (mbCol << 4)
	copyBlock(s.Y.Data, d.Y.Data, lumaWidth, lsi, ldi, 16, motionH&1 == 1, motionV&1 == 1, avg)

	cmH := motionH / 2
	cmV := motionV / 2
	hp = cmH >> 1
	vp = cmV >> 1
	csi := ((mbRow<<3)+vp)*chromaWidth + (mbCol << 3) + hp
	cdi := (mbRow<<3)*chromaWidth + (mbCol << 3)
	copyBlock(s.Cb.Data, d.Cb.Data, chromaWidth, csi, cdi, 8, cmH&1 == 1, cmV&1 == 1, avg)
	copyBlock(s.Cr.Data, d.Cr.Data, chromaWidth, csi, cdi, 8, cmH&1 == 1, cmV&1 == 1, avg)
}

// copyBlock performs motion compensation for a single size×size block (16 for
// luma, 8 for chroma) at byte offset si in src and di in dst, processing 8
// bytes of each row per iteration. oddH and oddV select half-pel interpolation.
func copyBlock(src, dst []byte, stride, si, di, size int, oddH, oddV, avg bool) {
	// Motion reads can reach just past the plane into the shared buffer.
	src = src[:cap(src)]

	for r := 0; r < size; r++ {
		for x := 0; x < size; x += 8 {
			var v uint64
			a := binary.LittleEndian.Uint64(src[si+x:])
			switch {
			case !oddH && !oddV:
				v = a
			case oddH && !oddV:
				v = roundAvg(a, binary.LittleEndian.Uint64(src[si+x+1:]))
			case !oddH && oddV:
				v = roundAvg(a, binary.LittleEndian.Uint64(src[si+x+stride:]))
			default:
				b := binary.LittleEndian.Uint64(src[si+x+1:])
				c := binary.LittleEndian.Uint64(src[si+x+stride:])
				e := binary.LittleEndian.Uint64(src[si+x+stride+1:])
				v = bilinAvg(a, b, c, e)
			}
			if avg {
				v = roundAvg(binary.LittleEndian.Uint64(dst[di+x:]), v)
			}
			binary.LittleEndian.PutUint64(dst[di+x:], v)
		}
		si += stride
		di += stride
	}
}

func idctBlock(block *[64]int32, maxIndex int, dest []byte, index, scan int, add bool) {
	idctBlockGo(block, maxIndex, dest, index, scan, add)
}

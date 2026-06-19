package mpeg

import (
	"testing"
)

// copyMacroblockRef is a scalar reference implementation of the MPEG-1 block
// motion compensation, used as an oracle to verify the optimized copyMacroblock
// backends (assembly or the simd package) produce bit-identical output.
func copyMacroblockRef(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame) {
	plane := func(src, dst []byte, stride, size, motionH, motionV, mbRow, mbCol int) {
		hp := motionH >> 1
		vp := motionV >> 1
		oddH := motionH&1 == 1
		oddV := motionV&1 == 1

		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				si := ((mbRow*size)+vp+y)*stride + (mbCol * size) + hp + x
				di := ((mbRow*size)+y)*stride + (mbCol * size) + x

				var v int
				switch {
				case !oddH && !oddV:
					v = int(src[si])
				case oddH && !oddV:
					v = (int(src[si]) + int(src[si+1]) + 1) >> 1
				case !oddH && oddV:
					v = (int(src[si]) + int(src[si+stride]) + 1) >> 1
				default:
					v = (int(src[si]) + int(src[si+1]) + int(src[si+stride]) + int(src[si+stride+1]) + 2) >> 2
				}
				dst[di] = byte(v)
			}
		}
	}

	plane(s.Y.Data, d.Y.Data, lumaWidth, 16, motionH, motionV, mbRow, mbCol)
	cmH := motionH / 2
	cmV := motionV / 2
	plane(s.Cb.Data, d.Cb.Data, chromaWidth, 8, cmH, cmV, mbRow, mbCol)
	plane(s.Cr.Data, d.Cr.Data, chromaWidth, 8, cmH, cmV, mbRow, mbCol)
}

func newTestFrame(lumaWidth, chromaWidth, fill int) *Frame {
	f := &Frame{}
	f.Y.Width, f.Y.Height, f.Y.Data = lumaWidth, lumaWidth, make([]byte, lumaWidth*lumaWidth)
	f.Cb.Width, f.Cb.Height, f.Cb.Data = chromaWidth, chromaWidth, make([]byte, chromaWidth*chromaWidth)
	f.Cr.Width, f.Cr.Height, f.Cr.Data = chromaWidth, chromaWidth, make([]byte, chromaWidth*chromaWidth)
	// Deterministic pseudo-random pattern; fill perturbs it per source frame.
	for i := range f.Y.Data {
		f.Y.Data[i] = byte((i*131 + fill*7) & 0xff)
	}
	for i := range f.Cb.Data {
		f.Cb.Data[i] = byte((i*197 + fill*13) & 0xff)
		f.Cr.Data[i] = byte((i*251 + fill*29) & 0xff)
	}
	return f
}

type copyMacroblockFunc func(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame)

func TestCopyMacroblockParity(t *testing.T) {
	runParitySweep(t, copyMacroblock)
}

// runParitySweep checks fn against the scalar oracle across both half-pel
// fractions on each axis. Positions start at macroblock 1 so the negative
// motion vectors (which exercise the toward-zero chroma rounding) stay in
// bounds.
func runParitySweep(t *testing.T, fn copyMacroblockFunc) {
	t.Helper()

	const lumaWidth, chromaWidth = 64, 32

	src := newTestFrame(lumaWidth, chromaWidth, 1)

	for _, mbRow := range []int{1, 2} {
		for _, mbCol := range []int{1, 2} {
			for motionH := -3; motionH < 4; motionH++ {
				for motionV := -3; motionV < 4; motionV++ {
					got := newTestFrame(lumaWidth, chromaWidth, 0)
					want := newTestFrame(lumaWidth, chromaWidth, 0)

					fn(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, src, got)
					copyMacroblockRef(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, src, want)

					check := func(name string, a, b []byte) {
						for i := range a {
							if a[i] != b[i] {
								t.Fatalf("%s mismatch at byte %d (mbRow=%d mbCol=%d mH=%d mV=%d): got %d want %d",
									name, i, mbRow, mbCol, motionH, motionV, a[i], b[i])
							}
						}
					}
					check("Y", got.Y.Data, want.Y.Data)
					check("Cb", got.Cb.Data, want.Cb.Data)
					check("Cr", got.Cr.Data, want.Cr.Data)
				}
			}
		}
	}
}

func benchmarkCopyMacroblock(b *testing.B, motionH, motionV int) {
	const lumaWidth, chromaWidth = 64, 32
	src := newTestFrame(lumaWidth, chromaWidth, 1)
	dst := newTestFrame(lumaWidth, chromaWidth, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copyMacroblock(motionH, motionV, 1, 1, lumaWidth, chromaWidth, src, dst)
	}
}

func BenchmarkCopyMacroblockCopy(b *testing.B)  { benchmarkCopyMacroblock(b, 0, 0) }
func BenchmarkCopyMacroblockHoriz(b *testing.B) { benchmarkCopyMacroblock(b, 1, 0) }
func BenchmarkCopyMacroblockVert(b *testing.B)  { benchmarkCopyMacroblock(b, 0, 1) }
func BenchmarkCopyMacroblockBilin(b *testing.B) { benchmarkCopyMacroblock(b, 3, 3) }

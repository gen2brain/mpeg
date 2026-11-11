package mpeg

import (
	"testing"
)

// copyMacroblockRef is a scalar reference implementation of the MPEG-1 block
// motion compensation, used as an oracle to verify the optimized copyMacroblock
// backends (assembly or the simd package) produce bit-identical output.
func copyMacroblockRef(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool) {
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
				if avg {
					v = (int(dst[di]) + v + 1) >> 1
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

type copyMacroblockFunc func(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame, avg bool)

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
					for _, avg := range []bool{false, true} {
						got := newTestFrame(lumaWidth, chromaWidth, 0)
						want := newTestFrame(lumaWidth, chromaWidth, 0)

						fn(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, src, got, avg)
						copyMacroblockRef(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth, src, want, avg)

						check := func(name string, a, b []byte) {
							for i := range a {
								if a[i] != b[i] {
									t.Fatalf("%s mismatch at byte %d (mbRow=%d mbCol=%d mH=%d mV=%d avg=%v): got %d want %d",
										name, i, mbRow, mbCol, motionH, motionV, avg, a[i], b[i])
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
}

func newGuardedFrame(lumaWidth, chromaWidth, fill int, mark byte) (*Frame, [][]byte) {
	const guard = 4096
	f := newTestFrame(lumaWidth, chromaWidth, fill)
	var backing [][]byte
	carve := func(p *Plane) {
		n := len(p.Data)
		b := make([]byte, guard+n+guard)
		for i := range b {
			b[i] = mark
		}
		copy(b[guard:], p.Data)
		p.Data = b[guard : guard+n : guard+n]
		backing = append(backing, b)
	}
	carve(&f.Y)
	carve(&f.Cb)
	carve(&f.Cr)

	return f, backing
}

func guardsIntact(backing [][]byte, mark byte) bool {
	const guard = 4096
	for _, b := range backing {
		for i := range guard {
			if b[i] != mark || b[len(b)-1-i] != mark {
				return false
			}
		}
	}

	return true
}

func TestPredictMacroblockOutOfBounds(t *testing.T) {
	const lumaWidth, chromaWidth = 64, 32
	const mbs = lumaWidth / 16

	cases := []struct {
		name                           string
		motionH, motionV, mbRow, mbCol int
	}{
		{"negative column", 0, 0, 0, -1},
		{"negative row", 0, 0, -1, 0},
		{"column past width", 0, 0, 0, mbs},
		{"row past height", 0, 0, mbs, 0},
		{"motion above frame", 0, -2048, 1, 1},
		{"motion below frame", 0, 2048, 1, 1},
		{"motion left of frame", -2048, 0, 0, 0},
		{"motion right of frame", 2048, 0, mbs - 1, mbs - 1},
		{"half-pel past bottom right", 1, 1, mbs - 1, mbs - 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, srcBacking := newGuardedFrame(lumaWidth, chromaWidth, 1, 0x5A)
			dst, dstBacking := newGuardedFrame(lumaWidth, chromaWidth, 0, 0xA5)
			want := newTestFrame(lumaWidth, chromaWidth, 0)

			v := &Video{lumaWidth: lumaWidth, chromaWidth: chromaWidth, pictureType: pictureTypePredictive,
				mbRow: c.mbRow, mbCol: c.mbCol, frameForward: *src, frameCurrent: *dst}
			v.motionForward.H, v.motionForward.V = c.motionH, c.motionV
			v.predictMacroblock()

			if !guardsIntact(dstBacking, 0xA5) || !guardsIntact(srcBacking, 0x5A) {
				t.Fatal("memory outside the frame planes was modified")
			}
			if string(dst.Y.Data) != string(want.Y.Data) ||
				string(dst.Cb.Data) != string(want.Cb.Data) ||
				string(dst.Cr.Data) != string(want.Cr.Data) {
				t.Fatal("destination modified by an out of bounds macroblock")
			}
		})
	}
}

func TestCopyMacroblockPaddedEdge(t *testing.T) {
	const mbWidth, mbHeight = 4, 3
	v := &Video{
		width:        mbWidth * 16,
		height:       mbHeight * 16,
		lumaWidth:    mbWidth * 16,
		lumaHeight:   mbHeight * 16,
		chromaWidth:  mbWidth * 8,
		chromaHeight: mbHeight * 8,
	}
	var src, got, want Frame
	v.initFrame(&src)
	v.initFrame(&got)
	v.initFrame(&want)
	base := src.Y.Data[:cap(src.Y.Data)]
	for i := range base {
		base[i] = byte(i*131 + 7)
	}

	wide := src
	wide.Y.Data = src.Y.Data[:cap(src.Y.Data)]
	wide.Cb.Data = src.Cb.Data[:cap(src.Cb.Data)]
	wide.Cr.Data = src.Cr.Data[:cap(src.Cr.Data)]

	const motionH, motionV = 1, 1
	mbRow, mbCol := mbHeight-1, mbWidth-1
	if !macroblockInBounds(motionH, motionV, mbRow, mbCol, v.lumaWidth, v.chromaWidth, &src, &got) {
		t.Fatal("a half-pel read into the padding was rejected")
	}
	copyMacroblock(motionH, motionV, mbRow, mbCol, v.lumaWidth, v.chromaWidth, &src, &got, false)
	copyMacroblockRef(motionH, motionV, mbRow, mbCol, v.lumaWidth, v.chromaWidth, &wide, &want, false)

	if string(got.Y.Data) != string(want.Y.Data) ||
		string(got.Cb.Data) != string(want.Cb.Data) ||
		string(got.Cr.Data) != string(want.Cr.Data) {
		t.Fatal("bottom right macroblock not predicted from the padded reference")
	}
}

func TestDecodeVlc(t *testing.T) {
	tables := map[string]*vlcTable{
		"address increment": vlcMacroblockAddressIncrement,
		"type intra":        vlcMacroblockType[1],
		"type predictive":   vlcMacroblockType[2],
		"type b":            vlcMacroblockType[3],
		"block pattern":     vlcCodeBlockPattern,
		"motion":            vlcMotion,
		"dct size luma":     vlcDctSize[0],
		"dct size chroma":   vlcDctSize[1],
		"dct coeff":         vlcDctCoeff,
	}
	for name, table := range tables {
		for seed := range 200 {
			data := make([]byte, 64)
			x := uint32(seed*2654435761 + 1)
			for i := range data {
				x ^= x << 13
				x ^= x >> 17
				x ^= x << 5
				data[i] = byte(x)
			}
			if seed%4 == 0 {
				clear(data[:seed%32])
			}

			want, _ := NewBuffer(nil)
			want.Write(data)
			got, _ := NewBuffer(nil)
			got.Write(data)
			for want.bitIndex < 32*8 {
				w := want.readVlc(table.tree)
				g := got.decodeVlc(table)
				if w != g || want.bitIndex != got.bitIndex {
					t.Fatalf("%s seed %d: decoded %d at bit %d, want %d at bit %d",
						name, seed, g, got.bitIndex, w, want.bitIndex)
				}
			}
		}
	}
}

func benchmarkCopyMacroblock(b *testing.B, motionH, motionV int, avg bool) {
	const lumaWidth, chromaWidth = 64, 32
	src := newTestFrame(lumaWidth, chromaWidth, 1)
	dst := newTestFrame(lumaWidth, chromaWidth, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copyMacroblock(motionH, motionV, 1, 1, lumaWidth, chromaWidth, src, dst, avg)
	}
}

func BenchmarkCopyMacroblockCopy(b *testing.B)        { benchmarkCopyMacroblock(b, 0, 0, false) }
func BenchmarkCopyMacroblockHoriz(b *testing.B)       { benchmarkCopyMacroblock(b, 1, 0, false) }
func BenchmarkCopyMacroblockVert(b *testing.B)        { benchmarkCopyMacroblock(b, 0, 1, false) }
func BenchmarkCopyMacroblockBilin(b *testing.B)       { benchmarkCopyMacroblock(b, 3, 3, false) }
func BenchmarkCopyMacroblockBilinInterp(b *testing.B) { benchmarkCopyMacroblock(b, 3, 3, true) }

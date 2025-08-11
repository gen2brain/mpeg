//go:build (!amd64 && !arm64) || noasm

package mpeg

import "unsafe"

func copyMacroblock(motionH, motionV, mbRow, mbCol, lumaWidth, chromaWidth int, s, d *Frame) {
	// We use 32bit writes here
	dY := unsafe.Slice((*uint32)(unsafe.Pointer(&d.Y.Data[0])), len(d.Y.Data)/4)
	dCb := unsafe.Slice((*uint32)(unsafe.Pointer(&d.Cb.Data[0])), len(d.Cb.Data)/4)
	dCr := unsafe.Slice((*uint32)(unsafe.Pointer(&d.Cr.Data[0])), len(d.Cr.Data)/4)

	// Luminance
	width := lumaWidth
	scan := width - 16

	hp := motionH >> 1
	vp := motionV >> 1
	oddH := (motionH & 1) == 1
	oddV := (motionV & 1) == 1

	si := ((mbRow<<4)+vp)*width + (mbCol << 4) + hp
	di := (mbRow*width + mbCol) << 2
	last := di + (width << 2)

	var y1, y2, y uint64

	if oddH {
		if oddV {
			for di < last {
				y1 = uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width])
				si++

				for x := 0; x < 4; x++ {
					y2 = uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width])
					si++
					y = ((y1 + y2 + 2) >> 2) & 0xff

					y1 = uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width])
					si++
					y |= ((y1 + y2 + 2) << 6) & 0xff00

					y2 = uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width])
					si++
					y |= ((y1 + y2 + 2) << 14) & 0xff0000

					y1 = uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width])
					si++
					y |= ((y1 + y2 + 2) << 22) & 0xff000000

					dY[di] = uint32(y)
					di++
				}
				di += scan >> 2
				si += scan - 1
			}
		} else {
			for di < last {
				y1 = uint64(s.Y.Data[si])
				si++
				for x := 0; x < 4; x++ {
					y2 = uint64(s.Y.Data[si])
					si++
					y = ((y1 + y2 + 1) >> 1) & 0xff

					y1 = uint64(s.Y.Data[si])
					si++
					y |= ((y1 + y2 + 1) << 7) & 0xff00

					y2 = uint64(s.Y.Data[si])
					si++
					y |= ((y1 + y2 + 1) << 15) & 0xff0000

					y1 = uint64(s.Y.Data[si])
					si++
					y |= ((y1 + y2 + 1) << 23) & 0xff000000

					dY[di] = uint32(y)
					di++
				}
				di += scan >> 2
				si += scan - 1
			}
		}
	} else {
		if oddV {
			for di < last {
				for x := 0; x < 4; x++ {
					y = ((uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width]) + 1) >> 1) & 0xff
					si++
					y |= ((uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width]) + 1) << 7) & 0xff00
					si++
					y |= ((uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width]) + 1) << 15) & 0xff0000
					si++
					y |= ((uint64(s.Y.Data[si]) + uint64(s.Y.Data[si+width]) + 1) << 23) & 0xff000000
					si++

					dY[di] = uint32(y)
					di++
				}
				di += scan >> 2
				si += scan
			}
		} else {
			for di < last {
				for x := 0; x < 4; x++ {
					y = uint64(s.Y.Data[si])
					si++
					y |= uint64(s.Y.Data[si]) << 8
					si++
					y |= uint64(s.Y.Data[si]) << 16
					si++
					y |= uint64(s.Y.Data[si]) << 24
					si++

					dY[di] = uint32(y)
					di++
				}
				di += scan >> 2
				si += scan
			}
		}
	}

	// Chrominance
	width = chromaWidth
	scan = width - 8

	hp = (motionH / 2) >> 1
	vp = (motionV / 2) >> 1
	oddH = ((motionH / 2) & 1) == 1
	oddV = ((motionV / 2) & 1) == 1

	si = ((mbRow<<3)+vp)*width + (mbCol << 3) + hp
	di = (mbRow*width + mbCol) << 1
	last = di + (width << 1)

	var cb1, cb2, cb, cr1, cr2, cr uint64
	if oddH {
		if oddV {
			for di < last {
				cr1 = uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width])
				cb1 = uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width])
				si++
				for x := 0; x < 2; x++ {
					cr2 = uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width])
					cb2 = uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width])
					si++
					cr = ((cr1 + cr2 + 2) >> 2) & 0xff
					cb = ((cb1 + cb2 + 2) >> 2) & 0xff

					cr1 = uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width])
					cb1 = uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width])
					si++
					cr |= ((cr1 + cr2 + 2) << 6) & 0xff00
					cb |= ((cb1 + cb2 + 2) << 6) & 0xff00

					cr2 = uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width])
					cb2 = uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width])
					si++
					cr |= ((cr1 + cr2 + 2) << 14) & 0xff0000
					cb |= ((cb1 + cb2 + 2) << 14) & 0xff0000

					cr1 = uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width])
					cb1 = uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width])
					si++
					cr |= ((cr1 + cr2 + 2) << 22) & 0xff000000
					cb |= ((cb1 + cb2 + 2) << 22) & 0xff000000

					dCr[di] = uint32(cr)
					dCb[di] = uint32(cb)
					di++
				}
				di += scan >> 2
				si += scan - 1
			}
		} else {
			for di < last {
				cr1 = uint64(s.Cr.Data[si])
				cb1 = uint64(s.Cb.Data[si])
				si++
				for x := 0; x < 2; x++ {
					cr2 = uint64(s.Cr.Data[si])
					cb2 = uint64(s.Cb.Data[si])
					si++
					cr = ((cr1 + cr2 + 1) >> 1) & 0xff
					cb = ((cb1 + cb2 + 1) >> 1) & 0xff

					cr1 = uint64(s.Cr.Data[si])
					cb1 = uint64(s.Cb.Data[si])
					si++
					cr |= ((cr1 + cr2 + 1) << 7) & 0xff00
					cb |= ((cb1 + cb2 + 1) << 7) & 0xff00

					cr2 = uint64(s.Cr.Data[si])
					cb2 = uint64(s.Cb.Data[si])
					si++
					cr |= ((cr1 + cr2 + 1) << 15) & 0xff0000
					cb |= ((cb1 + cb2 + 1) << 15) & 0xff0000

					cr1 = uint64(s.Cr.Data[si])
					cb1 = uint64(s.Cb.Data[si])
					si++
					cr |= ((cr1 + cr2 + 1) << 23) & 0xff000000
					cb |= ((cb1 + cb2 + 1) << 23) & 0xff000000

					dCr[di] = uint32(cr)
					dCb[di] = uint32(cb)
					di++
				}
				di += scan >> 2
				si += scan - 1
			}
		}
	} else {
		if oddV {
			for di < last {
				for x := 0; x < 2; x++ {
					cr = ((uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width]) + 1) >> 1) & 0xff
					cb = ((uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width]) + 1) >> 1) & 0xff
					si++

					cr |= ((uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width]) + 1) << 7) & 0xff00
					cb |= ((uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width]) + 1) << 7) & 0xff00
					si++

					cr |= ((uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width]) + 1) << 15) & 0xff0000
					cb |= ((uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width]) + 1) << 15) & 0xff0000
					si++

					cr |= ((uint64(s.Cr.Data[si]) + uint64(s.Cr.Data[si+width]) + 1) << 23) & 0xff000000
					cb |= ((uint64(s.Cb.Data[si]) + uint64(s.Cb.Data[si+width]) + 1) << 23) & 0xff000000
					si++

					dCr[di] = uint32(cr)
					dCb[di] = uint32(cb)
					di++
				}
				di += scan >> 2
				si += scan
			}
		} else {
			for di < last {
				for x := 0; x < 2; x++ {
					cr = uint64(s.Cr.Data[si])
					cb = uint64(s.Cb.Data[si])
					si++

					cr |= uint64(s.Cr.Data[si]) << 8
					cb |= uint64(s.Cb.Data[si]) << 8
					si++

					cr |= uint64(s.Cr.Data[si]) << 16
					cb |= uint64(s.Cb.Data[si]) << 16
					si++

					cr |= uint64(s.Cr.Data[si]) << 24
					cb |= uint64(s.Cb.Data[si]) << 24
					si++

					dCr[di] = uint32(cr)
					dCb[di] = uint32(cb)
					di++
				}
				di += scan >> 2
				si += scan
			}
		}
	}
}

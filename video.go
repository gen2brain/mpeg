package mpeg

import (
	"image"
	"image/color"
	"image/draw"
	"unsafe"
)

// Frame represents decoded video frame.
type Frame struct {
	Time float64

	Width  int
	Height int

	Y  Plane
	Cb Plane
	Cr Plane

	imYCbCr image.YCbCr
	imRGBA  image.RGBA
}

// YCbCr returns frame as image.YCbCr.
func (f *Frame) YCbCr() *image.YCbCr {
	return &f.imYCbCr
}

// RGBA returns frame as image.RGBA.
func (f *Frame) RGBA() *image.RGBA {
	b := f.imYCbCr.Bounds()
	draw.Draw(&f.imRGBA, b.Bounds(), &f.imYCbCr, b.Min, draw.Src)
	return &f.imRGBA
}

// Pixels returns frame as slice of color.RGBA.
func (f *Frame) Pixels() []color.RGBA {
	img := f.RGBA()
	return unsafe.Slice((*color.RGBA)(unsafe.Pointer(&img.Pix[0])), len(img.Pix)/4)
}

// Plane represents decoded video plane.
// The byte length of the data is width * height. Note that different planes have different sizes:
// the Luma plane (Y) is double the size of each of the two Chroma planes (Cr, Cb) - i.e. 4 times the byte length.
// Also note that the size of the plane does *not* denote the size of the displayed frame.
// The sizes of planes are always rounded up to the nearest macroblock (16px).
type Plane struct {
	Width  int
	Height int
	Data   []byte
}

// Video decodes MPEG-1 Video (mpeg1) data into raw YCrCb frames.
type Video struct {
	aspectRatio   float64
	frameRate     float64
	time          float64
	bitRate       int
	framesDecoded int
	width         int
	height        int
	mbWidth       int
	mbHeight      int
	mbSize        int

	lumaWidth  int
	lumaHeight int

	chromaWidth  int
	chromaHeight int

	startCode   int
	pictureType int

	motionForward  motion
	motionBackward motion

	hasSequenceHeader bool

	quantizerScale    int
	sliceBegin        bool
	macroblockAddress int

	mbRow int
	mbCol int

	macroblockType  int
	macroblockIntra bool

	dcPredictor []int

	buf *Buffer

	frameCurrent  Frame
	frameForward  Frame
	frameBackward Frame

	blockData           []int
	intraQuantMatrix    []byte
	nonIntraQuantMatrix []byte

	hasReferenceFrame bool
	assumeNoBFrames   bool
}

// NewVideo creates a video decoder with buffer as a source.
func NewVideo(buf *Buffer) *Video {
	video := &Video{}
	video.buf = buf

	video.dcPredictor = make([]int, 3)
	video.blockData = make([]int, 64)
	video.intraQuantMatrix = make([]byte, 64)
	video.nonIntraQuantMatrix = make([]byte, 64)

	// Attempt to decode the sequence header
	video.startCode = video.buf.findStartCode(startSequence)
	if video.startCode != -1 {
		video.decodeSequenceHeader()
	}

	return video
}

// Buffer returns video buffer.
func (v *Video) Buffer() *Buffer {
	return v.buf
}

// HasHeader checks whether a sequence header was found, and we can accurately report on
// dimensions and framerate.
func (v *Video) HasHeader() bool {
	if v.hasSequenceHeader {
		return true
	}

	if v.startCode != startSequence {
		v.startCode = v.buf.findStartCode(startSequence)
	}
	if v.startCode == -1 {
		return false
	}

	if !v.decodeSequenceHeader() {
		return false
	}

	return true
}

// Framerate returns the framerate in frames per second.
func (v *Video) Framerate() float64 {
	if v.HasHeader() {
		return v.frameRate
	}

	return 0
}

// Width returns the display width.
func (v *Video) Width() int {
	if v.HasHeader() {
		return v.width
	}

	return 0
}

// Height returns the display height.
func (v *Video) Height() int {
	if v.HasHeader() {
		return v.height
	}

	return 0
}

// SetNoDelay sets "no delay" mode. When enabled, the decoder assumes that the video does
// *not* contain any B-Frames. This is useful for reducing lag when streaming.
func (v *Video) SetNoDelay(noDelay bool) {
	v.assumeNoBFrames = noDelay
}

// Time returns the current internal time in seconds.
func (v *Video) Time() float64 {
	return v.time
}

// SetTime sets the current internal time in seconds. This is only useful when you
// manipulate the underlying video buffer and want to enforce a correct timestamps.
func (v *Video) SetTime(time float64) {
	v.framesDecoded = int(v.frameRate * v.time)
	v.time = time
}

// Rewind rewinds the internal buffer.
func (v *Video) Rewind() {
	v.buf.Rewind()
	v.time = 0
	v.framesDecoded = 0
	v.hasReferenceFrame = false
	v.startCode = -1
}

// HasEnded checks whether the file has ended. This will be cleared on rewind.
func (v *Video) HasEnded() bool {
	return v.buf.HasEnded()
}

// Decode decodes and returns one frame of video and advance the internal time by 1/framerate seconds.
func (v *Video) Decode() *Frame {
	if !v.HasHeader() {
		return nil
	}

	var frame *Frame

	for {
		if v.startCode != startPicture {
			v.startCode = v.buf.findStartCode(startPicture)

			if v.startCode == -1 {
				// If we reached the end of the file and the previously decoded
				// frame was a reference frame, we still have to return it.
				if v.hasReferenceFrame && !v.assumeNoBFrames && v.buf.HasEnded() &&
					(v.pictureType == pictureTypeIntra || v.pictureType == pictureTypePredictive) {
					v.hasReferenceFrame = false
					frame = &v.frameBackward
					break
				}

				return nil
			}
		}

		// Make sure we have a full picture in the buffer before attempting to
		// decode it. Sadly, this can only be done by seeking for the start code
		// of the next picture. Also, if we didn't find the start code for the
		// next picture, but the source has ended, we assume that this last
		// picture is in the buffer.
		if v.buf.hasStartCode(startPicture) == -1 && !v.buf.HasEnded() {
			return nil
		}
		v.buf.discardReadBytes()

		v.decodePicture()

		switch {
		case v.assumeNoBFrames:
			frame = &v.frameBackward
		case v.pictureType == pictureTypeB:
			frame = &v.frameCurrent
		case v.hasReferenceFrame:
			frame = &v.frameForward
		default:
			v.hasReferenceFrame = true
		}

		if frame != nil {
			break
		}
	}

	frame.Time = v.time
	v.framesDecoded++
	v.time = float64(v.framesDecoded) / v.frameRate

	return frame
}

func (v *Video) decodeSequenceHeader() bool {
	maxHeaderSize := 64 + 2*64*8 // 64 bit header + 2x 64 byte matrix
	if !v.buf.has(maxHeaderSize) {
		return false
	}

	v.width = v.buf.read(12)
	v.height = v.buf.read(12)

	if v.width <= 0 || v.height <= 0 {
		return false
	}

	v.aspectRatio = videoAspectRatio[v.buf.read(4)]
	v.frameRate = videoPictureRate[v.buf.read(4)]
	v.bitRate = v.buf.read(18)

	// Skip marker, buffer_size and constrained bit
	v.buf.skip(1 + 10 + 1)

	// Load custom intra quant matrix?
	if v.buf.read(1) != 0 {
		for i := 0; i < 64; i++ {
			idx := videoZigZag[i]
			v.intraQuantMatrix[idx] = byte(v.buf.read(8))
		}
	} else {
		copy(v.intraQuantMatrix, videoIntraQuantMatrix)
	}

	// Load custom non intra quant matrix?
	if v.buf.read(1) != 0 {
		for i := 0; i < 64; i++ {
			idx := videoZigZag[i]
			v.nonIntraQuantMatrix[idx] = byte(v.buf.read(8))
		}
	} else {
		copy(v.nonIntraQuantMatrix, videoNonIntraQuantMatrix)
	}

	v.mbWidth = (v.width + 15) >> 4
	v.mbHeight = (v.height + 15) >> 4
	v.mbSize = v.mbWidth * v.mbHeight

	v.lumaWidth = v.mbWidth << 4
	v.lumaHeight = v.mbHeight << 4

	v.chromaWidth = v.mbWidth << 3
	v.chromaHeight = v.mbHeight << 3

	v.initFrame(&v.frameCurrent)
	v.initFrame(&v.frameForward)
	v.initFrame(&v.frameBackward)

	v.hasSequenceHeader = true
	return true
}

func (v *Video) initFrame(frame *Frame) {
	lumaSize := v.lumaWidth * v.lumaHeight
	chromaSize := v.chromaWidth * v.chromaHeight
	frameSize := lumaSize + 2*chromaSize

	base := make([]byte, frameSize)

	frame.Width = v.width
	frame.Height = v.height

	frame.Y.Width = v.lumaWidth
	frame.Y.Height = v.lumaHeight
	frame.Y.Data = base[0:lumaSize:lumaSize]

	frame.Cb.Width = v.chromaWidth
	frame.Cb.Height = v.chromaHeight
	frame.Cb.Data = base[lumaSize : lumaSize+chromaSize : lumaSize+chromaSize]

	frame.Cr.Width = v.chromaWidth
	frame.Cr.Height = v.chromaHeight
	frame.Cr.Data = base[lumaSize+chromaSize : frameSize : frameSize]

	frame.imYCbCr = image.YCbCr{
		Y:              frame.Y.Data,
		Cb:             frame.Cb.Data,
		Cr:             frame.Cr.Data,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		YStride:        v.lumaWidth,
		CStride:        v.chromaWidth,
		Rect:           image.Rect(0, 0, v.width, v.height),
	}

	frame.imRGBA = image.RGBA{
		Pix:    make([]byte, v.width*v.height*4),
		Stride: 4 * v.width,
		Rect:   image.Rect(0, 0, v.width, v.height),
	}
}

func (v *Video) decodePicture() {
	v.buf.skip(10) // skip temporalReference
	v.pictureType = v.buf.read(3)
	v.buf.skip(16) // skip vbv_delay

	// D frames or unknown coding type
	if v.pictureType <= 0 || v.pictureType > pictureTypeB {
		return
	}

	// Forward fullPx, fCode
	if v.pictureType == pictureTypePredictive || v.pictureType == pictureTypeB {
		v.motionForward.FullPx = v.buf.read(1)
		fCode := v.buf.read(3)
		if fCode == 0 {
			// Ignore picture with zero fCode
			return
		}
		v.motionForward.RSize = fCode - 1
	}

	// Backward fullPx, fCode
	if v.pictureType == pictureTypeB {
		v.motionBackward.FullPx = v.buf.read(1)
		fCode := v.buf.read(3)
		if fCode == 0 {
			// Ignore picture with zero fCode
			return
		}
		v.motionBackward.RSize = fCode - 1
	}

	frameTemp := v.frameForward
	if v.pictureType == pictureTypeIntra || v.pictureType == pictureTypePredictive {
		v.frameForward = v.frameBackward
	}

	// Find first slice start code; skip extension and user data
	for {
		v.startCode = v.buf.nextStartCode()

		if v.startCode != startExtension && v.startCode != startUserData {
			break
		}
	}

	// Decode all slices
	for startIsSlice(v.startCode) {
		v.decodeSlice(v.startCode & 0x000000FF)
		if v.macroblockAddress >= v.mbSize-2 {
			break
		}
		v.startCode = v.buf.nextStartCode()
	}

	// If this is a reference picture rotate the prediction pointers
	if v.pictureType == pictureTypeIntra || v.pictureType == pictureTypePredictive {
		v.frameBackward = v.frameCurrent
		v.frameCurrent = frameTemp
	}
}

func (v *Video) decodeSlice(slice int) {
	v.sliceBegin = true
	v.macroblockAddress = (slice-1)*v.mbWidth - 1

	// Reset motion vectors and DC predictors
	v.motionBackward.H, v.motionForward.H = 0, 0
	v.motionBackward.V, v.motionForward.V = 0, 0
	v.dcPredictor[0] = 128
	v.dcPredictor[1] = 128
	v.dcPredictor[2] = 128

	v.quantizerScale = v.buf.read(5)

	// Skip extra
	for v.buf.read(1) != 0 {
		v.buf.skip(8)
	}

	for {
		v.decodeMacroblock()
		if v.macroblockAddress >= v.mbSize-1 || !v.buf.peekNonZero(23) {
			break
		}
	}
}

func (v *Video) decodeMacroblock() {
	// Decode increment
	increment := 0
	t := v.buf.readVlc(videoMacroblockAddressIncrement)

	for t == 34 {
		// macroblock_stuffing
		t = v.buf.readVlc(videoMacroblockAddressIncrement)
	}
	for t == 35 {
		// macroblock_escape
		increment += 33
		t = v.buf.readVlc(videoMacroblockAddressIncrement)
	}
	increment += t

	// Process any skipped macroblocks
	if v.sliceBegin {
		// The first increment of each slice is relative to beginning of the
		// previous row, not the previous macroblock
		v.sliceBegin = false
		v.macroblockAddress += increment
	} else {
		if v.macroblockAddress+increment >= v.mbSize {
			return // invalid
		}

		if increment > 1 {
			// Skipped macroblocks reset DC predictors
			v.dcPredictor[0] = 128
			v.dcPredictor[1] = 128
			v.dcPredictor[2] = 128

			// Skipped macroblocks in P-pictures reset motion vectors
			if v.pictureType == pictureTypePredictive {
				v.motionForward.H = 0
				v.motionForward.V = 0
			}
		}

		// Predict skipped macroblocks
		for increment > 1 {
			v.macroblockAddress++
			v.mbRow = v.macroblockAddress / v.mbWidth
			v.mbCol = v.macroblockAddress % v.mbWidth

			v.predictMacroblock()
			increment--
		}
		v.macroblockAddress++
	}

	v.mbRow = v.macroblockAddress / v.mbWidth
	v.mbCol = v.macroblockAddress % v.mbWidth

	if v.mbCol >= v.mbWidth || v.mbRow >= v.mbHeight {
		return // corrupt stream
	}

	// Process the current macroblock
	v.macroblockType = v.buf.readVlc(videoMacroBlockType[v.pictureType])

	v.macroblockIntra = v.macroblockType&0x01 != 0
	v.motionForward.IsSet = v.macroblockType&0x08 != 0
	v.motionBackward.IsSet = v.macroblockType&0x04 != 0

	// Quantizer scale
	if (v.macroblockType & 0x10) != 0 {
		v.quantizerScale = v.buf.read(5)
	}

	if v.macroblockIntra {
		// Intra-coded macroblocks reset motion vectors
		v.motionBackward.H, v.motionForward.H = 0, 0
		v.motionBackward.V, v.motionForward.V = 0, 0
	} else {
		// Non-intra macroblocks reset DC predictors
		v.dcPredictor[0] = 128
		v.dcPredictor[1] = 128
		v.dcPredictor[2] = 128

		v.decodeMotionVectors()
		v.predictMacroblock()
	}

	// Decode blocks
	cbp := 0
	if (v.macroblockType & 0x02) != 0 {
		cbp = v.buf.readVlc(videoCodeBlockPattern)
	} else if v.macroblockIntra {
		cbp = 0x3f
	}

	mask := 0x20
	for block := 0; block < 6; block++ {
		if (cbp & mask) != 0 {
			v.decodeBlock(block)
		}
		mask >>= 1
	}
}

func (v *Video) decodeMotionVectors() {
	// Forward
	if v.motionForward.IsSet {
		rSize := v.motionForward.RSize
		v.motionForward.H = v.decodeMotionVector(rSize, v.motionForward.H)
		v.motionForward.V = v.decodeMotionVector(rSize, v.motionForward.V)
	} else if v.pictureType == pictureTypePredictive {
		// No motion information in P-picture, reset vectors
		v.motionForward.H = 0
		v.motionForward.V = 0
	}

	if v.motionBackward.IsSet {
		rSize := v.motionBackward.RSize
		v.motionBackward.H = v.decodeMotionVector(rSize, v.motionBackward.H)
		v.motionBackward.V = v.decodeMotionVector(rSize, v.motionBackward.V)
	}
}

func (v *Video) decodeMotionVector(rSize, motion int) int {
	fscale := 1 << rSize
	mCode := v.buf.readVlc(videoMotion)
	var r, d int

	if mCode != 0 && fscale != 1 {
		r = v.buf.read(rSize)
		d = ((abs(mCode) - 1) << rSize) + r + 1
		if mCode < 0 {
			d = -d
		}
	} else {
		d = mCode
	}

	motion += d
	if motion > (fscale<<4)-1 {
		motion -= fscale << 5
	} else if motion < ((-fscale) << 4) {
		motion += fscale << 5
	}

	return motion
}

func (v *Video) predictMacroblock() {
	fwH := v.motionForward.H
	fwV := v.motionForward.V

	if v.motionForward.FullPx != 0 {
		fwH <<= 1
		fwV <<= 1
	}

	if v.pictureType == pictureTypeB {
		bwH := v.motionBackward.H
		bwV := v.motionBackward.V

		if v.motionBackward.FullPx != 0 {
			bwH <<= 1
			bwV <<= 1
		}

		if v.motionForward.IsSet {
			v.copyMacroblock(fwH, fwV, &v.frameForward)
			if v.motionBackward.IsSet {
				v.copyMacroblock(bwH, bwV, &v.frameBackward)
			}
		} else {
			v.copyMacroblock(bwH, bwV, &v.frameBackward)
		}
	} else {
		v.copyMacroblock(fwH, fwV, &v.frameForward)
	}
}

func (v *Video) copyMacroblock(motionH, motionV int, s *Frame) {
	// We use 32bit writes here
	d := &v.frameCurrent
	dY := unsafe.Slice((*uint32)(unsafe.Pointer(&d.Y.Data[0])), len(d.Y.Data)/4)
	dCb := unsafe.Slice((*uint32)(unsafe.Pointer(&d.Cb.Data[0])), len(d.Cb.Data)/4)
	dCr := unsafe.Slice((*uint32)(unsafe.Pointer(&d.Cr.Data[0])), len(d.Cr.Data)/4)

	// Luminance
	width := v.lumaWidth
	scan := width - 16

	hp := motionH >> 1
	vp := motionV >> 1
	oddH := (motionH & 1) == 1
	oddV := (motionV & 1) == 1

	si := ((v.mbRow<<4)+vp)*width + (v.mbCol << 4) + hp
	di := (v.mbRow*width + v.mbCol) << 2
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
	width = v.chromaWidth
	scan = width - 8

	hp = (motionH / 2) >> 1
	vp = (motionV / 2) >> 1
	oddH = ((motionH / 2) & 1) == 1
	oddV = ((motionV / 2) & 1) == 1

	si = ((v.mbRow<<3)+vp)*width + (v.mbCol << 3) + hp
	di = (v.mbRow*width + v.mbCol) << 1
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

func (v *Video) decodeBlock(block int) {
	var n int
	var quantMatrix []byte

	// Decode DC coefficient of intra-coded blocks
	if v.macroblockIntra {
		var predictor int
		var dctSize int

		// DC prediction
		planeIndex := 0
		if block > 3 {
			planeIndex = block - 3
		}
		predictor = v.dcPredictor[planeIndex]
		dctSize = v.buf.readVlc(videoDctSize[planeIndex])

		// Read DC coeff
		if dctSize > 0 {
			differential := v.buf.read(dctSize)
			if (differential & (1 << (dctSize - 1))) != 0 {
				v.blockData[0] = predictor + differential
			} else {
				v.blockData[0] = predictor + ((-1 << dctSize) | (differential + 1))
			}
		} else {
			v.blockData[0] = predictor
		}

		// Save predictor value
		v.dcPredictor[planeIndex] = v.blockData[0]

		// Dequantize + premultiply
		v.blockData[0] <<= 3 + 5

		quantMatrix = v.intraQuantMatrix
		n = 1
	} else {
		quantMatrix = v.nonIntraQuantMatrix
	}

	// Decode AC coefficients (+DC for non-intra)
	level := 0
	for {
		run := 0
		coeff := int(v.buf.readVlcUint(videoDctCoeff))

		if (coeff == 0x0001) && (n > 0) && (v.buf.read(1) == 0) {
			// end_of_block
			break
		}

		if coeff == 0xffff {
			// escape
			run = v.buf.read(6)
			level = v.buf.read(8)
			switch {
			case level == 0:
				level = v.buf.read(8)
			case level == 128:
				level = v.buf.read(8) - 256
			case level > 128:
				level -= 256
			}
		} else {
			run = coeff >> 8
			level = coeff & 0xff
			if (v.buf.read(1)) != 0 {
				level = -level
			}
		}

		n += run
		if n < 0 || n >= 64 {
			return // invalid
		}

		deZigZagged := videoZigZag[n]
		n++

		// Dequantize, oddify, clip
		level <<= 1
		if !v.macroblockIntra {
			if level < 0 {
				level += -1
			} else {
				level += 1
			}
		}

		level = (level * v.quantizerScale * int(quantMatrix[deZigZagged])) >> 4
		if (level & 1) == 0 {
			if level > 0 {
				level -= 1
			} else {
				level -= -1
			}
		}
		if level > 2047 {
			level = 2047
		} else if level < -2048 {
			level = -2048
		}

		// Save premultiplied coefficient
		v.blockData[deZigZagged] = level * int(videoPremultiplierMatrix[deZigZagged])
	}

	// Move block to its place
	var d []byte
	var di int
	var scan int

	if block < 4 {
		d = v.frameCurrent.Y.Data
		di = (v.mbRow*v.lumaWidth + v.mbCol) << 4
		scan = v.lumaWidth - 8
		if (block & 1) != 0 {
			di += 8
		}
		if (block & 2) != 0 {
			di += v.lumaWidth << 3
		}
	} else {
		if block == 4 {
			d = v.frameCurrent.Cb.Data
		} else {
			d = v.frameCurrent.Cr.Data
		}
		di = ((v.mbRow * v.lumaWidth) << 2) + (v.mbCol << 3)
		scan = (v.lumaWidth >> 1) - 8
	}

	s := v.blockData
	if v.macroblockIntra {
		// Overwrite (no prediction)
		if n == 1 {
			value := (s[0] + 128) >> 8
			copyValueToDest(int(clamp(value)), d, di, scan)
			s[0] = 0
		} else {
			v.idct(s)
			copyBlockToDest(s, d, di, scan)
			for i := range v.blockData {
				v.blockData[i] = 0
			}
		}
	} else {
		// Add data to the predicted macroblock
		if n == 1 {
			value := (s[0] + 128) >> 8
			addValueToDest(value, d, di, scan)
			s[0] = 0
		} else {
			v.idct(s)
			addBlockToDest(s, d, di, scan)
			for i := range v.blockData {
				v.blockData[i] = 0
			}
		}
	}
}

func (v *Video) idct(block []int) {
	// See http://vsr.informatik.tu-chemnitz.de/~jan/MPEG/HTML/IDCT.html for more info.

	var b1, b3, b4, b6, b7, tmp1, tmp2, m0,
		x0, x1, x2, x3, x4, y3, y4, y5, y6, y7 int

	// Transform columns
	for i := 0; i < 8; i++ {
		b1 = block[4*8+i]
		b3 = block[2*8+i] + block[6*8+i]
		b4 = block[5*8+i] - block[3*8+i]
		tmp1 = block[1*8+i] + block[7*8+i]
		tmp2 = block[3*8+i] + block[5*8+i]
		b6 = block[1*8+i] - block[7*8+i]
		b7 = tmp1 + tmp2
		m0 = block[0*8+i]
		x4 = ((b6*473 - b4*196 + 128) >> 8) - b7
		x0 = x4 - (((tmp1-tmp2)*362 + 128) >> 8)
		x1 = m0 - b1
		x2 = (((block[2*8+i]-block[6*8+i])*362 + 128) >> 8) - b3
		x3 = m0 + b1
		y3 = x1 + x2
		y4 = x3 + b3
		y5 = x1 - x2
		y6 = x3 - b3
		y7 = -x0 - ((b4*473 + b6*196 + 128) >> 8)
		block[0*8+i] = b7 + y4
		block[1*8+i] = x4 + y3
		block[2*8+i] = y5 - x0
		block[3*8+i] = y6 - y7
		block[4*8+i] = y6 + y7
		block[5*8+i] = x0 + y5
		block[6*8+i] = y3 - x4
		block[7*8+i] = y4 - b7
	}

	// Transform rows
	for i := 0; i < 64; i += 8 {
		b1 = block[4+i]
		b3 = block[2+i] + block[6+i]
		b4 = block[5+i] - block[3+i]
		tmp1 = block[1+i] + block[7+i]
		tmp2 = block[3+i] + block[5+i]
		b6 = block[1+i] - block[7+i]
		b7 = tmp1 + tmp2
		m0 = block[0+i]
		x4 = ((b6*473 - b4*196 + 128) >> 8) - b7
		x0 = x4 - (((tmp1-tmp2)*362 + 128) >> 8)
		x1 = m0 - b1
		x2 = (((block[2+i]-block[6+i])*362 + 128) >> 8) - b3
		x3 = m0 + b1
		y3 = x1 + x2
		y4 = x3 + b3
		y5 = x1 - x2
		y6 = x3 - b3
		y7 = -x0 - ((b4*473 + b6*196 + 128) >> 8)
		block[0+i] = (b7 + y4 + 128) >> 8
		block[1+i] = (x4 + y3 + 128) >> 8
		block[2+i] = (y5 - x0 + 128) >> 8
		block[3+i] = (y6 - y7 + 128) >> 8
		block[4+i] = (y6 + y7 + 128) >> 8
		block[5+i] = (x0 + y5 + 128) >> 8
		block[6+i] = (y3 - x4 + 128) >> 8
		block[7+i] = (y4 - b7 + 128) >> 8
	}
}

const (
	pictureTypeIntra      = 1
	pictureTypePredictive = 2
	pictureTypeB          = 3

	startPicture    = 0x00
	startSliceFirst = 0x01
	startSliceLast  = 0xAF
	startUserData   = 0xB2
	startSequence   = 0xB3
	startExtension  = 0xB5
)

func copyBlockToDest(block []int, dest []byte, index, scan int) {
	for n := 0; n < 64; n += 8 {
		dest[index+0] = clamp(block[n+0])
		dest[index+1] = clamp(block[n+1])
		dest[index+2] = clamp(block[n+2])
		dest[index+3] = clamp(block[n+3])
		dest[index+4] = clamp(block[n+4])
		dest[index+5] = clamp(block[n+5])
		dest[index+6] = clamp(block[n+6])
		dest[index+7] = clamp(block[n+7])

		index += scan + 8
	}
}

func addBlockToDest(block []int, dest []byte, index, scan int) {
	for n := 0; n < 64; n += 8 {
		dest[index+0] = clamp(int(dest[index+0]) + block[n+0])
		dest[index+1] = clamp(int(dest[index+1]) + block[n+1])
		dest[index+2] = clamp(int(dest[index+2]) + block[n+2])
		dest[index+3] = clamp(int(dest[index+3]) + block[n+3])
		dest[index+4] = clamp(int(dest[index+4]) + block[n+4])
		dest[index+5] = clamp(int(dest[index+5]) + block[n+5])
		dest[index+6] = clamp(int(dest[index+6]) + block[n+6])
		dest[index+7] = clamp(int(dest[index+7]) + block[n+7])

		index += scan + 8
	}

}

func copyValueToDest(value int, dest []byte, index, scan int) {
	val := clamp(value)
	for n := 0; n < 64; n += 8 {
		dest[index+0] = val
		dest[index+1] = val
		dest[index+2] = val
		dest[index+3] = val
		dest[index+4] = val
		dest[index+5] = val
		dest[index+6] = val
		dest[index+7] = val

		index += scan + 8
	}

}

func addValueToDest(value int, dest []byte, index, scan int) {
	for n := 0; n < 64; n += 8 {
		dest[index+0] = clamp(int(dest[index+0]) + value)
		dest[index+1] = clamp(int(dest[index+1]) + value)
		dest[index+2] = clamp(int(dest[index+2]) + value)
		dest[index+3] = clamp(int(dest[index+3]) + value)
		dest[index+4] = clamp(int(dest[index+4]) + value)
		dest[index+5] = clamp(int(dest[index+5]) + value)
		dest[index+6] = clamp(int(dest[index+6]) + value)
		dest[index+7] = clamp(int(dest[index+7]) + value)

		index += scan + 8
	}

}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func clamp(n int) byte {
	if n > 255 {
		n = 255
	} else if n < 0 {
		n = 0
	}

	return byte(n)
}

func startIsSlice(c int) bool {
	if c >= startSliceFirst && c <= startSliceLast {
		return true
	}
	return false
}

type motion struct {
	FullPx int
	RSize  int
	H      int
	V      int
	IsSet  bool
}

var videoPictureRate = []float64{
	0.000, 23.976, 24.000, 25.000, 29.970, 30.000, 50.000, 59.940,
	60.000, 0.000, 0.000, 0.000, 0.000, 0.000, 0.000, 0.000,
}

var videoAspectRatio = []float64{
	0.0000, 1.0000, 0.6735, 0.7031, 0.7615, 0.8055, 0.8437, 0.8935,
	0.9375, 0.9815, 1.0255, 1.0695, 1.1250, 1.1575, 1.2015, 0.0000,
}

var videoZigZag = []byte{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

var videoIntraQuantMatrix = []byte{
	8, 16, 19, 22, 26, 27, 29, 34,
	16, 16, 22, 24, 27, 29, 34, 37,
	19, 22, 26, 27, 29, 34, 34, 38,
	22, 22, 26, 27, 29, 34, 37, 40,
	22, 26, 27, 29, 32, 35, 40, 48,
	26, 27, 29, 32, 35, 40, 48, 58,
	26, 27, 29, 34, 38, 46, 56, 69,
	27, 29, 35, 38, 46, 56, 69, 83,
}

var videoNonIntraQuantMatrix = []byte{
	16, 16, 16, 16, 16, 16, 16, 16,
	16, 16, 16, 16, 16, 16, 16, 16,
	16, 16, 16, 16, 16, 16, 16, 16,
	16, 16, 16, 16, 16, 16, 16, 16,
	16, 16, 16, 16, 16, 16, 16, 16,
	16, 16, 16, 16, 16, 16, 16, 16,
	16, 16, 16, 16, 16, 16, 16, 16,
	16, 16, 16, 16, 16, 16, 16, 16,
}

var videoPremultiplierMatrix = []byte{
	32, 44, 42, 38, 32, 25, 17, 9,
	44, 62, 58, 52, 44, 35, 24, 12,
	42, 58, 55, 49, 42, 33, 23, 12,
	38, 52, 49, 44, 38, 30, 20, 10,
	32, 44, 42, 38, 32, 25, 17, 9,
	25, 35, 33, 30, 25, 20, 14, 7,
	17, 24, 23, 20, 17, 14, 9, 5,
	9, 12, 12, 10, 9, 7, 5, 2,
}

var videoMacroblockAddressIncrement = []vlc{
	{1 << 1, 0}, {0, 1}, //   0: x
	{2 << 1, 0}, {3 << 1, 0}, //   1: 0x
	{4 << 1, 0}, {5 << 1, 0}, //   2: 00x
	{0, 3}, {0, 2}, //   3: 01x
	{6 << 1, 0}, {7 << 1, 0}, //   4: 000x
	{0, 5}, {0, 4}, //   5: 001x
	{8 << 1, 0}, {9 << 1, 0}, //   6: 0000x
	{0, 7}, {0, 6}, //   7: 0001x
	{10 << 1, 0}, {11 << 1, 0}, //   8: 0000 0x
	{12 << 1, 0}, {13 << 1, 0}, //   9: 0000 1x
	{14 << 1, 0}, {15 << 1, 0}, //  10: 0000 00x
	{16 << 1, 0}, {17 << 1, 0}, //  11: 0000 01x
	{18 << 1, 0}, {19 << 1, 0}, //  12: 0000 10x
	{0, 9}, {0, 8}, //  13: 0000 11x
	{-1, 0}, {20 << 1, 0}, //  14: 0000 000x
	{-1, 0}, {21 << 1, 0}, //  15: 0000 001x
	{22 << 1, 0}, {23 << 1, 0}, //  16: 0000 010x
	{0, 15}, {0, 14}, //  17: 0000 011x
	{0, 13}, {0, 12}, //  18: 0000 100x
	{0, 11}, {0, 10}, //  19: 0000 101x
	{24 << 1, 0}, {25 << 1, 0}, //  20: 0000 0001x
	{26 << 1, 0}, {27 << 1, 0}, //  21: 0000 0011x
	{28 << 1, 0}, {29 << 1, 0}, //  22: 0000 0100x
	{30 << 1, 0}, {31 << 1, 0}, //  23: 0000 0101x
	{32 << 1, 0}, {-1, 0}, //  24: 0000 0001 0x
	{-1, 0}, {33 << 1, 0}, //  25: 0000 0001 1x
	{34 << 1, 0}, {35 << 1, 0}, //  26: 0000 0011 0x
	{36 << 1, 0}, {37 << 1, 0}, //  27: 0000 0011 1x
	{38 << 1, 0}, {39 << 1, 0}, //  28: 0000 0100 0x
	{0, 21}, {0, 20}, //  29: 0000 0100 1x
	{0, 19}, {0, 18}, //  30: 0000 0101 0x
	{0, 17}, {0, 16}, //  31: 0000 0101 1x
	{0, 35}, {-1, 0}, //  32: 0000 0001 00x
	{-1, 0}, {0, 34}, //  33: 0000 0001 11x
	{0, 33}, {0, 32}, //  34: 0000 0011 00x
	{0, 31}, {0, 30}, //  35: 0000 0011 01x
	{0, 29}, {0, 28}, //  36: 0000 0011 10x
	{0, 27}, {0, 26}, //  37: 0000 0011 11x
	{0, 25}, {0, 24}, //  38: 0000 0100 00x
	{0, 23}, {0, 22}, //  39: 0000 0100 01x
}

var videoMacroblockTypeIntra = []vlc{
	{1 << 1, 0}, {0, 0x01}, //   0: x
	{-1, 0}, {0, 0x11}, //   1: 0x
}

var videoMacroblockTypePredictive = []vlc{
	{1 << 1, 0}, {0, 0x0a}, //   0: x
	{2 << 1, 0}, {0, 0x02}, //   1: 0x
	{3 << 1, 0}, {0, 0x08}, //   2: 00x
	{4 << 1, 0}, {5 << 1, 0}, //   3: 000x
	{6 << 1, 0}, {0, 0x12}, //   4: 0000x
	{0, 0x1a}, {0, 0x01}, //   5: 0001x
	{-1, 0}, {0, 0x11}, //   6: 0000 0x
}

var videoMacroblockTypeB = []vlc{
	{1 << 1, 0}, {2 << 1, 0}, //   0: x
	{3 << 1, 0}, {4 << 1, 0}, //   1: 0x
	{0, 0x0c}, {0, 0x0e}, //   2: 1x
	{5 << 1, 0}, {6 << 1, 0}, //   3: 00x
	{0, 0x04}, {0, 0x06}, //   4: 01x
	{7 << 1, 0}, {8 << 1, 0}, //   5: 000x
	{0, 0x08}, {0, 0x0a}, //   6: 001x
	{9 << 1, 0}, {10 << 1, 0}, //   7: 0000x
	{0, 0x1e}, {0, 0x01}, //   8: 0001x
	{-1, 0}, {0, 0x11}, //   9: 0000 0x
	{0, 0x16}, {0, 0x1a}, //  10: 0000 1x
}

var videoMacroBlockType = [][]vlc{
	nil,
	videoMacroblockTypeIntra,
	videoMacroblockTypePredictive,
	videoMacroblockTypeB,
}

var videoCodeBlockPattern = []vlc{
	{1 << 1, 0}, {2 << 1, 0}, //   0: x
	{3 << 1, 0}, {4 << 1, 0}, //   1: 0x
	{5 << 1, 0}, {6 << 1, 0}, //   2: 1x
	{7 << 1, 0}, {8 << 1, 0}, //   3: 00x
	{9 << 1, 0}, {10 << 1, 0}, //   4: 01x
	{11 << 1, 0}, {12 << 1, 0}, //   5: 10x
	{13 << 1, 0}, {0, 60}, //   6: 11x
	{14 << 1, 0}, {15 << 1, 0}, //   7: 000x
	{16 << 1, 0}, {17 << 1, 0}, //   8: 001x
	{18 << 1, 0}, {19 << 1, 0}, //   9: 010x
	{20 << 1, 0}, {21 << 1, 0}, //  10: 011x
	{22 << 1, 0}, {23 << 1, 0}, //  11: 100x
	{0, 32}, {0, 16}, //  12: 101x
	{0, 8}, {0, 4}, //  13: 110x
	{24 << 1, 0}, {25 << 1, 0}, //  14: 0000x
	{26 << 1, 0}, {27 << 1, 0}, //  15: 0001x
	{28 << 1, 0}, {29 << 1, 0}, //  16: 0010x
	{30 << 1, 0}, {31 << 1, 0}, //  17: 0011x
	{0, 62}, {0, 2}, //  18: 0100x
	{0, 61}, {0, 1}, //  19: 0101x
	{0, 56}, {0, 52}, //  20: 0110x
	{0, 44}, {0, 28}, //  21: 0111x
	{0, 40}, {0, 20}, //  22: 1000x
	{0, 48}, {0, 12}, //  23: 1001x
	{32 << 1, 0}, {33 << 1, 0}, //  24: 0000 0x
	{34 << 1, 0}, {35 << 1, 0}, //  25: 0000 1x
	{36 << 1, 0}, {37 << 1, 0}, //  26: 0001 0x
	{38 << 1, 0}, {39 << 1, 0}, //  27: 0001 1x
	{40 << 1, 0}, {41 << 1, 0}, //  28: 0010 0x
	{42 << 1, 0}, {43 << 1, 0}, //  29: 0010 1x
	{0, 63}, {0, 3}, //  30: 0011 0x
	{0, 36}, {0, 24}, //  31: 0011 1x
	{44 << 1, 0}, {45 << 1, 0}, //  32: 0000 00x
	{46 << 1, 0}, {47 << 1, 0}, //  33: 0000 01x
	{48 << 1, 0}, {49 << 1, 0}, //  34: 0000 10x
	{50 << 1, 0}, {51 << 1, 0}, //  35: 0000 11x
	{52 << 1, 0}, {53 << 1, 0}, //  36: 0001 00x
	{54 << 1, 0}, {55 << 1, 0}, //  37: 0001 01x
	{56 << 1, 0}, {57 << 1, 0}, //  38: 0001 10x
	{58 << 1, 0}, {59 << 1, 0}, //  39: 0001 11x
	{0, 34}, {0, 18}, //  40: 0010 00x
	{0, 10}, {0, 6}, //  41: 0010 01x
	{0, 33}, {0, 17}, //  42: 0010 10x
	{0, 9}, {0, 5}, //  43: 0010 11x
	{-1, 0}, {60 << 1, 0}, //  44: 0000 000x
	{61 << 1, 0}, {62 << 1, 0}, //  45: 0000 001x
	{0, 58}, {0, 54}, //  46: 0000 010x
	{0, 46}, {0, 30}, //  47: 0000 011x
	{0, 57}, {0, 53}, //  48: 0000 100x
	{0, 45}, {0, 29}, //  49: 0000 101x
	{0, 38}, {0, 26}, //  50: 0000 110x
	{0, 37}, {0, 25}, //  51: 0000 111x
	{0, 43}, {0, 23}, //  52: 0001 000x
	{0, 51}, {0, 15}, //  53: 0001 001x
	{0, 42}, {0, 22}, //  54: 0001 010x
	{0, 50}, {0, 14}, //  55: 0001 011x
	{0, 41}, {0, 21}, //  56: 0001 100x
	{0, 49}, {0, 13}, //  57: 0001 101x
	{0, 35}, {0, 19}, //  58: 0001 110x
	{0, 11}, {0, 7}, //  59: 0001 111x
	{0, 39}, {0, 27}, //  60: 0000 0001x
	{0, 59}, {0, 55}, //  61: 0000 0010x
	{0, 47}, {0, 31}, //  62: 0000 0011x
}

var videoMotion = []vlc{
	{1 << 1, 0}, {0, 0}, //   0: x
	{2 << 1, 0}, {3 << 1, 0}, //   1: 0x
	{4 << 1, 0}, {5 << 1, 0}, //   2: 00x
	{0, 1}, {0, -1}, //   3: 01x
	{6 << 1, 0}, {7 << 1, 0}, //   4: 000x
	{0, 2}, {0, -2}, //   5: 001x
	{8 << 1, 0}, {9 << 1, 0}, //   6: 0000x
	{0, 3}, {0, -3}, //   7: 0001x
	{10 << 1, 0}, {11 << 1, 0}, //   8: 0000 0x
	{12 << 1, 0}, {13 << 1, 0}, //   9: 0000 1x
	{-1, 0}, {14 << 1, 0}, //  10: 0000 00x
	{15 << 1, 0}, {16 << 1, 0}, //  11: 0000 01x
	{17 << 1, 0}, {18 << 1, 0}, //  12: 0000 10x
	{0, 4}, {0, -4}, //  13: 0000 11x
	{-1, 0}, {19 << 1, 0}, //  14: 0000 001x
	{20 << 1, 0}, {21 << 1, 0}, //  15: 0000 010x
	{0, 7}, {0, -7}, //  16: 0000 011x
	{0, 6}, {0, -6}, //  17: 0000 100x
	{0, 5}, {0, -5}, //  18: 0000 101x
	{22 << 1, 0}, {23 << 1, 0}, //  19: 0000 0011x
	{24 << 1, 0}, {25 << 1, 0}, //  20: 0000 0100x
	{26 << 1, 0}, {27 << 1, 0}, //  21: 0000 0101x
	{28 << 1, 0}, {29 << 1, 0}, //  22: 0000 0011 0x
	{30 << 1, 0}, {31 << 1, 0}, //  23: 0000 0011 1x
	{32 << 1, 0}, {33 << 1, 0}, //  24: 0000 0100 0x
	{0, 10}, {0, -10}, //  25: 0000 0100 1x
	{0, 9}, {0, -9}, //  26: 0000 0101 0x
	{0, 8}, {0, -8}, //  27: 0000 0101 1x
	{0, 16}, {0, -16}, //  28: 0000 0011 00x
	{0, 15}, {0, -15}, //  29: 0000 0011 01x
	{0, 14}, {0, -14}, //  30: 0000 0011 10x
	{0, 13}, {0, -13}, //  31: 0000 0011 11x
	{0, 12}, {0, -12}, //  32: 0000 0100 00x
	{0, 11}, {0, -11}, //  33: 0000 0100 01x
}

var videoDctSizeLuminance = []vlc{
	{1 << 1, 0}, {2 << 1, 0}, //   0: x
	{0, 1}, {0, 2}, //   1: 0x
	{3 << 1, 0}, {4 << 1, 0}, //   2: 1x
	{0, 0}, {0, 3}, //   3: 10x
	{0, 4}, {5 << 1, 0}, //   4: 11x
	{0, 5}, {6 << 1, 0}, //   5: 111x
	{0, 6}, {7 << 1, 0}, //   6: 1111x
	{0, 7}, {8 << 1, 0}, //   7: 1111 1x
	{0, 8}, {-1, 0}, //   8: 1111 11x
}

var videoDctSizeChrominance = []vlc{
	{1 << 1, 0}, {2 << 1, 0}, //   0: x
	{0, 0}, {0, 1}, //   1: 0x
	{0, 2}, {3 << 1, 0}, //   2: 1x
	{0, 3}, {4 << 1, 0}, //   3: 11x
	{0, 4}, {5 << 1, 0}, //   4: 111x
	{0, 5}, {6 << 1, 0}, //   5: 1111x
	{0, 6}, {7 << 1, 0}, //   6: 1111 1x
	{0, 7}, {8 << 1, 0}, //   7: 1111 11x
	{0, 8}, {-1, 0}, //   8: 1111 111x
}

var videoDctSize = [][]vlc{
	videoDctSizeLuminance,
	videoDctSizeChrominance,
	videoDctSizeChrominance,
}

// dct_coeff bitmap:
//
//	0xff00  run
//	0x00ff  level
//
// Decoded values are unsigned. Sign bit follows in the stream.
var videoDctCoeff = []vlcUint{
	{1 << 1, 0}, {0, 0x0001}, //   0: x
	{2 << 1, 0}, {3 << 1, 0}, //   1: 0x
	{4 << 1, 0}, {5 << 1, 0}, //   2: 00x
	{6 << 1, 0}, {0, 0x0101}, //   3: 01x
	{7 << 1, 0}, {8 << 1, 0}, //   4: 000x
	{9 << 1, 0}, {10 << 1, 0}, //   5: 001x
	{0, 0x0002}, {0, 0x0201}, //   6: 010x
	{11 << 1, 0}, {12 << 1, 0}, //   7: 0000x
	{13 << 1, 0}, {14 << 1, 0}, //   8: 0001x
	{15 << 1, 0}, {0, 0x0003}, //   9: 0010x
	{0, 0x0401}, {0, 0x0301}, //  10: 0011x
	{16 << 1, 0}, {0, 0xffff}, //  11: 0000 0x
	{17 << 1, 0}, {18 << 1, 0}, //  12: 0000 1x
	{0, 0x0701}, {0, 0x0601}, //  13: 0001 0x
	{0, 0x0102}, {0, 0x0501}, //  14: 0001 1x
	{19 << 1, 0}, {20 << 1, 0}, //  15: 0010 0x
	{21 << 1, 0}, {22 << 1, 0}, //  16: 0000 00x
	{0, 0x0202}, {0, 0x0901}, //  17: 0000 10x
	{0, 0x0004}, {0, 0x0801}, //  18: 0000 11x
	{23 << 1, 0}, {24 << 1, 0}, //  19: 0010 00x
	{25 << 1, 0}, {26 << 1, 0}, //  20: 0010 01x
	{27 << 1, 0}, {28 << 1, 0}, //  21: 0000 000x
	{29 << 1, 0}, {30 << 1, 0}, //  22: 0000 001x
	{0, 0x0d01}, {0, 0x0006}, //  23: 0010 000x
	{0, 0x0c01}, {0, 0x0b01}, //  24: 0010 001x
	{0, 0x0302}, {0, 0x0103}, //  25: 0010 010x
	{0, 0x0005}, {0, 0x0a01}, //  26: 0010 011x
	{31 << 1, 0}, {32 << 1, 0}, //  27: 0000 0000x
	{33 << 1, 0}, {34 << 1, 0}, //  28: 0000 0001x
	{35 << 1, 0}, {36 << 1, 0}, //  29: 0000 0010x
	{37 << 1, 0}, {38 << 1, 0}, //  30: 0000 0011x
	{39 << 1, 0}, {40 << 1, 0}, //  31: 0000 0000 0x
	{41 << 1, 0}, {42 << 1, 0}, //  32: 0000 0000 1x
	{43 << 1, 0}, {44 << 1, 0}, //  33: 0000 0001 0x
	{45 << 1, 0}, {46 << 1, 0}, //  34: 0000 0001 1x
	{0, 0x1001}, {0, 0x0502}, //  35: 0000 0010 0x
	{0, 0x0007}, {0, 0x0203}, //  36: 0000 0010 1x
	{0, 0x0104}, {0, 0x0f01}, //  37: 0000 0011 0x
	{0, 0x0e01}, {0, 0x0402}, //  38: 0000 0011 1x
	{47 << 1, 0}, {48 << 1, 0}, //  39: 0000 0000 00x
	{49 << 1, 0}, {50 << 1, 0}, //  40: 0000 0000 01x
	{51 << 1, 0}, {52 << 1, 0}, //  41: 0000 0000 10x
	{53 << 1, 0}, {54 << 1, 0}, //  42: 0000 0000 11x
	{55 << 1, 0}, {56 << 1, 0}, //  43: 0000 0001 00x
	{57 << 1, 0}, {58 << 1, 0}, //  44: 0000 0001 01x
	{59 << 1, 0}, {60 << 1, 0}, //  45: 0000 0001 10x
	{61 << 1, 0}, {62 << 1, 0}, //  46: 0000 0001 11x
	{-1, 0}, {63 << 1, 0}, //  47: 0000 0000 000x
	{64 << 1, 0}, {65 << 1, 0}, //  48: 0000 0000 001x
	{66 << 1, 0}, {67 << 1, 0}, //  49: 0000 0000 010x
	{68 << 1, 0}, {69 << 1, 0}, //  50: 0000 0000 011x
	{70 << 1, 0}, {71 << 1, 0}, //  51: 0000 0000 100x
	{72 << 1, 0}, {73 << 1, 0}, //  52: 0000 0000 101x
	{74 << 1, 0}, {75 << 1, 0}, //  53: 0000 0000 110x
	{76 << 1, 0}, {77 << 1, 0}, //  54: 0000 0000 111x
	{0, 0x000b}, {0, 0x0802}, //  55: 0000 0001 000x
	{0, 0x0403}, {0, 0x000a}, //  56: 0000 0001 001x
	{0, 0x0204}, {0, 0x0702}, //  57: 0000 0001 010x
	{0, 0x1501}, {0, 0x1401}, //  58: 0000 0001 011x
	{0, 0x0009}, {0, 0x1301}, //  59: 0000 0001 100x
	{0, 0x1201}, {0, 0x0105}, //  60: 0000 0001 101x
	{0, 0x0303}, {0, 0x0008}, //  61: 0000 0001 110x
	{0, 0x0602}, {0, 0x1101}, //  62: 0000 0001 111x
	{78 << 1, 0}, {79 << 1, 0}, //  63: 0000 0000 0001x
	{80 << 1, 0}, {81 << 1, 0}, //  64: 0000 0000 0010x
	{82 << 1, 0}, {83 << 1, 0}, //  65: 0000 0000 0011x
	{84 << 1, 0}, {85 << 1, 0}, //  66: 0000 0000 0100x
	{86 << 1, 0}, {87 << 1, 0}, //  67: 0000 0000 0101x
	{88 << 1, 0}, {89 << 1, 0}, //  68: 0000 0000 0110x
	{90 << 1, 0}, {91 << 1, 0}, //  69: 0000 0000 0111x
	{0, 0x0a02}, {0, 0x0902}, //  70: 0000 0000 1000x
	{0, 0x0503}, {0, 0x0304}, //  71: 0000 0000 1001x
	{0, 0x0205}, {0, 0x0107}, //  72: 0000 0000 1010x
	{0, 0x0106}, {0, 0x000f}, //  73: 0000 0000 1011x
	{0, 0x000e}, {0, 0x000d}, //  74: 0000 0000 1100x
	{0, 0x000c}, {0, 0x1a01}, //  75: 0000 0000 1101x
	{0, 0x1901}, {0, 0x1801}, //  76: 0000 0000 1110x
	{0, 0x1701}, {0, 0x1601}, //  77: 0000 0000 1111x
	{92 << 1, 0}, {93 << 1, 0}, //  78: 0000 0000 0001 0x
	{94 << 1, 0}, {95 << 1, 0}, //  79: 0000 0000 0001 1x
	{96 << 1, 0}, {97 << 1, 0}, //  80: 0000 0000 0010 0x
	{98 << 1, 0}, {99 << 1, 0}, //  81: 0000 0000 0010 1x
	{100 << 1, 0}, {101 << 1, 0}, //  82: 0000 0000 0011 0x
	{102 << 1, 0}, {103 << 1, 0}, //  83: 0000 0000 0011 1x
	{0, 0x001f}, {0, 0x001e}, //  84: 0000 0000 0100 0x
	{0, 0x001d}, {0, 0x001c}, //  85: 0000 0000 0100 1x
	{0, 0x001b}, {0, 0x001a}, //  86: 0000 0000 0101 0x
	{0, 0x0019}, {0, 0x0018}, //  87: 0000 0000 0101 1x
	{0, 0x0017}, {0, 0x0016}, //  88: 0000 0000 0110 0x
	{0, 0x0015}, {0, 0x0014}, //  89: 0000 0000 0110 1x
	{0, 0x0013}, {0, 0x0012}, //  90: 0000 0000 0111 0x
	{0, 0x0011}, {0, 0x0010}, //  91: 0000 0000 0111 1x
	{104 << 1, 0}, {105 << 1, 0}, //  92: 0000 0000 0001 00x
	{106 << 1, 0}, {107 << 1, 0}, //  93: 0000 0000 0001 01x
	{108 << 1, 0}, {109 << 1, 0}, //  94: 0000 0000 0001 10x
	{110 << 1, 0}, {111 << 1, 0}, //  95: 0000 0000 0001 11x
	{0, 0x0028}, {0, 0x0027}, //  96: 0000 0000 0010 00x
	{0, 0x0026}, {0, 0x0025}, //  97: 0000 0000 0010 01x
	{0, 0x0024}, {0, 0x0023}, //  98: 0000 0000 0010 10x
	{0, 0x0022}, {0, 0x0021}, //  99: 0000 0000 0010 11x
	{0, 0x0020}, {0, 0x010e}, // 100: 0000 0000 0011 00x
	{0, 0x010d}, {0, 0x010c}, // 101: 0000 0000 0011 01x
	{0, 0x010b}, {0, 0x010a}, // 102: 0000 0000 0011 10x
	{0, 0x0109}, {0, 0x0108}, // 103: 0000 0000 0011 11x
	{0, 0x0112}, {0, 0x0111}, // 104: 0000 0000 0001 000x
	{0, 0x0110}, {0, 0x010f}, // 105: 0000 0000 0001 001x
	{0, 0x0603}, {0, 0x1002}, // 106: 0000 0000 0001 010x
	{0, 0x0f02}, {0, 0x0e02}, // 107: 0000 0000 0001 011x
	{0, 0x0d02}, {0, 0x0c02}, // 108: 0000 0000 0001 100x
	{0, 0x0b02}, {0, 0x1f01}, // 109: 0000 0000 0001 101x
	{0, 0x1e01}, {0, 0x1d01}, // 110: 0000 0000 0001 110x
	{0, 0x1c01}, {0, 0x1b01}, // 111: 0000 0000 0001 111x
}

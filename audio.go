package mpeg

import (
	"bytes"
	"io"
	"unsafe"
)

const (
	// SamplesPerFrame is the default count of samples.
	SamplesPerFrame = 1152
)

type AudioFormat int

const (
	// AudioF32N - 32-bit floating point samples, normalized
	AudioF32N AudioFormat = iota
	// AudioF32NLR - 32-bit floating point samples, normalized, separate channels
	AudioF32NLR
	// AudioF32 - 32-bit floating point samples
	AudioF32
	// AudioS16 - signed 16-bit samples
	AudioS16
)

// Samples represents decoded audio samples, stored as normalized (-1, 1) float32,
// interleaved and in separate channels.
type Samples struct {
	Time        float64
	S16         []int16
	F32         []float32
	Left        []float32
	Right       []float32
	Interleaved []float32

	format AudioFormat
}

// Bytes returns interleaved samples as slice of bytes.
func (s *Samples) Bytes() []byte {
	switch s.format {
	case AudioF32N:
		return unsafe.Slice((*byte)(unsafe.Pointer(&s.Interleaved[0])), len(s.Interleaved)*4)
	case AudioF32:
		return unsafe.Slice((*byte)(unsafe.Pointer(&s.F32[0])), len(s.F32)*4)
	case AudioS16:
		return unsafe.Slice((*byte)(unsafe.Pointer(&s.S16[0])), len(s.S16)*2)
	}

	return nil
}

type SamplesReader struct {
	reader *bytes.Reader
}

// Read implements the io.Reader interface.
func (s *SamplesReader) Read(b []byte) (int, error) {
	if s.reader.Len() == 0 {
		_, err := s.reader.Seek(0, io.SeekStart)
		if err != nil {
			return 0, err
		}
	}

	return s.reader.Read(b)
}

// Seek implements the io.Seeker interface.
func (s *SamplesReader) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

// Audio decodes MPEG-1 Audio Layer II (mp2) data into raw samples.
type Audio struct {
	time              float64
	samplesDecoded    int
	samplerateIndex   int
	bitrateIndex      int
	version           int
	layer             int
	mode              int
	channels          int
	bound             int
	vPos              int
	nextFrameDataSize int
	hasHeader         bool

	buf *Buffer

	allocation      [2][32]*quantizerSpec
	scaleFactorInfo [2][32]byte
	scaleFactor     [2][32][3]int
	sample          [2][32][3]int

	samples Samples
	format  AudioFormat

	d []float32
	v [][]float32
	u []float32
}

// NewAudio creates an audio decoder with buffer as a source.
func NewAudio(buf *Buffer) *Audio {
	audio := &Audio{}

	audio.buf = buf
	audio.samplerateIndex = 3 // Indicates 0

	audio.samples.S16 = make([]int16, SamplesPerFrame*2)
	audio.samples.F32 = make([]float32, SamplesPerFrame*2)
	audio.samples.Left = make([]float32, SamplesPerFrame)
	audio.samples.Right = make([]float32, SamplesPerFrame)
	audio.samples.Interleaved = make([]float32, SamplesPerFrame*2)

	audio.d = make([]float32, 1024)
	for i, d := range synthesisWindow {
		audio.d[i] = d
		audio.d[i+512] = d
	}

	audio.v = make([][]float32, 2)
	for i := range audio.v {
		audio.v[i] = make([]float32, 1024)
	}

	audio.u = make([]float32, 32)

	// Attempt to decode first header
	audio.nextFrameDataSize = audio.decodeHeader()

	return audio
}

// Reader returns samples reader.
func (a *Audio) Reader() io.Reader {
	switch a.format {
	case AudioF32N:
		b := unsafe.Slice((*byte)(unsafe.Pointer(&a.samples.Interleaved[0])), len(a.samples.Interleaved)*4)
		return &SamplesReader{bytes.NewReader(b)}
	case AudioF32:
		b := unsafe.Slice((*byte)(unsafe.Pointer(&a.samples.F32[0])), len(a.samples.F32)*4)
		return &SamplesReader{bytes.NewReader(b)}
	case AudioS16:
		b := unsafe.Slice((*byte)(unsafe.Pointer(&a.samples.S16[0])), len(a.samples.S16)*2)
		return &SamplesReader{bytes.NewReader(b)}
	}

	return nil
}

// Buffer returns audio buffer.
func (a *Audio) Buffer() *Buffer {
	return a.buf
}

// HasHeader checks whether a frame header was found, and we can accurately report on samplerate.
func (a *Audio) HasHeader() bool {
	if a.hasHeader {
		return true
	}

	a.nextFrameDataSize = a.decodeHeader()

	return a.hasHeader
}

// Samplerate returns the sample rate in samples per second.
func (a *Audio) Samplerate() int {
	if a.HasHeader() {
		return int(samplerate[a.samplerateIndex])
	}

	return 0
}

// Channels returns the number of channels.
func (a *Audio) Channels() int {
	return a.channels
}

// Time returns the current internal time in seconds.
func (a *Audio) Time() float64 {
	return a.time
}

// SetTime sets the current internal time in seconds. This is only useful when you
// manipulate the underlying video buffer and want to enforce a correct timestamps.
func (a *Audio) SetTime(time float64) {
	a.samplesDecoded = int(time * float64(samplerate[a.samplerateIndex]))
	a.time = time
}

// Rewind rewinds the internal buffer.
func (a *Audio) Rewind() {
	a.buf.Rewind()
	a.time = 0
	a.samplesDecoded = 0
	a.nextFrameDataSize = 0
}

// HasEnded checks whether the file has ended. This will be cleared on rewind.
func (a *Audio) HasEnded() bool {
	return a.buf.HasEnded()
}

// Decode decodes and returns one "frame" of audio and advance the
// internal time by (SamplesPerFrame/samplerate) seconds.
func (a *Audio) Decode() *Samples {
	// Do we have at least enough information to decode the frame header?
	if a.nextFrameDataSize == 0 {
		a.nextFrameDataSize = a.decodeHeader()
	}

	if a.nextFrameDataSize == 0 || !a.buf.has(a.nextFrameDataSize<<3) {
		return nil
	}

	a.decodeFrame()
	a.nextFrameDataSize = 0

	a.samples.Time = a.time

	a.samplesDecoded += SamplesPerFrame
	a.time = float64(a.samplesDecoded) / float64(samplerate[a.samplerateIndex])

	return &a.samples
}

func (a *Audio) decodeHeader() int {
	if !a.buf.has(48) {
		return 0
	}

	a.buf.skipBytes(0x00)
	sync := a.buf.read(11)

	// Attempt to resync if no syncword was found. This sucks balls. The MP2
	// stream contains a syncword just before every frame (11 bits set to 1).
	// However, this syncword is not guaranteed to not occur elsewhere in the
	// stream. So, if we have to resync, we also have to check if the header
	// (samplerate, bitrate) differs from the one we had before. This all
	// may still lead to garbage data being decoded :/

	if sync != frameSync && !a.buf.findFrameSync() {
		return 0
	}

	a.version = a.buf.read(2)
	a.layer = a.buf.read(2)
	hasCRC := a.buf.read1() == 0

	if a.version != mpeg1 || a.layer != layerII {
		return 0
	}

	bitrateIndex := a.buf.read(4) - 1
	if bitrateIndex > 13 {
		return 0
	}

	samplerateIndex := a.buf.read(2)
	if samplerateIndex == 3 {
		return 0
	}

	padding := a.buf.read1()
	a.buf.skip(1) // f_private
	mode := a.buf.read(2)

	// If we already have a header, make sure the samplerate, bitrate and mode
	// are still the same, otherwise we might have missed sync.

	if a.hasHeader && (a.bitrateIndex != bitrateIndex || a.samplerateIndex != samplerateIndex || a.mode != mode) {
		return 0
	}

	a.bitrateIndex = bitrateIndex
	a.samplerateIndex = samplerateIndex
	a.mode = mode
	a.hasHeader = true

	if mode == modeStereo || mode == modeJointStereo {
		a.channels = 2
	} else if mode == modeMono {
		a.channels = 1
	}

	// Parse the mode_extension, set up the stereo bound
	if mode == modeJointStereo {
		a.bound = (a.buf.read(2) + 1) << 2
	} else {
		a.buf.skip(2)
		if mode == modeMono {
			a.bound = 0
		} else {
			a.bound = 32
		}
	}

	// Discard the last 4 bits of the header and the CRC Value, if present
	a.buf.skip(4) // copyright(1), original(1), emphasis(2)
	if hasCRC {
		a.buf.skip(16)
	}

	// Compute frame size, check if we have enough data to decode the whole frame.
	br := bitrate[a.bitrateIndex]
	sr := samplerate[a.samplerateIndex]
	frameSize := (144000 * int(br) / int(sr)) + padding

	r := 4
	if hasCRC {
		r = 6
	}

	return frameSize - r
}

func (a *Audio) decodeFrame() {
	// Prepare the quantizer table lookups
	tab1 := 1
	if a.mode == modeMono {
		tab1 = 0
	}
	tab2 := int(quantLutStep1[tab1][a.bitrateIndex])
	tab3 := int(quantLutStep2[tab2][a.samplerateIndex])

	sblimit := tab3 & 63
	tab3 >>= 6

	if a.bound > sblimit {
		a.bound = sblimit
	}

	// read the allocation information
	for sb := 0; sb < a.bound; sb++ {
		a.allocation[0][sb] = a.readAllocation(sb, tab3)
		a.allocation[1][sb] = a.readAllocation(sb, tab3)
	}

	for sb := a.bound; sb < sblimit; sb++ {
		a.allocation[0][sb] = a.readAllocation(sb, tab3)
		a.allocation[1][sb] = a.allocation[0][sb]
	}

	// read scale factor selector information
	channels := 2
	if a.mode == modeMono {
		channels = 1
	}

	for sb := 0; sb < sblimit; sb++ {
		for ch := 0; ch < channels; ch++ {
			if a.allocation[ch][sb] != nil {
				a.scaleFactorInfo[ch][sb] = byte(a.buf.read(2))
			}
		}
		if a.mode == modeMono {
			a.scaleFactorInfo[1][sb] = a.scaleFactorInfo[0][sb]
		}
	}

	// read scale factors
	for sb := 0; sb < sblimit; sb++ {
		for ch := 0; ch < channels; ch++ {
			if a.allocation[ch][sb] != nil {
				switch a.scaleFactorInfo[ch][sb] {
				case 0:
					a.scaleFactor[ch][sb][0] = a.buf.read(6)
					a.scaleFactor[ch][sb][1] = a.buf.read(6)
					a.scaleFactor[ch][sb][2] = a.buf.read(6)
				case 1:
					tmp := a.buf.read(6)
					a.scaleFactor[ch][sb][0] = tmp
					a.scaleFactor[ch][sb][1] = tmp
					a.scaleFactor[ch][sb][2] = a.buf.read(6)
				case 2:
					tmp := a.buf.read(6)
					a.scaleFactor[ch][sb][0] = tmp
					a.scaleFactor[ch][sb][1] = tmp
					a.scaleFactor[ch][sb][2] = tmp
				case 3:
					a.scaleFactor[ch][sb][0] = a.buf.read(6)
					tmp := a.buf.read(6)
					a.scaleFactor[ch][sb][1] = tmp
					a.scaleFactor[ch][sb][2] = tmp
				}
			}
		}

		if a.mode == modeMono {
			a.scaleFactor[1][sb][0] = a.scaleFactor[0][sb][0]
			a.scaleFactor[1][sb][1] = a.scaleFactor[0][sb][1]
			a.scaleFactor[1][sb][2] = a.scaleFactor[0][sb][2]
		}
	}

	// Coefficient input and reconstruction
	outPos := 0
	for part := 0; part < 3; part++ {
		for granule := 0; granule < 4; granule++ {
			// Read the samples
			for sb := 0; sb < a.bound; sb++ {
				a.readSamples(0, sb, part)
				a.readSamples(1, sb, part)
			}
			for sb := a.bound; sb < sblimit; sb++ {
				a.readSamples(0, sb, part)
				a.sample[1][sb][0] = a.sample[0][sb][0]
				a.sample[1][sb][1] = a.sample[0][sb][1]
				a.sample[1][sb][2] = a.sample[0][sb][2]
			}
			for sb := sblimit; sb < 32; sb++ {
				a.sample[0][sb][0] = 0
				a.sample[0][sb][1] = 0
				a.sample[0][sb][2] = 0
				a.sample[1][sb][0] = 0
				a.sample[1][sb][1] = 0
				a.sample[1][sb][2] = 0
			}

			// Synthesis loop
			for p := 0; p < 3; p++ {
				// Shifting step
				a.vPos = (a.vPos - 64) & 1023

				for ch := 0; ch < 2; ch++ {
					a.idct36(a.sample[ch], p, a.v[ch], a.vPos)

					// Build U, windowing, calculate output
					for i := range a.u {
						a.u[i] = 0
					}

					dIndex := 512 - (a.vPos >> 1)
					vIndex := (a.vPos % 128) >> 1
					for vIndex < 1024 {
						for i := 0; i < 32; i++ {
							a.u[i] += a.d[dIndex] * a.v[ch][vIndex]
							dIndex++
							vIndex++
						}

						vIndex += 128 - 32
						dIndex += 64 - 32
					}

					dIndex -= 512 - 32
					vIndex = (128 - 32 + 1024) - vIndex
					for vIndex < 1024 {
						for i := 0; i < 32; i++ {
							a.u[i] += a.d[dIndex] * a.v[ch][vIndex]
							dIndex++
							vIndex++
						}

						vIndex += 128 - 32
						dIndex += 64 - 32
					}

					// Output samples
					var out []float32
					if ch == 0 {
						out = a.samples.Left
					} else {
						out = a.samples.Right
					}

					for j := 0; j < 32; j++ {
						s := a.u[j] / 2147418112.0

						switch a.format {
						case AudioF32N:
							a.samples.Interleaved[((outPos+j)<<1)+ch] = s
						case AudioF32NLR:
							out[outPos+j] = s
						case AudioS16:
							if s < 0 {
								a.samples.S16[((outPos+j)<<1)+ch] = int16(s * 0x8000)
							} else {
								a.samples.S16[((outPos+j)<<1)+ch] = int16(s * 0x7FFF)
							}
						case AudioF32:
							if s < 0 {
								a.samples.F32[((outPos+j)<<1)+ch] = s * 0x80000000
							} else {
								a.samples.F32[((outPos+j)<<1)+ch] = s * 0x7FFFFFFF
							}
						}
					}
				} // End of synthesis ch loop

				outPos += 32
			} // End of synthesis sub-block loop
		} // Decoding of the granule finished
	}

	a.buf.align()
}

func (a *Audio) readAllocation(sb, tab3 int) *quantizerSpec {
	tab4 := quantLutStep3[tab3][sb]
	qtab := quantLutStep4[tab4&15][a.buf.read(int(tab4)>>4)]

	if qtab != 0 {
		return &quantTab[qtab-1]
	}

	return nil
}

func (a *Audio) readSamples(ch, sb, part int) {
	q := a.allocation[ch][sb]
	sf := a.scaleFactor[ch][sb][part]
	val := 0

	if q == nil {
		// No bits allocated for this subband
		a.sample[ch][sb][0] = 0
		a.sample[ch][sb][1] = 0
		a.sample[ch][sb][2] = 0

		return
	}

	// Resolve scale factor
	if sf == 63 {
		sf = 0
	} else {
		shift := sf / 3
		sf = (scalefactorBase[sf%3] + ((1 << shift) >> 1)) >> shift
	}

	// Decode samples
	adj := int(q.Levels)
	if q.Group != 0 {
		// Decode grouped samples
		val = a.buf.read(int(q.Bits))
		a.sample[ch][sb][0] = val % adj
		val /= adj
		a.sample[ch][sb][1] = val % adj
		a.sample[ch][sb][2] = val / adj
	} else {
		// Decode direct samples
		a.sample[ch][sb][0] = a.buf.read(int(q.Bits))
		a.sample[ch][sb][1] = a.buf.read(int(q.Bits))
		a.sample[ch][sb][2] = a.buf.read(int(q.Bits))
	}

	// Postmultiply samples
	scale := 65536 / (adj + 1)
	adj = ((adj + 1) >> 1) - 1

	val = (adj - a.sample[ch][sb][0]) * scale
	a.sample[ch][sb][0] = (val*(sf>>12) + ((val*(sf&4095) + 2048) >> 12)) >> 12

	val = (adj - a.sample[ch][sb][1]) * scale
	a.sample[ch][sb][1] = (val*(sf>>12) + ((val*(sf&4095) + 2048) >> 12)) >> 12

	val = (adj - a.sample[ch][sb][2]) * scale
	a.sample[ch][sb][2] = (val*(sf>>12) + ((val*(sf&4095) + 2048) >> 12)) >> 12
}

func (a *Audio) idct36(s [32][3]int, ss int, d []float32, dp int) {
	var t01, t02, t03, t04, t05, t06, t07, t08, t09, t10, t11, t12,
		t13, t14, t15, t16, t17, t18, t19, t20, t21, t22, t23, t24,
		t25, t26, t27, t28, t29, t30, t31, t32, t33 float32

	t01 = float32(s[0][ss] + s[31][ss])
	t02 = float32(s[0][ss]-s[31][ss]) * 0.500602998235
	t03 = float32(s[1][ss] + s[30][ss])
	t04 = float32(s[1][ss]-s[30][ss]) * 0.505470959898
	t05 = float32(s[2][ss] + s[29][ss])
	t06 = float32(s[2][ss]-s[29][ss]) * 0.515447309923
	t07 = float32(s[3][ss] + s[28][ss])
	t08 = float32(s[3][ss]-s[28][ss]) * 0.53104259109
	t09 = float32(s[4][ss] + s[27][ss])
	t10 = float32(s[4][ss]-s[27][ss]) * 0.553103896034
	t11 = float32(s[5][ss] + s[26][ss])
	t12 = float32(s[5][ss]-s[26][ss]) * 0.582934968206
	t13 = float32(s[6][ss] + s[25][ss])
	t14 = float32(s[6][ss]-s[25][ss]) * 0.622504123036
	t15 = float32(s[7][ss] + s[24][ss])
	t16 = float32(s[7][ss]-s[24][ss]) * 0.674808341455
	t17 = float32(s[8][ss] + s[23][ss])
	t18 = float32(s[8][ss]-s[23][ss]) * 0.744536271002
	t19 = float32(s[9][ss] + s[22][ss])
	t20 = float32(s[9][ss]-s[22][ss]) * 0.839349645416
	t21 = float32(s[10][ss] + s[21][ss])
	t22 = float32(s[10][ss]-s[21][ss]) * 0.972568237862
	t23 = float32(s[11][ss] + s[20][ss])
	t24 = float32(s[11][ss]-s[20][ss]) * 1.16943993343
	t25 = float32(s[12][ss] + s[19][ss])
	t26 = float32(s[12][ss]-s[19][ss]) * 1.48416461631
	t27 = float32(s[13][ss] + s[18][ss])
	t28 = float32(s[13][ss]-s[18][ss]) * 2.05778100995
	t29 = float32(s[14][ss] + s[17][ss])
	t30 = float32(s[14][ss]-s[17][ss]) * 3.40760841847
	t31 = float32(s[15][ss] + s[16][ss])
	t32 = float32(s[15][ss]-s[16][ss]) * 10.1900081235

	t33 = t01 + t31
	t31 = (t01 - t31) * 0.502419286188
	t01 = t03 + t29
	t29 = (t03 - t29) * 0.52249861494
	t03 = t05 + t27
	t27 = (t05 - t27) * 0.566944034816
	t05 = t07 + t25
	t25 = (t07 - t25) * 0.64682178336
	t07 = t09 + t23
	t23 = (t09 - t23) * 0.788154623451
	t09 = t11 + t21
	t21 = (t11 - t21) * 1.06067768599
	t11 = t13 + t19
	t19 = (t13 - t19) * 1.72244709824
	t13 = t15 + t17
	t17 = (t15 - t17) * 5.10114861869
	t15 = t33 + t13
	t13 = (t33 - t13) * 0.509795579104
	t33 = t01 + t11
	t01 = (t01 - t11) * 0.601344886935
	t11 = t03 + t09
	t09 = (t03 - t09) * 0.899976223136
	t03 = t05 + t07
	t07 = (t05 - t07) * 2.56291544774
	t05 = t15 + t03
	t15 = (t15 - t03) * 0.541196100146
	t03 = t33 + t11
	t11 = (t33 - t11) * 1.30656296488
	t33 = t05 + t03
	t05 = (t05 - t03) * 0.707106781187
	t03 = t15 + t11
	t15 = (t15 - t11) * 0.707106781187
	t03 += t15
	t11 = t13 + t07
	t13 = (t13 - t07) * 0.541196100146
	t07 = t01 + t09
	t09 = (t01 - t09) * 1.30656296488
	t01 = t11 + t07
	t07 = (t11 - t07) * 0.707106781187
	t11 = t13 + t09
	t13 = (t13 - t09) * 0.707106781187
	t11 += t13
	t01 += t11
	t11 += t07
	t07 += t13
	t09 = t31 + t17
	t31 = (t31 - t17) * 0.509795579104
	t17 = t29 + t19
	t29 = (t29 - t19) * 0.601344886935
	t19 = t27 + t21
	t21 = (t27 - t21) * 0.899976223136
	t27 = t25 + t23
	t23 = (t25 - t23) * 2.56291544774
	t25 = t09 + t27
	t09 = (t09 - t27) * 0.541196100146
	t27 = t17 + t19
	t19 = (t17 - t19) * 1.30656296488
	t17 = t25 + t27
	t27 = (t25 - t27) * 0.707106781187
	t25 = t09 + t19
	t19 = (t09 - t19) * 0.707106781187
	t25 += t19
	t09 = t31 + t23
	t31 = (t31 - t23) * 0.541196100146
	t23 = t29 + t21
	t21 = (t29 - t21) * 1.30656296488
	t29 = t09 + t23
	t23 = (t09 - t23) * 0.707106781187
	t09 = t31 + t21
	t31 = (t31 - t21) * 0.707106781187
	t09 += t31
	t29 += t09
	t09 += t23
	t23 += t31
	t17 += t29
	t29 += t25
	t25 += t09
	t09 += t27
	t27 += t23
	t23 += t19
	t19 += t31
	t21 = t02 + t32
	t02 = (t02 - t32) * 0.502419286188
	t32 = t04 + t30
	t04 = (t04 - t30) * 0.52249861494
	t30 = t06 + t28
	t28 = (t06 - t28) * 0.566944034816
	t06 = t08 + t26
	t08 = (t08 - t26) * 0.64682178336
	t26 = t10 + t24
	t10 = (t10 - t24) * 0.788154623451
	t24 = t12 + t22
	t22 = (t12 - t22) * 1.06067768599
	t12 = t14 + t20
	t20 = (t14 - t20) * 1.72244709824
	t14 = t16 + t18
	t16 = (t16 - t18) * 5.10114861869
	t18 = t21 + t14
	t14 = (t21 - t14) * 0.509795579104
	t21 = t32 + t12
	t32 = (t32 - t12) * 0.601344886935
	t12 = t30 + t24
	t24 = (t30 - t24) * 0.899976223136
	t30 = t06 + t26
	t26 = (t06 - t26) * 2.56291544774
	t06 = t18 + t30
	t18 = (t18 - t30) * 0.541196100146
	t30 = t21 + t12
	t12 = (t21 - t12) * 1.30656296488
	t21 = t06 + t30
	t30 = (t06 - t30) * 0.707106781187
	t06 = t18 + t12
	t12 = (t18 - t12) * 0.707106781187
	t06 += t12
	t18 = t14 + t26
	t26 = (t14 - t26) * 0.541196100146
	t14 = t32 + t24
	t24 = (t32 - t24) * 1.30656296488
	t32 = t18 + t14
	t14 = (t18 - t14) * 0.707106781187
	t18 = t26 + t24
	t24 = (t26 - t24) * 0.707106781187
	t18 += t24
	t32 += t18
	t18 += t14
	t26 = t14 + t24
	t14 = t02 + t16
	t02 = (t02 - t16) * 0.509795579104
	t16 = t04 + t20
	t04 = (t04 - t20) * 0.601344886935
	t20 = t28 + t22
	t22 = (t28 - t22) * 0.899976223136
	t28 = t08 + t10
	t10 = (t08 - t10) * 2.56291544774
	t08 = t14 + t28
	t14 = (t14 - t28) * 0.541196100146
	t28 = t16 + t20
	t20 = (t16 - t20) * 1.30656296488
	t16 = t08 + t28
	t28 = (t08 - t28) * 0.707106781187
	t08 = t14 + t20
	t20 = (t14 - t20) * 0.707106781187
	t08 += t20
	t14 = t02 + t10
	t02 = (t02 - t10) * 0.541196100146
	t10 = t04 + t22
	t22 = (t04 - t22) * 1.30656296488
	t04 = t14 + t10
	t10 = (t14 - t10) * 0.707106781187
	t14 = t02 + t22
	t02 = (t02 - t22) * 0.707106781187
	t14 += t02
	t04 += t14
	t14 += t10
	t10 += t02
	t16 += t04
	t04 += t08
	t08 += t14
	t14 += t28
	t28 += t10
	t10 += t20
	t20 += t02
	t21 += t16
	t16 += t32
	t32 += t04
	t04 += t06
	t06 += t08
	t08 += t18
	t18 += t14
	t14 += t30
	t30 += t28
	t28 += t26
	t26 += t10
	t10 += t12
	t12 += t20
	t20 += t24
	t24 += t02

	d[dp+48] = -t33
	d[dp+49] = -t21
	d[dp+47] = -t21
	d[dp+50] = -t17
	d[dp+46] = -t17
	d[dp+51] = -t16
	d[dp+45] = -t16
	d[dp+52] = -t01
	d[dp+44] = -t01
	d[dp+53] = -t32
	d[dp+43] = -t32
	d[dp+54] = -t29
	d[dp+42] = -t29
	d[dp+55] = -t04
	d[dp+41] = -t04
	d[dp+56] = -t03
	d[dp+40] = -t03
	d[dp+57] = -t06
	d[dp+39] = -t06
	d[dp+58] = -t25
	d[dp+38] = -t25
	d[dp+59] = -t08
	d[dp+37] = -t08
	d[dp+60] = -t11
	d[dp+36] = -t11
	d[dp+61] = -t18
	d[dp+35] = -t18
	d[dp+62] = -t09
	d[dp+34] = -t09
	d[dp+63] = -t14
	d[dp+33] = -t14
	d[dp+32] = -t05
	d[dp+0] = t05
	d[dp+31] = -t30
	d[dp+1] = t30
	d[dp+30] = -t27
	d[dp+2] = t27
	d[dp+29] = -t28
	d[dp+3] = t28
	d[dp+28] = -t07
	d[dp+4] = t07
	d[dp+27] = -t26
	d[dp+5] = t26
	d[dp+26] = -t23
	d[dp+6] = t23
	d[dp+25] = -t10
	d[dp+7] = t10
	d[dp+24] = -t15
	d[dp+8] = t15
	d[dp+23] = -t12
	d[dp+9] = t12
	d[dp+22] = -t19
	d[dp+10] = t19
	d[dp+21] = -t20
	d[dp+11] = t20
	d[dp+20] = -t13
	d[dp+12] = t13
	d[dp+19] = -t24
	d[dp+13] = t24
	d[dp+18] = -t31
	d[dp+14] = t31
	d[dp+17] = -t02
	d[dp+15] = t02
	d[dp+16] = 0.0
}

const (
	frameSync = 0x7ff

	mpeg25 = 0x0
	mpeg2  = 0x2
	mpeg1  = 0x3

	layerIII = 0x1
	layerII  = 0x2
	layerI   = 0x3

	modeStereo      = 0x0
	modeJointStereo = 0x1
	modeDualChannel = 0x2
	modeMono        = 0x3
)

// quantizerSpec .
type quantizerSpec struct {
	Levels uint16
	Group  uint8
	Bits   uint8
}

var samplerate = []uint16{
	44100, 48000, 32000, 0, // MPEG-1
	22050, 24000, 16000, 0, // MPEG-2
}

var bitrate = []int16{
	32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, // MPEG-1
	8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, // MPEG-2
}

var scalefactorBase = []int{
	0x02000000, 0x01965FEA, 0x01428A30,
}

var synthesisWindow = []float32{
	0.0, -0.5, -0.5, -0.5, -0.5, -0.5,
	-0.5, -1.0, -1.0, -1.0, -1.0, -1.5,
	-1.5, -2.0, -2.0, -2.5, -2.5, -3.0,
	-3.5, -3.5, -4.0, -4.5, -5.0, -5.5,
	-6.5, -7.0, -8.0, -8.5, -9.5, -10.5,
	-12.0, -13.0, -14.5, -15.5, -17.5, -19.0,
	-20.5, -22.5, -24.5, -26.5, -29.0, -31.5,
	-34.0, -36.5, -39.5, -42.5, -45.5, -48.5,
	-52.0, -55.5, -58.5, -62.5, -66.0, -69.5,
	-73.5, -77.0, -80.5, -84.5, -88.0, -91.5,
	-95.0, -98.0, -101.0, -104.0, 106.5, 109.0,
	111.0, 112.5, 113.5, 114.0, 114.0, 113.5,
	112.0, 110.5, 107.5, 104.0, 100.0, 94.5,
	88.5, 81.5, 73.0, 63.5, 53.0, 41.5,
	28.5, 14.5, -1.0, -18.0, -36.0, -55.5,
	-76.5, -98.5, -122.0, -147.0, -173.5, -200.5,
	-229.5, -259.5, -290.5, -322.5, -355.5, -389.5,
	-424.0, -459.5, -495.5, -532.0, -568.5, -605.0,
	-641.5, -678.0, -714.0, -749.0, -783.5, -817.0,
	-849.0, -879.5, -908.5, -935.0, -959.5, -981.0,
	-1000.5, -1016.0, -1028.5, -1037.5, -1042.5, -1043.5,
	-1040.0, -1031.5, 1018.5, 1000.0, 976.0, 946.5,
	911.0, 869.5, 822.0, 767.5, 707.0, 640.0,
	565.5, 485.0, 397.0, 302.5, 201.0, 92.5,
	-22.5, -144.0, -272.5, -407.0, -547.5, -694.0,
	-846.0, -1003.0, -1165.0, -1331.5, -1502.0, -1675.5,
	-1852.5, -2031.5, -2212.5, -2394.0, -2576.5, -2758.5,
	-2939.5, -3118.5, -3294.5, -3467.5, -3635.5, -3798.5,
	-3955.0, -4104.5, -4245.5, -4377.5, -4499.0, -4609.5,
	-4708.0, -4792.5, -4863.5, -4919.0, -4958.0, -4979.5,
	-4983.0, -4967.5, -4931.5, -4875.0, -4796.0, -4694.5,
	-4569.5, -4420.0, -4246.0, -4046.0, -3820.0, -3567.0,
	3287.0, 2979.5, 2644.0, 2280.5, 1888.0, 1467.5,
	1018.5, 541.0, 35.0, -499.0, -1061.0, -1650.0,
	-2266.5, -2909.0, -3577.0, -4270.0, -4987.5, -5727.5,
	-6490.0, -7274.0, -8077.5, -8899.5, -9739.0, -10594.5,
	-11464.5, -12347.0, -13241.0, -14144.5, -15056.0, -15973.5,
	-16895.5, -17820.0, -18744.5, -19668.0, -20588.0, -21503.0,
	-22410.5, -23308.5, -24195.0, -25068.5, -25926.5, -26767.0,
	-27589.0, -28389.0, -29166.5, -29919.0, -30644.5, -31342.0,
	-32009.5, -32645.0, -33247.0, -33814.5, -34346.0, -34839.5,
	-35295.0, -35710.0, -36084.5, -36417.5, -36707.5, -36954.0,
	-37156.5, -37315.0, -37428.0, -37496.0, 37519.0, 37496.0,
	37428.0, 37315.0, 37156.5, 36954.0, 36707.5, 36417.5,
	36084.5, 35710.0, 35295.0, 34839.5, 34346.0, 33814.5,
	33247.0, 32645.0, 32009.5, 31342.0, 30644.5, 29919.0,
	29166.5, 28389.0, 27589.0, 26767.0, 25926.5, 25068.5,
	24195.0, 23308.5, 22410.5, 21503.0, 20588.0, 19668.0,
	18744.5, 17820.0, 16895.5, 15973.5, 15056.0, 14144.5,
	13241.0, 12347.0, 11464.5, 10594.5, 9739.0, 8899.5,
	8077.5, 7274.0, 6490.0, 5727.5, 4987.5, 4270.0,
	3577.0, 2909.0, 2266.5, 1650.0, 1061.0, 499.0,
	-35.0, -541.0, -1018.5, -1467.5, -1888.0, -2280.5,
	-2644.0, -2979.5, 3287.0, 3567.0, 3820.0, 4046.0,
	4246.0, 4420.0, 4569.5, 4694.5, 4796.0, 4875.0,
	4931.5, 4967.5, 4983.0, 4979.5, 4958.0, 4919.0,
	4863.5, 4792.5, 4708.0, 4609.5, 4499.0, 4377.5,
	4245.5, 4104.5, 3955.0, 3798.5, 3635.5, 3467.5,
	3294.5, 3118.5, 2939.5, 2758.5, 2576.5, 2394.0,
	2212.5, 2031.5, 1852.5, 1675.5, 1502.0, 1331.5,
	1165.0, 1003.0, 846.0, 694.0, 547.5, 407.0,
	272.5, 144.0, 22.5, -92.5, -201.0, -302.5,
	-397.0, -485.0, -565.5, -640.0, -707.0, -767.5,
	-822.0, -869.5, -911.0, -946.5, -976.0, -1000.0,
	1018.5, 1031.5, 1040.0, 1043.5, 1042.5, 1037.5,
	1028.5, 1016.0, 1000.5, 981.0, 959.5, 935.0,
	908.5, 879.5, 849.0, 817.0, 783.5, 749.0,
	714.0, 678.0, 641.5, 605.0, 568.5, 532.0,
	495.5, 459.5, 424.0, 389.5, 355.5, 322.5,
	290.5, 259.5, 229.5, 200.5, 173.5, 147.0,
	122.0, 98.5, 76.5, 55.5, 36.0, 18.0,
	1.0, -14.5, -28.5, -41.5, -53.0, -63.5,
	-73.0, -81.5, -88.5, -94.5, -100.0, -104.0,
	-107.5, -110.5, -112.0, -113.5, -114.0, -114.0,
	-113.5, -112.5, -111.0, -109.0, 106.5, 104.0,
	101.0, 98.0, 95.0, 91.5, 88.0, 84.5,
	80.5, 77.0, 73.5, 69.5, 66.0, 62.5,
	58.5, 55.5, 52.0, 48.5, 45.5, 42.5,
	39.5, 36.5, 34.0, 31.5, 29.0, 26.5,
	24.5, 22.5, 20.5, 19.0, 17.5, 15.5,
	14.5, 13.0, 12.0, 10.5, 9.5, 8.5,
	8.0, 7.0, 6.5, 5.5, 5.0, 4.5,
	4.0, 3.5, 3.5, 3.0, 2.5, 2.5,
	2.0, 2.0, 1.5, 1.5, 1.0, 1.0,
	1.0, 1.0, 0.5, 0.5, 0.5, 0.5,
	0.5, 0.5,
}

// Quantizer lookup, step 1: bitrate classes.
var quantLutStep1 = [][]byte{
	// 32, 48, 56, 64, 80, 96,112,128,160,192,224,256,320,384 <- bitrate
	{0, 0, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2}, // mono
	// 16, 24, 28, 32, 40, 48, 56, 64, 80, 96,112,128,160,192 <- bitrate / chan
	{0, 0, 0, 0, 0, 0, 1, 1, 1, 2, 2, 2, 2, 2}, // stereo
}

// Quantizer lookup, step 2: bitrate class, sample rate -> B2 table idx, sblimit.
var quantTabA = byte(27 | 64) // Table 3-B.2a: high-rate, sblimit = 27
var quantTabB = byte(30 | 64) // Table 3-B.2b: high-rate, sblimit = 30
var quantTabC = byte(8)       // Table 3-B.2c:  low-rate, sblimit =  8
var quantTabD = byte(12)      // Table 3-B.2d:  low-rate, sblimit = 12

var quantLutStep2 = [][]byte{
	// 44.1 kHz, 48 kHz, 32 kHz
	{quantTabC, quantTabC, quantTabD}, // 32 - 48 kbit/sec/ch
	{quantTabA, quantTabA, quantTabA}, // 56 - 80 kbit/sec/ch
	{quantTabB, quantTabA, quantTabB}, // 96+	 kbit/sec/ch
}

// Quantizer lookup, step 3: B2 table, subband -> nbal, row Index (upper 4 bits: nbal, lower 4 bits: row Index).
var quantLutStep3 = [][]byte{
	// Low-rate table (3-B.2c and 3-B.2d)
	{
		0x44, 0x44,
		0x34, 0x34, 0x34, 0x34, 0x34, 0x34, 0x34, 0x34, 0x34, 0x34,
	},
	// High-rate table (3-B.2a and 3-B.2b)
	{
		0x43, 0x43, 0x43,
		0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42, 0x42,
		0x31, 0x31, 0x31, 0x31, 0x31, 0x31, 0x31, 0x31, 0x31, 0x31, 0x31, 0x31,
		0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20,
	},
	// MPEG-2 LSR table (B.2 in ISO 13818-3)
	{
		0x45, 0x45, 0x45, 0x45,
		0x34, 0x34, 0x34, 0x34, 0x34, 0x34, 0x34,
		0x24, 0x24, 0x24, 0x24, 0x24, 0x24, 0x24, 0x24, 0x24, 0x24,
		0x24, 0x24, 0x24, 0x24, 0x24, 0x24, 0x24, 0x24, 0x24,
	},
}

// Quantizer lookup, step 4: table row, allocation[] Value -> quant table Index.
var quantLutStep4 = [][]byte{
	{0, 1, 2, 17},
	{0, 1, 2, 3, 4, 5, 6, 17},
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 17},
	{0, 1, 3, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17},
	{0, 1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
}

var quantTab = []quantizerSpec{
	{3, 1, 5},      //  1
	{5, 1, 7},      //  2
	{7, 0, 3},      //  3
	{9, 1, 10},     //  4
	{15, 0, 4},     //  5
	{31, 0, 5},     //  6
	{63, 0, 6},     //  7
	{127, 0, 7},    //  8
	{255, 0, 8},    //  9
	{511, 0, 9},    // 10
	{1023, 0, 10},  // 11
	{2047, 0, 11},  // 12
	{4095, 0, 12},  // 13
	{8191, 0, 13},  // 14
	{16383, 0, 14}, // 15
	{32767, 0, 15}, // 16
	{65535, 0, 16}, // 17
}

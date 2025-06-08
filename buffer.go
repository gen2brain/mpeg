package mpeg

import (
	"errors"
	"io"
)

var (
	// BufferSize is the default size for buffer.
	BufferSize = 128 * 1024
)

// LoadFunc callback function.
type LoadFunc func(buffer *Buffer)

// Buffer provides the data source for all other interfaces.
type Buffer struct {
	reader io.Reader
	bytes  []byte

	bitIndex  int
	totalSize int

	hasEnded    bool
	discardRead bool

	available    []byte
	loadCallback LoadFunc
}

// NewBuffer creates a buffer instance.
func NewBuffer(r io.Reader) (*Buffer, error) {
	buf := &Buffer{}

	if r != nil {
		seeker, ok := r.(io.Seeker)
		if ok {
			cur, err := seeker.Seek(0, io.SeekCurrent)
			if err != nil {
				return nil, err
			}
			off, err := seeker.Seek(0, io.SeekEnd)
			if err != nil {
				return nil, err
			}
			buf.totalSize = int(off)
			_, err = seeker.Seek(cur, io.SeekStart)
			if err != nil {
				return nil, err
			}
		}
	}

	buf.reader = r
	buf.bytes = make([]byte, 0, BufferSize)
	buf.available = make([]byte, BufferSize)

	buf.discardRead = true

	return buf, nil
}

// Bytes returns a slice holding the unread portion of the buffer.
func (b *Buffer) Bytes() []byte {
	return b.bytes
}

// Index returns byte index.
func (b *Buffer) Index() int {
	return b.bitIndex >> 3
}

// Seekable returns true if reader is seekable.
func (b *Buffer) Seekable() bool {
	return b.reader != nil && b.totalSize > 0
}

// Write appends the contents of p to the buffer.
func (b *Buffer) Write(p []byte) int {
	if b.discardRead {
		b.discardReadBytes()
	}

	b.bytes = append(b.bytes, p...)

	b.hasEnded = false

	return len(p)
}

// SignalEnd marks the current byte length as the end of this buffer and signal that no
// more data is expected to be written to it. This function should be called
// just after the last Write().
func (b *Buffer) SignalEnd() {
	b.totalSize = len(b.bytes)
}

// SetLoadCallback sets a callback that is called whenever the buffer needs more data.
func (b *Buffer) SetLoadCallback(callback LoadFunc) {
	b.loadCallback = callback
}

// Rewind the buffer back to the beginning. When loading from io.ReadSeeker,
// this also seeks to the beginning.
func (b *Buffer) Rewind() {
	b.seek(0)
}

// Size returns the total size. For io.ReadSeeker, this returns the total size. For all other
// types it returns the number of bytes currently in the buffer.
func (b *Buffer) Size() int {
	if b.totalSize > 0 {
		return b.totalSize
	}

	return len(b.bytes)
}

// Remaining returns the number of remaining (yet unread) bytes in the buffer.
// This can be useful to throttle writing.
func (b *Buffer) Remaining() int {
	return len(b.bytes) - (b.bitIndex >> 3)
}

// HasEnded checks whether the read position of the buffer is at the end and no more data is expected.
func (b *Buffer) HasEnded() bool {
	return b.hasEnded
}

// LoadReaderCallback is a callback that is called whenever the buffer needs more data.
func (b *Buffer) LoadReaderCallback(buffer *Buffer) {
	if b.hasEnded {
		return
	}

	p := b.available

	n, err := io.ReadFull(b.reader, p)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			p = p[:n]
		} else if err == io.EOF {
			b.hasEnded = true

			return
		}
	}

	if n == 0 {
		b.hasEnded = true

		return
	}

	b.Write(p)
}

func (b *Buffer) seek(pos int) {
	b.hasEnded = false

	if b.reader != nil && b.totalSize > 0 {
		seeker := b.reader.(io.Seeker)
		_, _ = seeker.Seek(int64(pos), io.SeekStart)
		b.bytes = b.bytes[:0]

		b.bitIndex = 0
	} else if b.reader == nil {
		if pos != 0 {
			return
		}

		b.bytes = b.bytes[:0]

		b.bitIndex = 0
	}
}

func (b *Buffer) tell() int {
	if b.reader != nil && b.totalSize > 0 {
		seeker := b.reader.(io.Seeker)
		off, _ := seeker.Seek(0, io.SeekCurrent)

		return int(off) + (b.bitIndex >> 3) - len(b.bytes)
	}

	return b.bitIndex >> 3
}

func (b *Buffer) discardReadBytes() {
	bytePos := b.bitIndex >> 3
	if bytePos == len(b.bytes) {
		b.bytes = b.bytes[:0]

		b.bitIndex = 0
	} else if bytePos > 0 {
		copy(b.bytes, b.bytes[bytePos:])
		b.bytes = b.bytes[:len(b.bytes)-bytePos]

		b.bitIndex -= bytePos << 3
	}
}

func (b *Buffer) has(count int) bool {
	if ((len(b.bytes) << 3) - b.bitIndex) >= count {
		return true
	}

	if b.loadCallback != nil {
		b.loadCallback(b)

		if ((len(b.bytes) << 3) - b.bitIndex) >= count {
			return true
		}
	}

	if b.totalSize != 0 && len(b.bytes) == b.totalSize {
		b.hasEnded = true
	}

	return false
}

func (b *Buffer) read(count int) int {
	if !b.has(count) {
		return 0
	}

	value := 0
	for count != 0 {
		currentByte := int(b.Bytes()[b.bitIndex>>3])

		remaining := 8 - (b.bitIndex & 7) // Remaining bits in byte
		read := count
		if remaining < count { // Bits in self run
			read = remaining
		}

		shift := remaining - read
		mask := 0xff >> (8 - read)

		value = (value << read) | ((currentByte & (mask << shift)) >> shift)

		b.bitIndex += read
		count -= read
	}

	return value
}

func (b *Buffer) read1() int {
	if !b.has(1) {
		return 0
	}

	currentByte := int(b.Bytes()[b.bitIndex>>3])

	shift := 7 - (b.bitIndex & 7)
	value := (currentByte & (1 << shift)) >> shift

	b.bitIndex += 1

	return value
}

func (b *Buffer) align() {
	b.bitIndex = ((b.bitIndex + 7) >> 3) << 3 // Align to next byte
}

func (b *Buffer) skip(count int) {
	if b.has(count) {
		b.bitIndex += count
	}
}

func (b *Buffer) skipBytes(v byte) int {
	b.align()

	skipped := 0
	for b.has(8) && b.Bytes()[b.bitIndex>>3] == v {
		b.bitIndex += 8
		skipped++
	}

	return skipped
}

func (b *Buffer) nextStartCode() int {
	b.align()

	for b.has(5 << 3) {
		data := b.Bytes()
		byteIndex := b.bitIndex >> 3
		if data[byteIndex] == 0x00 &&
			data[byteIndex+1] == 0x00 &&
			data[byteIndex+2] == 0x01 {
			b.bitIndex = (byteIndex + 4) << 3

			return int(data[byteIndex+3])
		}

		b.bitIndex += 8
	}

	return -1
}

func (b *Buffer) findStartCode(code int) int {
	for {
		current := b.nextStartCode()
		if current == code || current == -1 {
			return current
		}
	}
}

func (b *Buffer) hasStartCode(code int) int {
	prevBitIndex := b.bitIndex
	prevDiscardRead := b.discardRead

	b.discardRead = false
	current := b.findStartCode(code)

	b.bitIndex = prevBitIndex
	b.discardRead = prevDiscardRead

	return current
}

func (b *Buffer) findFrameSync() bool {
	var i int
	for i = b.bitIndex >> 3; i < len(b.bytes)-1; i++ {
		if b.Bytes()[i] == 0xFF && (b.Bytes()[i+1]&0xFE) == 0xFC {
			b.bitIndex = ((i + 1) << 3) + 3

			return true
		}
	}

	b.bitIndex = (i + 1) << 3

	return false
}

func (b *Buffer) peekNonZero(bitCount int) bool {
	if !b.has(bitCount) {
		return false
	}

	val := b.read(bitCount)
	b.bitIndex -= bitCount

	return val != 0
}

func (b *Buffer) readVlc(table []vlc) int {
	var state vlc

	for {
		state = table[int(state.Index)+b.read1()]
		if state.Index <= 0 {
			break
		}
	}

	return int(state.Value)
}

func (b *Buffer) readVlcUint(table []vlcUint) uint16 {
	var state vlcUint

	for {
		state = table[int(state.Index)+b.read1()]
		if state.Index <= 0 {
			break
		}
	}

	return state.Value
}

type vlc struct {
	Index int16
	Value int16
}

type vlcUint struct {
	Index int16
	Value uint16
}

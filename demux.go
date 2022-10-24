package mpeg

import (
	"errors"
)

// Packet is demuxed MPEG PS packet.
// The Type maps directly to the various MPEG-PES start codes.
// Pts is the presentation time stamp of the packet in seconds (not all packets have a pts Value).
type Packet struct {
	Type int
	Pts  float64
	Data []byte

	length int
}

// Various packet types.
const (
	PacketInvalidTS = -1

	PacketPrivate = 0xBD
	PacketAudio1  = 0xC0
	PacketAudio2  = 0xC1
	PacketAudio3  = 0xC2
	PacketAudio4  = 0xC2
	PacketVideo1  = 0xE0
)

// ErrInvalidHeader is the error returned when pack and system headers are not found.
var ErrInvalidHeader = errors.New("invalid MPEG-PS header")

// Demux an MPEG Program Stream (PS) data into separate packages.
type Demux struct {
	buf *Buffer

	sysClockRef    float64
	lastFileSize   int
	lastDecodedPts float64
	startTime      float64
	duration       float64

	startCode       int
	hasPackHeader   bool
	hasSystemHeader bool
	hasHeaders      bool

	numAudioStreams int
	numVideoStreams int

	currentPacket Packet
	nextPacket    Packet
}

// NewDemux creates a demuxer with buffer as a source.
func NewDemux(buf *Buffer) (*Demux, error) {
	dmux := &Demux{}

	dmux.buf = buf
	dmux.startTime = PacketInvalidTS
	dmux.duration = PacketInvalidTS
	dmux.startCode = -1

	if !dmux.HasHeaders() {
		return nil, ErrInvalidHeader
	}

	return dmux, nil
}

// Buffer returns demuxer buffer.
func (d *Demux) Buffer() *Buffer {
	return d.buf
}

// HasHeaders checks whether pack and system headers have been found.
// This will attempt to read the headers if non are present yet.
func (d *Demux) HasHeaders() bool {
	if d.hasHeaders {
		return true
	}

	// Decode pack header
	if !d.hasPackHeader {
		if d.startCode != startPack && d.buf.findStartCode(startPack) == -1 {
			return false
		}

		d.startCode = startPack
		if !d.buf.has(64) {
			return false
		}
		d.startCode = -1

		if d.buf.read(4) != 0x02 {
			return false
		}

		d.sysClockRef = d.decodeTime()
		d.buf.skip(1)
		d.buf.skip(22) // muxRate * 50
		d.buf.skip(1)

		d.hasPackHeader = true
	}

	// Decode system header
	if !d.hasSystemHeader {
		if d.startCode != startSystem && d.buf.findStartCode(startSystem) == -1 {
			return false
		}

		d.startCode = startSystem
		if !d.buf.has(56) {
			return false
		}
		d.startCode = -1

		d.buf.skip(16) // header length
		d.buf.skip(24) // rate bound
		d.numAudioStreams = d.buf.read(6)
		d.buf.skip(5) // misc flags
		d.numVideoStreams = d.buf.read(5)

		d.hasSystemHeader = true
	}

	d.hasHeaders = true
	return true
}

// NumVideoStreams returns the number of video streams found in the system header.
func (d *Demux) NumVideoStreams() int {
	if d.HasHeaders() {
		return d.numVideoStreams
	}

	return 0
}

// NumAudioStreams returns the number of audio streams found in the system header.
func (d *Demux) NumAudioStreams() int {
	if d.HasHeaders() {
		return d.numAudioStreams
	}

	return 0
}

// Rewind rewinds the internal buffer.
func (d *Demux) Rewind() {
	d.buf.Rewind()
	d.currentPacket.length = 0
	d.nextPacket.length = 0
	d.startCode = -1
}

// HasEnded checks whether the file has ended. This will be cleared on seeking or rewind.
func (d *Demux) HasEnded() bool {
	return d.buf.HasEnded()
}

// Seek seeks to a packet of the specified type with a PTS just before specified time.
// If forceIntra is true, only packets containing an intra frame will be
// considered - this only makes sense when the type is video.
// Note that the specified time is considered 0-based, regardless of the first PTS in the data source.
func (d *Demux) Seek(seekTime float64, typ int, forceIntra bool) *Packet {
	if !d.hasHeaders {
		return nil
	}

	// Using the current time, current byte position and the average bytes per
	// second for this file, try to jump to a byte position that hopefully has
	// packets containing timestamps within one second before to the desired seekTime.

	// If we hit close to the seekTime scan through all packets to find the
	// last one (just before the seekTime) containing an intra frame.
	// Otherwise, we should at least be closer than before. Calculate the bytes
	// per second for the jumped range and jump again.

	// The number of retries here is hard-limited to a generous amount. Usually
	// the correct range is found after 1-5 jumps, even for files with very
	// variable bitrates. If significantly more jumps are needed, there's
	// probably something wrong with the file, and we just avoid getting into an
	// infinite loop. 32 retries should be enough for anybody.

	duration := d.Duration(typ)
	fileSize := d.buf.Size()
	byteRate := float64(fileSize) / duration

	curTime := d.lastDecodedPts
	scanSpan := float64(1)

	if seekTime > duration {
		seekTime = duration
	} else if seekTime < 0 {
		seekTime = 0
	}
	seekTime += d.startTime

	for retry := 0; retry < 32; retry++ {
		foundPacketWithPts := false
		foundPacketInRange := false
		lastValidPacketStart := -1
		firstPacketTime := float64(PacketInvalidTS)

		curPos := d.buf.tell()

		// Estimate byte offset and jump to it.
		offset := (seekTime - curTime - scanSpan) * byteRate
		seekPos := curPos + int(offset)
		if seekPos < 0 {
			seekPos = 0
		} else if seekPos > fileSize-256 {
			seekPos = fileSize - 256
		}

		d.bufferSeek(seekPos)

		// Scan through all packets up to the seekTime to find the last packet
		// containing an intra frame.
		for d.buf.findStartCode(typ) != -1 {
			packetStart := d.buf.tell()
			packet := d.decodePacket(typ)

			// skip packet if it has no PTS
			if packet == nil || packet.Pts == PacketInvalidTS {
				continue
			}

			// Bail scanning through packets if we hit one that is outside seekTime - scanSpan.
			// We also adjust the curTime and byteRate values here so the next iteration can be a bit more precise.
			if packet.Pts > seekTime || packet.Pts < seekTime-scanSpan {
				foundPacketWithPts = true
				byteRate = float64(seekPos-curPos) / (packet.Pts - curTime)
				curTime = packet.Pts
				break
			}

			// If we are still here, it means this packet is in close range to
			// the seekTime. If this is the first packet for this jump position
			// record the PTS. If we later have to back off, when there was no
			// intra frame in this range, we can lower the seekTime to not scan
			// this range again.
			if !foundPacketInRange {
				foundPacketInRange = true
				firstPacketTime = packet.Pts
			}

			// Check if this is an intra frame packet. If so, record the buffer
			// position of the start of this packet. We want to jump back to it
			// later, when we know it's the last intra frame before desired
			// seek time.
			if forceIntra {
				for i := 0; i < packet.length-6; i++ {
					// Find the startPicture code
					if packet.Data[i] == 0x00 &&
						packet.Data[i+1] == 0x00 &&
						packet.Data[i+2] == 0x01 &&
						packet.Data[i+3] == 0x00 {
						// Bits 11--13 in the picture header contain the frame type, where 1=Intra
						if (packet.Data[i+5] & 0x38) == 8 {
							lastValidPacketStart = packetStart
						}
						break
					}
				}
			} else { // If we don't want intra frames, just use the last PTS found.
				lastValidPacketStart = packetStart
			}
		}

		switch {
		case lastValidPacketStart != -1:
			// If there was at least one intra frame in the range scanned above,
			// our search is over. Jump back to the packet and decode it again.
			d.bufferSeek(lastValidPacketStart)
			return d.decodePacket(typ)
		case foundPacketInRange:
			// If we hit the right range, but still found no intra frame, we have to increase the scanSpan.
			// This is done exponentially to also handle video files with very few intra frames.
			scanSpan *= 2
			seekTime = firstPacketTime
		case !foundPacketWithPts:
			// If we didn't find any packet with a PTS, it probably means we reached
			// the end of the file. Estimate byteRate and curTime accordingly.
			byteRate = float64(seekPos-curPos) / (duration - curTime)
			curTime = duration
		}
	}

	return nil
}

// StartTime gets the PTS of the first packet of this type.
// Returns PacketInvalidTS if packet of this packet type can not be found.
func (d *Demux) StartTime(typ int) float64 {
	if d.startTime != PacketInvalidTS {
		return d.startTime
	}

	prevPos := d.buf.tell()
	prevStartCode := d.startCode

	// Find first video PTS
	d.Rewind()
	for {
		packet := d.Decode()
		if packet == nil {
			break
		}

		if packet.Type == typ {
			d.startTime = packet.Pts
		}

		if d.startTime != PacketInvalidTS {
			break
		}
	}

	d.bufferSeek(prevPos)
	d.startCode = prevStartCode

	return d.startTime
}

// Duration gets the duration for the specified packet type - i.e. the span between
// the first PTS and the last PTS in the data source.
func (d *Demux) Duration(typ int) float64 {
	fileSize := d.buf.Size()
	if d.duration != PacketInvalidTS && d.lastFileSize == fileSize {
		return d.duration
	}

	prevPos := d.buf.tell()
	prevStartCode := d.startCode

	// Find last video PTS. Start searching 64kb from the end and go further back if needed.
	startRange := 64 * 1024
	maxRange := 4096 * 1024

	for r := startRange; r <= maxRange; r *= 2 {
		seekPos := fileSize - r
		if seekPos < 0 {
			seekPos = 0
			r = maxRange // make sure to bail after this round
		}
		d.bufferSeek(seekPos)
		d.currentPacket.length = 0

		lastPts := float64(PacketInvalidTS)
		for {
			packet := d.Decode()
			if packet == nil {
				break
			}

			if packet.Pts != PacketInvalidTS && packet.Type == typ {
				lastPts = packet.Pts
			}
		}

		if lastPts != PacketInvalidTS {
			d.duration = lastPts - d.StartTime(typ)
			break
		}
	}

	d.bufferSeek(prevPos)
	d.startCode = prevStartCode
	d.lastFileSize = fileSize

	return d.duration
}

// Decode decodes and returns the next packet.
func (d *Demux) Decode() *Packet {
	if !d.HasHeaders() {
		return nil
	}

	if d.currentPacket.length != 0 {
		bitsTillNextPacket := d.currentPacket.length << 3
		if !d.buf.has(bitsTillNextPacket) {
			return nil
		}

		d.buf.skip(bitsTillNextPacket)
		d.currentPacket.length = 0
	}

	// pending packet waiting for data?
	if d.nextPacket.length != 0 {
		return d.packet()
	}

	// pending packet waiting for header?
	if d.startCode != -1 {
		return d.decodePacket(d.startCode)
	}

	for {
		d.startCode = d.buf.nextStartCode()
		if d.startCode == PacketVideo1 || d.startCode == PacketPrivate ||
			(d.startCode >= PacketAudio1 && d.startCode <= PacketAudio4) {
			return d.decodePacket(d.startCode)
		}

		if d.startCode == -1 {
			break
		}
	}

	return nil
}

func (d *Demux) bufferSeek(pos int) {
	d.buf.seek(pos)
	d.currentPacket.length = 0
	d.nextPacket.length = 0
	d.startCode = -1
}

func (d *Demux) decodeTime() float64 {
	clock := d.buf.read(3) << 30
	d.buf.skip(1)
	clock |= d.buf.read(15) << 15
	d.buf.skip(1)
	clock |= d.buf.read(15)
	d.buf.skip(1)
	return float64(clock) / 90000.0
}

func (d *Demux) decodePacket(typ int) *Packet {
	if !d.buf.has(16 << 3) {
		return nil
	}

	d.startCode = -1

	d.nextPacket.Type = typ
	d.nextPacket.length = d.buf.read(16)
	d.nextPacket.length -= d.buf.skipBytes(0xff) // stuffing

	// skip P-STD
	if d.buf.read(2) == 0x01 {
		d.buf.skip(16)
		d.nextPacket.length -= 2
	}

	ptsDtsMarker := d.buf.read(2)
	switch {
	case ptsDtsMarker == 0x03:
		d.nextPacket.Pts = d.decodeTime()
		d.lastDecodedPts = d.nextPacket.Pts
		d.buf.skip(40) // skip DTS
		d.nextPacket.length -= 10
	case ptsDtsMarker == 0x02:
		d.nextPacket.Pts = d.decodeTime()
		d.lastDecodedPts = d.nextPacket.Pts
		d.nextPacket.length -= 5
	case ptsDtsMarker == 0x00:
		d.nextPacket.Pts = PacketInvalidTS
		d.buf.skip(4)
		d.nextPacket.length -= 1
	default:
		return nil // invalid
	}

	return d.packet()
}

func (d *Demux) packet() *Packet {
	if !d.buf.has(d.nextPacket.length << 3) {
		return nil
	}

	index := d.buf.Index()
	d.currentPacket.Data = d.buf.Bytes()[index : index+d.nextPacket.length : index+d.nextPacket.length]
	d.currentPacket.Type = d.nextPacket.Type
	d.currentPacket.Pts = d.nextPacket.Pts

	d.currentPacket.length = d.nextPacket.length
	d.nextPacket.length = 0

	return &d.currentPacket
}

const (
	startPack   = 0xBA
	startEnd    = 0xB9
	startSystem = 0xBB
)

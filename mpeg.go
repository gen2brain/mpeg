// Package mpeg implements MPEG-1 Video decoder, MP2 Audio decoder and MPEG-PS demuxer.
//
// This library provides several interfaces to demux and decode MPEG video and audio data.
// A high-level MPEG API combines the demuxer, video and audio decoders in an easy-to-use wrapper.
//
// With the high-level interface you have two options to decode video and audio:
//
// 1. Decode() and just hand over the delta time since the last call.
// It will decode everything needed and call your callbacks (specified through
// Set{Video|Audio}Callback()) any number of times.
//
// 2. Use DecodeVideo() and DecodeAudio() to decode exactly one frame of video or audio data at a time.
// How you handle the synchronization of both streams is up to you.
//
// If you only want to decode video *or* audio through these functions, you should
// disable the other stream (Set{Video|Audio}Enabled(false))
//
// Video data is decoded into a struct with all 3 planes (Y, Cb, Cr) stored in separate buffers,
// you can get image.YCbCr via YCbCr() function. You can either convert to image.RGBA on the CPU (slow)
// via the RGBA() function or do it on the GPU with the following matrix:
//
//	mat4 bt601 = mat4(
//	    1.16438,  0.00000,  1.59603, -0.87079,
//	    1.16438, -0.39176, -0.81297,  0.52959,
//	    1.16438,  2.01723,  0.00000, -1.08139,
//	    0, 0, 0, 1
//	);
//
//	gl_FragColor = vec4(y, cb, cr, 1.0) * bt601;
//
// Audio data is decoded into a struct with separate []float32 slices for left and right channel,
// and with a single []float32 slice with the samples for the left and right channel interleaved.
// You can convert interleaved samples to byte slice via the Bytes() function.
//
// There should be no need to use the lower level Demux, Video and Audio, if all you want to do is
// read/decode an MPEG-PS file. However, if you get raw mpeg1video data or raw mp2 audio data from a different source,
// these functions can be used to decode the raw data directly. Similarly, if you only want to analyze an MPEG-PS file
// or extract raw video or audio packets from it, you can use the Demux.
package mpeg

import (
	"bytes"
	"errors"
	"io"
	"time"
)

// VideoFunc callback function.
type VideoFunc func(mpeg *MPEG, frame *Frame)

// AudioFunc callback function.
type AudioFunc func(mpeg *MPEG, samples *Samples)

// ErrInvalidMPEG is the error returned when the reader is not a valid MPEG Program Stream.
var ErrInvalidMPEG = errors.New("invalid MPEG-PS")

// MPEG is high-level interface implementation.
type MPEG struct {
	demux *Demux
	time  float64

	loop        bool
	hasEnded    bool
	hasDecoders bool

	videoEnabled    bool
	videoPacketType int
	videoBuffer     *Buffer
	videoDecoder    *Video

	audioEnabled     bool
	audioPacketType  int
	audioStreamIndex int
	audioLeadTime    float64
	audioBuffer      *Buffer
	audioDecoder     *Audio

	done chan bool

	videoCallback VideoFunc
	audioCallback AudioFunc
}

// New creates a new MPEG instance.
func New(r io.Reader) (*MPEG, error) {
	m := &MPEG{}

	buf, err := NewBuffer(r)
	if err != nil {
		return nil, err
	}

	buf.SetLoadCallback(buf.LoadReaderCallback)

	if !buf.has(32) {
		return nil, ErrInvalidMPEG
	}
	if !bytes.Equal([]byte{0x00, 0x00, 0x01, 0xBA}, buf.Bytes()[0:4]) {
		return nil, ErrInvalidMPEG
	}
	buf.Rewind()

	m.demux, err = NewDemux(buf)
	if err != nil {
		return nil, err
	}

	m.done = make(chan bool, 1)

	m.videoEnabled = true
	m.audioEnabled = true
	m.initDecoders()

	return m, nil
}

// HasHeaders checks whether we have headers on all available streams, and if we can report the
// number of video/audio streams, video dimensions, framerate and audio samplerate.
func (m *MPEG) HasHeaders() bool {
	if !m.demux.HasHeaders() {
		return false
	}

	if !m.initDecoders() {
		return false
	}

	if (m.videoDecoder != nil && !m.videoDecoder.HasHeader()) || (m.audioDecoder != nil && !m.audioDecoder.HasHeader()) {
		return false
	}

	return true
}

// Probe probes the MPEG-PS data to find the actual number of video and audio streams within the buffer.
// For certain files (e.g. VideoCD) this can be more accurate than just reading the number of streams from the headers.
// This should only be used when the underlying reader is seekable.
// The necessary probe size is dependent on the files you expect to read. Usually a few hundred KB should be enough to find all streams.
// Use Num{Audio|Video}Streams() afterwards to get the number of streams in the file.
// Returns true if any streams were found within the probe size.
func (m *MPEG) Probe(probeSize int) bool {
	if !m.demux.Probe(probeSize) {
		return false
	}

	// Re-init decoders
	m.hasDecoders = false
	m.videoPacketType = 0
	m.audioPacketType = 0

	return m.initDecoders()
}

// Done returns done channel.
func (m *MPEG) Done() chan bool {
	return m.done
}

// Video returns video decoder.
func (m *MPEG) Video() *Video {
	return m.videoDecoder
}

// SetVideoCallback sets a video callback.
func (m *MPEG) SetVideoCallback(callback VideoFunc) {
	m.videoCallback = callback
}

// VideoEnabled checks whether video decoding is enabled.
func (m *MPEG) VideoEnabled() bool {
	return m.videoEnabled
}

// SetVideoEnabled sets whether video decoding is enabled.
func (m *MPEG) SetVideoEnabled(enabled bool) {
	m.videoEnabled = enabled

	if !enabled {
		m.videoPacketType = 0

		return
	}

	if m.initDecoders() && m.videoDecoder != nil {
		m.videoPacketType = PacketVideo1
	} else {
		m.videoPacketType = 0
	}
}

// NumVideoStreams returns the number of video streams (0--1) reported in the system header.
func (m *MPEG) NumVideoStreams() int {
	return m.demux.NumVideoStreams()
}

// Width returns the display width of the video stream.
func (m *MPEG) Width() int {
	if m.initDecoders() && m.videoDecoder != nil {
		return m.videoDecoder.Width()
	}

	return 0
}

// Height returns the display height of the video stream.
func (m *MPEG) Height() int {
	if m.initDecoders() && m.videoDecoder != nil {
		return m.videoDecoder.Height()
	}

	return 0
}

// Framerate returns the framerate of the video stream in frames per second.
func (m *MPEG) Framerate() float64 {
	if m.initDecoders() && m.videoDecoder != nil {
		return m.videoDecoder.Framerate()
	}

	return 0
}

// Audio returns video decoder.
func (m *MPEG) Audio() *Audio {
	return m.audioDecoder
}

// AudioFormat returns audio format.
func (m *MPEG) AudioFormat() AudioFormat {
	return m.audioDecoder.format
}

// SetAudioFormat sets audio format.
func (m *MPEG) SetAudioFormat(format AudioFormat) {
	m.audioDecoder.format = format
	m.audioDecoder.samples.format = format
}

// SetAudioCallback sets a audio callback.
func (m *MPEG) SetAudioCallback(callback AudioFunc) {
	m.audioCallback = callback
}

// AudioEnabled checks whether audio decoding is enabled.
func (m *MPEG) AudioEnabled() bool {
	return m.audioEnabled
}

// SetAudioEnabled sets whether audio decoding is enabled.
func (m *MPEG) SetAudioEnabled(enabled bool) {
	m.audioEnabled = enabled
	if !enabled {
		m.audioPacketType = 0

		return
	}

	if m.initDecoders() && m.audioDecoder != nil {
		m.audioPacketType = PacketAudio1 + m.audioStreamIndex
	} else {
		m.audioPacketType = 0
	}
}

// NumAudioStreams returns the number of audio streams (0--4) reported in the system header.
func (m *MPEG) NumAudioStreams() int {
	return m.demux.NumAudioStreams()
}

// SetAudioStream sets the desired audio stream (0--3). Default 0.
func (m *MPEG) SetAudioStream(streamIndex int) {
	if streamIndex < 0 || streamIndex > 3 {
		return
	}
	m.audioStreamIndex = streamIndex

	// Set the correct audio_packet_type
	m.SetAudioEnabled(m.audioEnabled)
}

// Samplerate returns the samplerate of the audio stream in samples per second.
func (m *MPEG) Samplerate() int {
	if m.initDecoders() && m.audioDecoder != nil {
		return m.audioDecoder.Samplerate()
	}

	return 0
}

// Channels returns the number of channels.
func (m *MPEG) Channels() int {
	if m.initDecoders() && m.audioDecoder != nil {
		return m.audioDecoder.Channels()
	}

	return 0
}

// AudioLeadTime returns the audio lead time in seconds - the time in which audio samples
// are decoded in advance (or behind) the video decode time.
func (m *MPEG) AudioLeadTime() time.Duration {
	return time.Duration(m.audioLeadTime * float64(time.Second))
}

// SetAudioLeadTime sets the audio lead time in seconds. Typically, this
// should be set to the duration of the buffer of the audio API that you use
// for output. E.g. for SDL2: (SDL_AudioSpec.samples / samplerate).
func (m *MPEG) SetAudioLeadTime(leadTime time.Duration) {
	m.audioLeadTime = leadTime.Seconds()
}

// Time returns the current internal time in seconds.
func (m *MPEG) Time() time.Duration {
	return time.Duration(m.time * float64(time.Second))
}

// Duration returns the video duration of the underlying source.
func (m *MPEG) Duration() time.Duration {
	return time.Duration(m.demux.Duration(PacketVideo1) * float64(time.Second))
}

// Rewind rewinds all buffers back to the beginning.
func (m *MPEG) Rewind() {
	if m.videoDecoder != nil {
		m.videoDecoder.Rewind()
	}

	if m.audioDecoder != nil {
		m.audioDecoder.Rewind()
	}

	m.demux.Rewind()
	m.time = 0
}

// Loop returns looping.
func (m *MPEG) Loop() bool {
	return m.loop
}

// SetLoop sets looping.
func (m *MPEG) SetLoop(loop bool) {
	m.loop = loop
}

// HasEnded checks whether the file has ended.
// If looping is enabled, this will always return false.
func (m *MPEG) HasEnded() bool {
	return m.hasEnded
}

// Decode advances the internal timer by seconds and decode video/audio up to this time.
// This will call the video_decode_callback and audio_decode_callback any number of times.
// A frame-skip is not implemented, i.e. everything up to current time will be decoded.
func (m *MPEG) Decode(tick time.Duration) {
	if !m.initDecoders() {
		return
	}

	decodeVideo := m.videoCallback != nil && m.videoPacketType != 0
	decodeAudio := m.audioCallback != nil && m.audioPacketType != 0

	if !decodeVideo && !decodeAudio {
		// Nothing to do here
		return
	}

	didDecode := false
	decodeVideoFailed := false
	decodeAudioFailed := false

	videoTargetTime := m.time + tick.Seconds()
	audioTargetTime := m.time + tick.Seconds() + m.audioLeadTime

	for {
		didDecode = false

		if decodeVideo && m.videoDecoder.Time() < videoTargetTime {
			frame := m.videoDecoder.Decode()
			if frame != nil {
				m.videoCallback(m, frame)
				didDecode = true
			} else {
				decodeVideoFailed = true
			}
		}

		if decodeAudio && m.audioDecoder.Time() < audioTargetTime {
			samples := m.audioDecoder.Decode()
			if samples != nil {
				m.audioCallback(m, samples)
				didDecode = true
			} else {
				decodeAudioFailed = true
			}
		}

		if !didDecode {
			break
		}
	}

	if (!decodeVideo || decodeVideoFailed) && (!decodeAudio || decodeAudioFailed) && m.demux.HasEnded() {
		m.handleEnd()

		return
	}

	m.time += tick.Seconds()
}

// DecodeVideo decodes and returns one video frame. Returns nil if no frame could be decoded
// (either because the source ended or data is corrupt). If you only want to decode video, you should
// disable audio via SetAudioEnabled(). The returned Frame is valid until the next call to DecodeVideo().
func (m *MPEG) DecodeVideo() *Frame {
	if !m.initDecoders() {
		return nil
	}

	if m.videoPacketType == 0 {
		return nil
	}

	frame := m.videoDecoder.Decode()
	if frame != nil {
		m.time = frame.Time
	} else if m.demux.HasEnded() {
		m.handleEnd()
	}

	return frame
}

// DecodeAudio decodes and returns one audio frame. Returns nil if no frame could be decoded
// (either because the source ended or data is corrupt). If you only want to decode audio, you should
// disable video via SetVideoEnabled(). The returned Samples is valid until the next call to DecodeAudio().
func (m *MPEG) DecodeAudio() *Samples {
	if !m.initDecoders() {
		return nil
	}

	if m.audioPacketType == 0 {
		return nil
	}

	samples := m.audioDecoder.Decode()
	if samples != nil {
		m.time = samples.Time
	} else if m.demux.HasEnded() {
		m.handleEnd()
	}

	return samples
}

// SeekFrame seeks, similar to Seek(), but will not call the VideoFunc callback,
// AudioFunc callback or make any attempts to sync audio.
// Returns the found frame or nil if no frame could be found.
func (m *MPEG) SeekFrame(tm time.Duration, seekExact bool) *Frame {
	if !m.initDecoders() {
		return nil
	}

	if m.videoPacketType == 0 {
		return nil
	}

	typ := m.videoPacketType
	startTime := m.demux.StartTime(typ)
	duration := m.demux.Duration(typ)

	if tm.Seconds() < 0 {
		tm = time.Duration(0)
	} else if tm.Seconds() > duration {
		tm = time.Duration(duration * float64(time.Second))
	}

	packet := m.demux.Seek(tm.Seconds(), typ, true)
	if packet == nil {
		return nil
	}

	// Disable writing to the audio buffer while decoding video
	prevAudioPacketType := m.audioPacketType
	m.audioPacketType = 0

	// Clear video buffer and decode the found packet
	m.videoDecoder.Rewind()
	m.videoDecoder.SetTime(packet.Pts - startTime)
	m.videoBuffer.Write(packet.Data)
	frame := m.videoDecoder.Decode()

	// If we want to seek to an exact frame, we have to decode all frames
	// on top of the intra frame we just jumped to.
	if seekExact {
		for frame != nil && frame.Time < tm.Seconds() {
			frame = m.videoDecoder.Decode()
		}
	}

	// Enable writing to the audio buffer again
	m.audioPacketType = prevAudioPacketType

	if frame != nil {
		m.time = frame.Time
	}

	m.hasEnded = false

	return frame
}

// Seek seeks to the specified time, clamped between 0 -- duration. This can only be
// used when the underlying Buffer is seekable.
// If seekExact is true this will seek to the exact time, otherwise it will
// seek to the last intra frame just before the desired time. Exact seeking can
// be slow, because all frames up to the seeked one have to be decoded on top of
// the previous intra frame.
// If seeking succeeds, this function will call the VideoFunc callback
// exactly once with the target frame. If audio is enabled, it will also call
// the AudioFunc callback any number of times, until the audioLeadTime is satisfied.
// Returns true if seeking succeeded or false if no frame could be found.
func (m *MPEG) Seek(tm time.Duration, seekExact bool) bool {
	frame := m.SeekFrame(tm, seekExact)

	if frame == nil {
		return false
	}

	if m.videoCallback != nil {
		m.videoCallback(m, frame)
	}

	// If audio is not enabled we are done here.
	if m.audioPacketType == 0 {
		return true
	}

	// Sync up Audio. This demuxes more packets until the first audio packet
	// with a PTS greater than the current time is found. Decode() is then
	// called to decode enough audio data to satisfy the audioLeadTime.

	startTime := m.demux.StartTime(m.videoPacketType)
	m.audioDecoder.Rewind()

	for {
		packet := m.demux.Decode()
		if packet == nil {
			break
		}

		if packet.Type == m.videoPacketType {
			m.videoBuffer.Write(packet.Data)
		} else if packet.Type == m.audioPacketType && packet.Pts-startTime > m.time {
			m.audioDecoder.SetTime(packet.Pts - startTime)
			m.audioBuffer.Write(packet.Data)

			// Disable writing to the audio buffer while decoding video
			prevAudioPacketType := m.audioPacketType
			m.audioPacketType = 0

			m.Decode(0)

			// Enable writing to the audio buffer again
			m.audioPacketType = prevAudioPacketType

			// Decode audio
			m.Decode(0)

			break
		}
	}

	return true
}

func (m *MPEG) initDecoders() bool {
	if m.hasDecoders {
		return true
	}

	if !m.demux.HasHeaders() {
		return false
	}

	var err error
	if m.demux.NumVideoStreams() > 0 {
		if m.videoEnabled {
			m.videoPacketType = PacketVideo1
		}

		if m.videoDecoder == nil {
			m.videoBuffer, err = NewBuffer(nil)
			if err != nil {
				return false
			}

			m.videoBuffer.SetLoadCallback(m.readVideoPacket)
			m.videoDecoder = NewVideo(m.videoBuffer)
		}
	}

	if m.demux.NumAudioStreams() > 0 {
		if m.audioEnabled {
			m.audioPacketType = PacketAudio1 + m.audioStreamIndex
		}

		if m.audioDecoder == nil {
			m.audioBuffer, err = NewBuffer(nil)
			if err != nil {
				return false
			}

			m.audioBuffer.SetLoadCallback(m.readAudioPacket)
			m.audioDecoder = NewAudio(m.audioBuffer)
		}
	}

	m.hasDecoders = true

	return true
}

func (m *MPEG) handleEnd() {
	if m.loop {
		m.Rewind()
	} else {
		m.hasEnded = true
		m.done <- true
	}
}

func (m *MPEG) readVideoPacket(buffer *Buffer) {
	m.readPackets(m.videoPacketType)
}

func (m *MPEG) readAudioPacket(buffer *Buffer) {
	m.readPackets(m.audioPacketType)
}

func (m *MPEG) readPackets(requestedType int) {
	for {
		packet := m.demux.Decode()
		if packet == nil {
			break
		}

		if packet.Type == m.videoPacketType {
			m.videoBuffer.Write(packet.Data)
		} else if packet.Type == m.audioPacketType {
			m.audioBuffer.Write(packet.Data)
		}

		if packet.Type == requestedType {
			return
		}
	}

	if m.demux.HasEnded() {
		if m.videoBuffer != nil {
			m.videoBuffer.SignalEnd()
		}

		if m.audioBuffer != nil {
			m.audioBuffer.SignalEnd()
		}
	}
}

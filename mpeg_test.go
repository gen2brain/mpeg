package mpeg_test

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"testing"
	"time"

	"github.com/gen2brain/mpeg"
)

//go:embed testdata/test.mp2
var testMp2 []byte

//go:embed testdata/test.mpeg1video
var testMpeg1video []byte

//go:embed testdata/test.mpg
var testMpg []byte

func TestBuffer(t *testing.T) {
	buffer, err := mpeg.NewBuffer(bytes.NewReader(testMpg))
	if err != nil {
		t.Fatal(err)
	}

	buffer.SetLoadCallback(buffer.LoadReaderCallback)

	if !buffer.Seekable() {
		t.Error("Seekable: not seekable")
	}

	if buffer.Size() != 380932 {
		t.Errorf("Size: got %d, want %d", buffer.Size(), 380932)
	}
}

func TestDemux(t *testing.T) {
	buf, err := mpeg.NewBuffer(bytes.NewReader(testMpg))
	if err != nil {
		t.Fatal(err)
	}

	buf.SetLoadCallback(buf.LoadReaderCallback)

	demux, err := mpeg.NewDemux(buf)
	if err != nil {
		t.Fatal(err)
	}

	if !demux.Probe(5000 * 1024) {
		t.Error("Probe: no MPEG video or audio streams found")
	}

	if !demux.HasHeaders() {
		t.Error("HasHeaders: no headers")
	}

	if demux.NumAudioStreams() != 1 {
		t.Errorf("NumAudioStreams: got %d, want %d", demux.NumAudioStreams(), 1)
	}

	if demux.NumVideoStreams() != 1 {
		t.Errorf("NumVideoStreams: got %d, want %d", demux.NumVideoStreams(), 1)
	}

	if int(demux.Duration(mpeg.PacketVideo1)) != 9 {
		t.Errorf("Duration: got %d, want %d", int(demux.Duration(mpeg.PacketVideo1)), 9)
	}

	packet := demux.Decode()
	if packet == nil {
		t.Fatal("Decode: packet is nil")
	}

	if packet.Type != mpeg.PacketVideo1 {
		t.Errorf("Type: got %d, want %d", packet.Type, mpeg.PacketVideo1)
	}
}

// TestDemuxStartTimeDuration checks that StartTime and Duration are reported
// per packet type, return the lowest/highest PTS (not just the first/last
// packet, which can be reordered), and that Duration includes the final frame.
func TestDemuxStartTimeDuration(t *testing.T) {
	newDemux := func() *mpeg.Demux {
		buf, err := mpeg.NewBuffer(bytes.NewReader(testMpg))
		if err != nil {
			t.Fatal(err)
		}
		buf.SetLoadCallback(buf.LoadReaderCallback)
		d, err := mpeg.NewDemux(buf)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	const (
		videoStart    = 0.810078
		audioStart    = 0.810078
		videoDur      = 9.233333
		audioDur      = 9.325711
		firstVideoPts = 0.876744 // higher than the start: a later reordered packet has the lowest PTS
		eps           = 0.001
	)

	near := func(name string, got, want float64) {
		if math.Abs(got-want) > eps {
			t.Errorf("%s: got %.6f, want %.6f", name, got, want)
		}
	}

	// Values must not depend on query order (the cache is keyed by type).
	vFirst := newDemux()
	near("video StartTime", vFirst.StartTime(mpeg.PacketVideo1), videoStart)
	near("video Duration", vFirst.Duration(mpeg.PacketVideo1), videoDur)
	near("audio StartTime after video", vFirst.StartTime(mpeg.PacketAudio1), audioStart)
	near("audio Duration after video", vFirst.Duration(mpeg.PacketAudio1), audioDur)

	aFirst := newDemux()
	near("audio StartTime", aFirst.StartTime(mpeg.PacketAudio1), audioStart)
	near("audio Duration", aFirst.Duration(mpeg.PacketAudio1), audioDur)
	near("video StartTime after audio", aFirst.StartTime(mpeg.PacketVideo1), videoStart)
	near("video Duration after audio", aFirst.Duration(mpeg.PacketVideo1), videoDur)

	// The start must be the lowest PTS, below the first packet decoded.
	if s := newDemux().StartTime(mpeg.PacketVideo1); s >= firstVideoPts {
		t.Errorf("video StartTime %.6f did not look past the first packet (%.6f)", s, firstVideoPts)
	}
}

func TestDemuxNegativePacketLength(t *testing.T) {
	data := []byte{
		0x00, 0x00, 0x01, 0xBA, 0x21, 0x00, 0x01, 0x00, 0x01, 0x80, 0x00, 0x01,
		0x00, 0x00, 0x01, 0xBB, 0x00, 0x06, 0x80, 0x00, 0x01, 0x04, 0xE1, 0xFF,
		0x00, 0x00, 0x01, 0xE0, 0x00, 0x02, 0x31, 0x00, 0x01, 0x00, 0x01, 0x11, 0x00, 0x01, 0x00, 0x01,
		0x00, 0x00, 0x01, 0xE0, 0x00, 0x05, 0x0F, 'A', 'B', 'C', 'D',
	}
	data = append(data, make([]byte, 32)...)

	buf, err := mpeg.NewBuffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	buf.Write(data)

	demux, err := mpeg.NewDemux(buf)
	if err != nil {
		t.Fatal(err)
	}

	for range 4 {
		if p := demux.Decode(); p != nil && string(p.Data) == "ABCD" {
			return
		}
	}
	t.Fatal("the packet after the invalid one was not found")
}

func TestTruncated(t *testing.T) {
	const limit = 10000

	for _, tc := range []struct {
		name   string
		data   []byte
		decode func(data []byte) int
	}{
		{"mpeg1video", testMpeg1video, func(data []byte) int {
			buf, _ := mpeg.NewBuffer(bytes.NewReader(data))
			buf.SetLoadCallback(buf.LoadReaderCallback)
			video := mpeg.NewVideo(buf)
			n := 0
			for n < limit && video.Decode() != nil {
				n++
			}

			return n
		}},
		{"mp2", testMp2, func(data []byte) int {
			buf, _ := mpeg.NewBuffer(bytes.NewReader(data))
			buf.SetLoadCallback(buf.LoadReaderCallback)
			audio := mpeg.NewAudio(buf)
			n := 0
			for n < limit && audio.Decode() != nil {
				n++
			}

			return n
		}},
		{"mpg video", testMpg, func(data []byte) int {
			n := 0
			if mpg, err := mpeg.New(bytes.NewReader(data)); err == nil {
				for n < limit && mpg.DecodeVideo() != nil {
					n++
				}
			}

			return n
		}},
		{"mpg audio", testMpg, func(data []byte) int {
			n := 0
			if mpg, err := mpeg.New(bytes.NewReader(data)); err == nil {
				for n < limit && mpg.DecodeAudio() != nil {
					n++
				}
			}

			return n
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cuts []int
			for n := 1; n < len(tc.data); n += 4999 {
				cuts = append(cuts, n)
			}
			for i := 0; i+4 < min(len(tc.data), 1<<16); i++ {
				if bytes.HasPrefix(tc.data[i:], []byte{0, 0, 1, 0}) {
					cuts = append(cuts, i+4)
				}
			}

			for _, n := range cuts {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("truncated at %d of %d bytes: %v", n, len(tc.data), r)
						}
					}()
					if got := tc.decode(tc.data[:n]); got == limit {
						t.Fatalf("truncated at %d of %d bytes: still decoding after %d units", n, len(tc.data), got)
					}
				}()
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(testMpg[:16384])
	f.Add(testMpeg1video[:8192])
	f.Add(testMp2[:4096])

	f.Fuzz(func(t *testing.T, data []byte) {
		const limit = 64

		if mpg, err := mpeg.New(bytes.NewReader(data)); err == nil {
			for range limit {
				if mpg.DecodeVideo() == nil {
					break
				}
			}
		}
		if mpg, err := mpeg.New(bytes.NewReader(data)); err == nil {
			for range limit {
				if mpg.DecodeAudio() == nil {
					break
				}
			}
		}

		buf, err := mpeg.NewBuffer(bytes.NewReader(data))
		if err != nil {
			return
		}
		buf.SetLoadCallback(buf.LoadReaderCallback)
		video := mpeg.NewVideo(buf)
		for range limit {
			if video.Decode() == nil {
				break
			}
		}

		buf, err = mpeg.NewBuffer(bytes.NewReader(data))
		if err != nil {
			return
		}
		buf.SetLoadCallback(buf.LoadReaderCallback)
		audio := mpeg.NewAudio(buf)
		for range limit {
			if audio.Decode() == nil {
				break
			}
		}
	})
}

func TestLeadingData(t *testing.T) {
	decode := func(data []byte) []byte {
		mpg, err := mpeg.New(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		mpg.SetAudioEnabled(false)
		var out []byte
		for frame := mpg.DecodeVideo(); frame != nil; frame = mpg.DecodeVideo() {
			out = append(out, frame.Y.Data...)
		}

		return out
	}

	junk := make([]byte, 65536)
	copy(junk, "RIFF\x00\x00\x00\x00CDXAfmt ")
	for i := 16; i < len(junk); i++ {
		junk[i] = byte(i*7 + 3)
	}
	want := decode(testMpg)
	if got := decode(append(junk, testMpg...)); !bytes.Equal(got, want) {
		t.Fatalf("leading data changed the output: %d bytes, want %d", len(got), len(want))
	}

	if _, err := mpeg.New(bytes.NewReader(junk)); !errors.Is(err, mpeg.ErrInvalidMPEG) {
		t.Fatalf("no pack header: %v, want ErrInvalidMPEG", err)
	}
	pack := append(append([]byte{}, junk...), 0x00, 0x00, 0x01, 0xBA, 0x21, 0x00, 0x01, 0x00, 0x01, 0x80, 0x00, 0x01)
	if _, err := mpeg.New(bytes.NewReader(pack)); !errors.Is(err, mpeg.ErrInvalidHeader) {
		t.Fatalf("pack header without a system header: %v, want ErrInvalidHeader", err)
	}
}

func TestAudio(t *testing.T) {
	buf, err := mpeg.NewBuffer(bytes.NewReader(testMp2))
	if err != nil {
		t.Fatal(err)
	}

	buf.SetLoadCallback(buf.LoadReaderCallback)

	audio := mpeg.NewAudio(buf)
	if !audio.HasHeader() {
		t.Error("HasHeader: no header")
	}

	if audio.Samplerate() != 44100 {
		t.Errorf("Samplerate: got %d, want %d", audio.Samplerate(), 44100)
	}

	if audio.Channels() != 1 {
		t.Errorf("Channels: got %d, want %d", audio.Channels(), 1)
	}

	audio.Rewind()
	samples := audio.Decode()

	if samples == nil {
		t.Error("Decode: samples is nil")
	}
}

// TestAudioGolden decodes the whole test clip and hashes the synthesized
// samples, guarding the synthesis filter against accidental numeric changes.
func TestAudioGolden(t *testing.T) {
	buf, err := mpeg.NewBuffer(bytes.NewReader(testMp2))
	if err != nil {
		t.Fatal(err)
	}
	buf.SetLoadCallback(buf.LoadReaderCallback)

	audio := mpeg.NewAudio(buf)

	h := fnv.New64a()
	var b [4]byte
	frames := 0
	for {
		s := audio.Decode()
		if s == nil {
			break
		}
		for _, f := range s.Interleaved {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(f))
			h.Write(b[:])
		}
		frames++
	}

	// Output depends on which multiply-adds fuse to FMA, which varies by target;
	// all correct. Hashes: no FMA (amd64), windowing FMA (amd64 AVX2),
	// windowing+idct36 FMA (arm64).
	want := map[uint64]bool{
		0x405d166317d6fb8d: true,
		0x27b94bf42ccbd675: true,
		0x3c761702da063f05: true,
	}
	if got := h.Sum64(); !want[got] {
		t.Fatalf("audio output hash: got %#016x (frames=%d)", got, frames)
	}
}

// TestVideoGolden hashes the visible area of every frame of test.mpg; the
// expected value is pl_mpeg's output without the two leading B frames of the open first GOP.
func TestVideoGolden(t *testing.T) {
	mpg, err := mpeg.New(bytes.NewReader(testMpg))
	if err != nil {
		t.Fatal(err)
	}
	mpg.SetAudioEnabled(false)

	h := fnv.New64a()
	frames := 0
	for frame := mpg.DecodeVideo(); frame != nil; frame = mpg.DecodeVideo() {
		for i, p := range []*mpeg.Plane{&frame.Y, &frame.Cb, &frame.Cr} {
			w, rows := frame.Width, frame.Height
			if i > 0 {
				w, rows = (w+1)/2, (rows+1)/2
			}
			for y := range rows {
				h.Write(p.Data[y*p.Width : y*p.Width+w])
			}
		}
		frames++
	}

	const want uint64 = 0x9426ca33696b9758
	if got := h.Sum64(); got != want {
		t.Fatalf("video output hash: got %#016x want %#016x (frames=%d)", got, want, frames)
	}
}

func TestVideo(t *testing.T) {
	buf, err := mpeg.NewBuffer(bytes.NewReader(testMpeg1video))
	if err != nil {
		t.Fatal(err)
	}

	buf.SetLoadCallback(buf.LoadReaderCallback)

	video := mpeg.NewVideo(buf)
	if !video.HasHeader() {
		t.Error("HasHeader: no header")
	}

	if video.Width() != 160 {
		t.Errorf("Width: got %d, want %d", video.Width(), 120)
	}

	if video.Height() != 120 {
		t.Errorf("Height: got %d, want %d", video.Height(), 120)
	}

	if video.Framerate() != 30.0 {
		t.Errorf("Framerate: got %f, want %f", video.Framerate(), 30.0)
	}

	frame := video.Decode()
	if frame == nil {
		t.Fatal("Decode: frame is nil")
	}

	if frame.Width != video.Width() {
		t.Errorf("Width: got %d, want %d", frame.Width, video.Width())
	}

	if len(frame.Y.Data) != 20480 {
		t.Errorf("Y: got %d, want %d", len(frame.Y.Data), 20480)
	}

	if len(frame.Cb.Data) != len(frame.Y.Data)/4 {
		t.Errorf("Cb: got %d, want %d", len(frame.Cb.Data), len(frame.Y.Data)/4)
	}
}

func TestMpeg(t *testing.T) {
	mpg, err := mpeg.New(bytes.NewReader(testMpg))
	if err != nil {
		t.Fatal(err)
	}

	if !mpg.Probe(5000 * 1024) {
		t.Error("Probe: no MPEG video or audio streams found")
	}

	if !mpg.HasHeaders() {
		t.Error("HasHeaders: no headers")
	}

	if mpg.NumAudioStreams() != 1 {
		t.Errorf("NumAudioStreams: got %d, want %d", mpg.NumAudioStreams(), 1)
	}

	if mpg.NumVideoStreams() != 1 {
		t.Errorf("NumVideoStreams: got %d, want %d", mpg.NumVideoStreams(), 1)
	}

	if mpg.Width() != 160 {
		t.Errorf("Width: got %d, want %d", mpg.Width(), 120)
	}

	if mpg.Height() != 120 {
		t.Errorf("Height: got %d, want %d", mpg.Height(), 120)
	}

	if mpg.Framerate() != 30.0 {
		t.Errorf("Framerate: got %f, want %f", mpg.Framerate(), 30.0)
	}

	mpg.SetAudioStream(0)
	mpg.SetAudioEnabled(true)
	if !mpg.AudioEnabled() {
		t.Errorf("AudioEnabled: got %v, want %v", mpg.AudioEnabled(), true)
	}

	mpg.SetVideoEnabled(true)
	if !mpg.VideoEnabled() {
		t.Errorf("VideoEnabled: got %v, want %v", mpg.VideoEnabled(), true)
	}

	if mpg.Samplerate() != 44100 {
		t.Errorf("Samplerate: got %d, want %d", mpg.Samplerate(), 44100)
	}

	if mpg.Channels() != 1 {
		t.Errorf("Channels: got %d, want %d", mpg.Channels(), 1)
	}

	mpg.SetAudioLeadTime(1 * time.Second)
	if mpg.AudioLeadTime().Seconds() != 1 {
		t.Errorf("AudioLeadTime: got %s, want %f", mpg.AudioLeadTime(), 1.0)
	}

	if int(mpg.Duration().Seconds()) != 9 {
		t.Errorf("Duration: got %d, want %d", int(mpg.Duration()), 9)
	}

	mpg.Rewind()
	mpg.SetLoop(false)
	if mpg.Loop() {
		t.Errorf("Loop: got %v, want %v", mpg.Loop(), false)
	}

	mpg.SetAudioEnabled(false)
	mpg.SetVideoEnabled(true)
	frame := mpg.DecodeVideo()
	if frame == nil {
		t.Fatal("DecodeVideo: frame is nil")
	}

	if frame.Width != mpg.Width() {
		t.Errorf("Width: got %d, want %d", frame.Width, mpg.Width())
	}

	if len(frame.Y.Data) != 20480 {
		t.Errorf("Y: got %d, want %d", len(frame.Y.Data), 20480)
	}

	if len(frame.Cb.Data) != len(frame.Y.Data)/4 {
		t.Errorf("Cb: got %d, want %d", len(frame.Cb.Data), len(frame.Y.Data)/4)
	}

	mpg.SetAudioEnabled(true)
	mpg.SetVideoEnabled(false)
	samples := mpg.DecodeAudio()
	if samples == nil {
		t.Fatal("DecodeAudio: samples is nil")
	}

	if len(samples.Bytes()) != len(samples.Interleaved)*4 {
		t.Errorf("bytes: got %d, want %d", len(samples.Bytes()), len(samples.Interleaved)*4)
	}

	mpg.SetAudioEnabled(true)
	mpg.SetVideoEnabled(true)
	ret := mpg.Seek(1, false)
	if !ret {
		t.Fatal("Seek: returned false")
	}

	frame = mpg.SeekFrame(1*time.Second, true)
	if frame == nil {
		t.Fatal("SeekFrame: frame is nil")
	}

	// Seeking past the end clamps to the duration and returns the last frame.
	frame = mpg.SeekFrame(100*time.Second, true)
	if frame == nil {
		t.Fatal("SeekFrame: frame is nil")
	}
	if frame.Time < mpg.Duration().Seconds()-1.0 {
		t.Errorf("SeekFrame: got time %f, want near duration %f", frame.Time, mpg.Duration().Seconds())
	}

	mpg.SetAudioCallback(func(m *mpeg.MPEG, s *mpeg.Samples) {})
	mpg.SetVideoCallback(func(m *mpeg.MPEG, f *mpeg.Frame) {})
	mpg.Decode(1 * time.Second)
}

// TestSeekAudioTime checks that an exact seek off a frame boundary (3001ms)
// does not leave Audio().Time() out of sync with the stream.
func TestSeekAudioTime(t *testing.T) {
	newMpeg := func() *mpeg.MPEG {
		m, err := mpeg.New(bytes.NewReader(testMpg))
		if err != nil {
			t.Fatal(err)
		}
		m.SetAudioCallback(func(_ *mpeg.MPEG, _ *mpeg.Samples) {})
		m.SetVideoCallback(func(_ *mpeg.MPEG, _ *mpeg.Frame) {})
		return m
	}

	// Audio time must stay within one audio packet of the stream time.
	const tolerance = 0.5

	var times []float64
	for _, ms := range []int{1000, 2000, 3000, 3001, 4000, 5000} {
		m := newMpeg()
		if !m.Seek(time.Duration(ms)*time.Millisecond, true) {
			t.Fatalf("seek to %dms returned false", ms)
		}

		streamTime := m.Time().Seconds()
		audioTime := m.Audio().Time()

		if math.Abs(audioTime-streamTime) > tolerance {
			t.Errorf("seek to %dms: audio time %.4f too far from stream time %.4f",
				ms, audioTime, streamTime)
		}

		times = append(times, audioTime)
	}

	// A 1ms change (3000ms vs 3001ms) must not jump the audio time.
	if d := math.Abs(times[3] - times[2]); d > tolerance {
		t.Errorf("audio time jumped by %.4f between 3000ms and 3001ms seeks", d)
	}
}

// TestSeekVideoCallbackOnce checks that Seek() fires the video callback exactly
// once, for both exact and non-exact seeks.
func TestSeekVideoCallbackOnce(t *testing.T) {
	for _, exact := range []bool{false, true} {
		m, err := mpeg.New(bytes.NewReader(testMpg))
		if err != nil {
			t.Fatal(err)
		}

		count := 0
		m.SetVideoCallback(func(_ *mpeg.MPEG, _ *mpeg.Frame) { count++ })
		m.SetAudioCallback(func(_ *mpeg.MPEG, _ *mpeg.Samples) {})

		if !m.Seek(3*time.Second, exact) {
			t.Fatalf("seek (exact=%v) returned false", exact)
		}

		if count != 1 {
			t.Errorf("seek (exact=%v): video callback fired %d times, want 1", exact, count)
		}
	}
}

func BenchmarkDecodeVideo(b *testing.B) {
	mpg, err := mpeg.New(bytes.NewReader(testMpg))
	if err != nil {
		b.Fatal(err)
	}

	mpg.SetLoop(true)
	mpg.SetAudioEnabled(false)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mpg.DecodeVideo()
	}
}

func BenchmarkDecodeAudio(b *testing.B) {
	mpg, err := mpeg.New(bytes.NewReader(testMpg))
	if err != nil {
		b.Fatal(err)
	}

	mpg.SetLoop(true)
	mpg.SetVideoEnabled(false)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		mpg.DecodeAudio()
	}
}

func BenchmarkRGBA(b *testing.B) {
	mpg, err := mpeg.New(bytes.NewReader(testMpg))
	if err != nil {
		b.Fatal(err)
	}

	frame := mpg.DecodeVideo()
	if frame == nil {
		b.Fatal("DecodeVideo: frame is nil")
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		frame.RGBA()
	}
}

func TestEndTwice(t *testing.T) {
	mpg, err := mpeg.New(bytes.NewReader(testMpg))
	if err != nil {
		t.Fatal(err)
	}

	finished := make(chan struct{})
	go func() {
		for mpg.DecodeVideo() != nil {
		}
		for mpg.DecodeAudio() != nil {
		}
		mpg.DecodeVideo()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("decoding blocked after the end of the stream")
	}
	if !mpg.HasEnded() {
		t.Fatal("HasEnded is false")
	}
	select {
	case <-mpg.Done():
	default:
		t.Fatal("Done did not fire")
	}
}

func TestDamagedSlice(t *testing.T) {
	buf, err := mpeg.NewBuffer(bytes.NewReader(testMpg))
	if err != nil {
		t.Fatal(err)
	}
	buf.SetLoadCallback(buf.LoadReaderCallback)
	demux, err := mpeg.NewDemux(buf)
	if err != nil {
		t.Fatal(err)
	}
	var es []byte
	for p := demux.Decode(); p != nil; p = demux.Decode() {
		if p.Type == mpeg.PacketVideo1 {
			es = append(es, p.Data...)
		}
	}

	count := func(data []byte) int {
		buf, _ := mpeg.NewBuffer(bytes.NewReader(data))
		buf.SetLoadCallback(buf.LoadReaderCallback)
		video := mpeg.NewVideo(buf)
		n := 0
		for video.Decode() != nil {
			n++
		}

		return n
	}

	var pictures []int
	for i := 0; i+3 < len(es); i++ {
		if es[i] == 0 && es[i+1] == 0 && es[i+2] == 1 && es[i+3] == 0 {
			pictures = append(pictures, i)
		}
	}
	next := pictures[5]
	slice := bytes.LastIndex(es[:next], []byte{0, 0, 1})
	if next-slice < 32 {
		t.Fatalf("last slice of picture 4 is only %d bytes", next-slice)
	}
	damaged := append(append([]byte{}, es[:slice+16]...), es[next:]...)

	if want, got := count(es), count(damaged); got != want {
		t.Fatalf("damaged slice lost pictures: %d frames, want %d", got, want)
	}
}

package mpeg_test

import (
	"bytes"
	_ "embed"
	"testing"

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

	mpg.SetAudioLeadTime(1)
	if mpg.AudioLeadTime() != 1 {
		t.Errorf("AudioLeadTime: got %f, want %f", mpg.AudioLeadTime(), 1.0)
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

	frame = mpg.SeekFrame(1, true)
	if frame == nil {
		t.Fatal("SeekFrame: frame is nil")
	}

	frame = mpg.SeekFrame(100, true)
	if frame != nil {
		t.Fatal("SeekFrame: expected nil frame")
	}

	mpg.SetAudioCallback(func(m *mpeg.MPEG, s *mpeg.Samples) {})
	mpg.SetVideoCallback(func(m *mpeg.MPEG, f *mpeg.Frame) {})
	mpg.Decode(1)
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

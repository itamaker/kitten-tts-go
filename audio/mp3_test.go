package audio

import "testing"

// countMP3Frames walks MPEG-1 Layer III frames using each header's frame length
// (the encoder emits CBR 128 kbps / 44.1 kHz), which is robust against the false
// matches a naive 0xFF byte scan hits inside frame payloads.
func countMP3Frames(b []byte) int {
	const bitrate, sampleRate = 128_000, 44_100
	n, i := 0, 0
	for i+4 <= len(b) {
		if b[i] == 0xFF && b[i+1]&0xE0 == 0xE0 { // 11-bit frame sync
			pad := int(b[i+2]>>1) & 1
			n++
			i += 144*bitrate/sampleRate + pad
			continue
		}
		i++
	}
	return n
}

// TestMP3FrameCountFullLength guards the mono-encoding fix: shine-mp3's Write
// strides by samplesPerPass*2 (assuming interleaved stereo), so feeding it mono
// dropped every other frame and produced static. encodeMP3 now duplicates the
// mono signal into both channels, so the output must cover the whole input.
func TestMP3FrameCountFullLength(t *testing.T) {
	in := make([]float32, SampleRate) // 1 s @ 24 kHz -> 44.1 kHz
	for i := range in {
		in[i] = 0.1
	}
	data, err := encodeMP3(in)
	if err != nil {
		t.Fatal(err)
	}

	const framedSamples = 1152
	want := (44_100 + framedSamples - 1) / framedSamples // 39 frames
	if got := countMP3Frames(data); got < want-1 || got > want+1 {
		t.Fatalf("mp3 frame count = %d, want ~%d (samples dropped?)", got, want)
	}
}

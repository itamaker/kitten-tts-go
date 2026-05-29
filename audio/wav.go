package audio

import (
	"bytes"
	"encoding/binary"
)

func init() {
	register("pcm", func() Encoder { return pcmEncoder{} })
	register("wav", func() Encoder { return wavEncoder{} })
}

// pcmEncoder emits raw 16-bit signed little-endian PCM.
type pcmEncoder struct{}

func (pcmEncoder) Name() string                       { return "pcm" }
func (pcmEncoder) ContentType() string                { return "audio/pcm" }
func (pcmEncoder) Encode(s []float32) ([]byte, error) { return EncodePCM(s), nil }

// wavEncoder emits a WAV file (mono, 24 kHz, 16-bit PCM).
type wavEncoder struct{}

func (wavEncoder) Name() string                       { return "wav" }
func (wavEncoder) ContentType() string                { return "audio/wav" }
func (wavEncoder) Encode(s []float32) ([]byte, error) { return encodeWAV(s), nil }

// EncodePCM encodes samples as raw 16-bit signed little-endian PCM. It is
// exported because the streaming API emits raw PCM chunks directly.
func EncodePCM(samples []float32) []byte {
	buf := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(floatToI16(s)))
	}
	return buf
}

func encodeWAV(samples []float32) []byte {
	pcm := EncodePCM(samples)

	const (
		channels      = 1
		bitsPerSample = 16
	)
	byteRate := SampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8

	var buf bytes.Buffer
	buf.Grow(44 + len(pcm))
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16)) // PCM fmt chunk size
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // PCM format
	binary.Write(&buf, binary.LittleEndian, uint16(channels))
	binary.Write(&buf, binary.LittleEndian, uint32(SampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample))
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}

package audio

import (
	"bytes"
	"fmt"

	shinemp3 "github.com/braheezy/shine-mp3/pkg/mp3"
)

func init() {
	register("mp3", func() Encoder { return mp3Encoder{} })
}

// mp3Encoder emits MP3 audio using the pure-Go shine encoder.
type mp3Encoder struct{}

func (mp3Encoder) Name() string                       { return "mp3" }
func (mp3Encoder) ContentType() string                { return "audio/mpeg" }
func (mp3Encoder) Encode(s []float32) ([]byte, error) { return encodeMP3(s) }

// encodeMP3 resamples to 44.1 kHz before encoding for universal player
// compatibility (24 kHz is an MPEG-2 rate that some players mishandle).
func encodeMP3(samples []float32) ([]byte, error) {
	const mp3Rate = 44100
	resampled := Resample(samples, SampleRate, mp3Rate)

	pcm := make([]int16, len(resampled))
	for i, s := range resampled {
		pcm[i] = floatToI16(s)
	}

	var out bytes.Buffer
	if err := shinemp3.NewEncoder(mp3Rate, 1).Write(&out, pcm); err != nil {
		return nil, fmt.Errorf("mp3 encode: %w", err)
	}
	return out.Bytes(), nil
}

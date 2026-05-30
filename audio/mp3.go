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
//
// shine-mp3's Write advances its read cursor by samplesPerPass*2 every frame —
// it assumes interleaved stereo. Feeding it a mono buffer therefore drops every
// other frame and produces a buzzing/static artifact. We sidestep that by
// encoding as 2 channels with both equal to the mono signal, padded to a whole
// number of frames so the final (unsafe, stride-2) read stays in bounds.
func encodeMP3(samples []float32) ([]byte, error) {
	const (
		mp3Rate       = 44100
		framedSamples = 1152 // GranulesPerFrame(2) * GRANULE_SIZE(576) at 44.1 kHz
	)
	resampled := Resample(samples, SampleRate, mp3Rate)

	frames := (len(resampled) + framedSamples - 1) / framedSamples
	stereo := make([]int16, frames*framedSamples*2) // zero-padded tail = silence
	for i, s := range resampled {
		v := floatToI16(s)
		stereo[2*i], stereo[2*i+1] = v, v
	}

	var out bytes.Buffer
	if err := shinemp3.NewEncoder(mp3Rate, 2).Write(&out, stereo); err != nil {
		return nil, fmt.Errorf("mp3 encode: %w", err)
	}
	return out.Bytes(), nil
}

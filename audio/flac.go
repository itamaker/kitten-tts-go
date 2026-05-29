package audio

import (
	"bytes"
	"fmt"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

func init() {
	register("flac", func() Encoder { return flacEncoder{} })
}

// flacEncoder emits FLAC audio (mono, 24 kHz, 16-bit) using the pure-Go
// mewkiz/flac encoder.
type flacEncoder struct{}

func (flacEncoder) Name() string                       { return "flac" }
func (flacEncoder) ContentType() string                { return "audio/flac" }
func (flacEncoder) Encode(s []float32) ([]byte, error) { return encodeFLAC(s) }

func encodeFLAC(samples []float32) ([]byte, error) {
	const blockSize = 4096

	pcm := make([]int32, len(samples))
	for i, s := range samples {
		pcm[i] = int32(floatToI16(s))
	}

	info := &meta.StreamInfo{
		BlockSizeMin:  blockSize,
		BlockSizeMax:  blockSize,
		SampleRate:    SampleRate,
		NChannels:     1,
		BitsPerSample: 16,
		NSamples:      uint64(len(pcm)),
	}

	var out bytes.Buffer
	enc, err := flac.NewEncoder(&out, info)
	if err != nil {
		return nil, fmt.Errorf("flac init: %w", err)
	}

	for offset := 0; offset < len(pcm); offset += blockSize {
		end := min(offset+blockSize, len(pcm))
		block := make([]int32, end-offset)
		copy(block, pcm[offset:end])

		f := &frame.Frame{
			Header: frame.Header{
				HasFixedBlockSize: true,
				BlockSize:         uint16(len(block)),
				SampleRate:        SampleRate,
				Channels:          frame.ChannelsMono,
				BitsPerSample:     16,
			},
			Subframes: []*frame.Subframe{{
				SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
				Samples:   block,
				NSamples:  len(block),
			}},
		}
		if err := enc.WriteFrame(f); err != nil {
			return nil, fmt.Errorf("flac encode: %w", err)
		}
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("flac close: %w", err)
	}
	return out.Bytes(), nil
}

// Package audio encodes 24 kHz mono float32 samples into common audio
// containers. Formats are exposed through the [Encoder] interface and looked up
// by name with [NewEncoder], rather than via a single switch — adding a format
// means registering one implementation.
package audio

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// SampleRate is the native sample rate of the samples produced by the model.
const SampleRate = 24_000

// ErrUnsupportedFormat is returned by [NewEncoder] for an unknown format name.
var ErrUnsupportedFormat = errors.New("audio: unsupported format")

// Encoder turns mono 24 kHz float32 samples into encoded bytes for one format.
type Encoder interface {
	// Encode returns the encoded representation of samples.
	Encode(samples []float32) ([]byte, error)
	// ContentType is the HTTP Content-Type for the encoded bytes.
	ContentType() string
	// Name is the lowercase format name (e.g. "mp3").
	Name() string
}

// registry maps a format name to a constructor. Each encoder file registers
// itself here at init.
var registry = map[string]func() Encoder{}

func register(name string, ctor func() Encoder) {
	registry[name] = ctor
}

// NewEncoder returns the [Encoder] for the named format (case-insensitive).
func NewEncoder(format string) (Encoder, error) {
	if ctor, ok := registry[strings.ToLower(format)]; ok {
		return ctor(), nil
	}
	return nil, fmt.Errorf("%w %q (supported: %s)", ErrUnsupportedFormat, format, strings.Join(Formats(), ", "))
}

// Formats returns the supported format names, sorted.
func Formats() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// floatToI16 converts a float32 sample in [-1, 1] to a rounded, clamped 16-bit
// integer. NaN — e.g. from a division by zero upstream, or from feeding an
// unsanitized sample straight through — is treated as silence rather than
// left to reach the float-to-int conversion, whose result Go leaves
// implementation-defined for NaN.
func floatToI16(s float32) int16 {
	if math.IsNaN(float64(s)) {
		return 0
	}
	v := math.Round(float64(s) * 32767.0)
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	default:
		return int16(v)
	}
}

// Resample changes the sample rate using linear interpolation.
func Resample(samples []float32, srcRate, dstRate int) []float32 {
	if srcRate == dstRate || len(samples) == 0 {
		out := make([]float32, len(samples))
		copy(out, samples)
		return out
	}

	ratio := float64(dstRate) / float64(srcRate)
	outLen := int(float64(len(samples))*ratio + 0.999999)
	out := make([]float32, outLen)
	for i := range out {
		pos := float64(i) / ratio
		idx := int(pos)
		frac := float32(pos - float64(idx))
		if idx+1 < len(samples) {
			out[i] = samples[idx]*(1-frac) + samples[idx+1]*frac
		} else {
			out[i] = samples[len(samples)-1]
		}
	}
	return out
}

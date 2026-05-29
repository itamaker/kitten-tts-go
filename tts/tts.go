// Package tts is a self-contained text-to-speech engine: it phonemizes English
// text, runs ONNX inference against a KittenTTS model, and returns 24 kHz mono
// audio samples.
//
// The typical entry point is [New], which loads a model directory and returns a
// [Model]. Behaviour can be customised with [Option] values, e.g. swapping the
// [Phonemizer]:
//
//	m, err := tts.New("models/kitten-tts-nano-int8")
//	samples, err := m.Generate("Hello, world!", "Bruno", 1.0, true)
package tts

import (
	"errors"
	"fmt"
)

// Errors reported by the package. Callers can match them with [errors.Is].
var (
	// ErrUnknownVoice is returned when a requested voice cannot be resolved.
	ErrUnknownVoice = errors.New("tts: unknown voice")
	// ErrUnsupportedModel is returned for a config.json with an unrecognised type.
	ErrUnsupportedModel = errors.New("tts: unsupported model type")
)

// Phonemizer converts English text into a string of IPA phonemes.
//
// Defining this as an interface lets the engine run against espeak-ng in
// production while accepting a fake in tests, and leaves room for alternative
// phonemizers without touching [Model].
type Phonemizer interface {
	Phonemize(text string) (string, error)
}

// Model is a loaded KittenTTS model ready for inference. It is safe for use by
// a single goroutine; guard it with a mutex to share across goroutines.
type Model struct {
	net         *network // ONNX session, isolated in onnx.go
	voices      map[string]*Matrix
	speedPriors map[string]float32
	aliases     map[string]string
	phonemizer  Phonemizer
}

// Option customises a [Model] during construction.
type Option func(*options)

type options struct {
	phonemizer Phonemizer
}

// WithPhonemizer overrides the default espeak-ng phonemizer. It is primarily
// useful for testing or for plugging in an alternative phoneme source.
func WithPhonemizer(p Phonemizer) Option {
	return func(o *options) { o.phonemizer = p }
}

const (
	maxChunkLen     = 400
	trailingSilence = 5000
)

// Generate synthesizes speech for text using the named voice and speed
// multiplier, returning f32 samples at 24 kHz. When clean is true the text is
// run through [Normalize] first.
//
// Long inputs are split on sentence boundaries and synthesized chunk by chunk.
func (m *Model) Generate(text, voice string, speed float32, clean bool) ([]float32, error) {
	if clean {
		text = Normalize(text)
	}

	var out []float32
	for _, chunk := range ChunkText(text, maxChunkLen) {
		audio, err := m.GenerateChunk(chunk, voice, speed)
		if err != nil {
			return nil, err
		}
		out = append(out, audio...)
	}
	return out, nil
}

// GenerateChunk synthesizes a single chunk of text (no further splitting) and
// returns f32 samples at 24 kHz.
func (m *Model) GenerateChunk(text, voice string, speed float32) ([]float32, error) {
	internal, ok := ResolveVoiceName(voice, m.aliases)
	if !ok {
		return nil, fmt.Errorf("%w %q (available: %v)", ErrUnknownVoice, voice, VoiceNames)
	}
	if prior, ok := m.speedPriors[internal]; ok {
		speed *= prior
	}

	phonemes, err := m.phonemizer.Phonemize(text)
	if err != nil {
		return nil, err
	}
	tokens := TokenIDs(phonemes)

	style, ok := m.voices[internal]
	if !ok {
		return nil, fmt.Errorf("%w: %q missing from voice file", ErrUnknownVoice, internal)
	}
	refIdx := max(min(len(tokens), style.Rows-1), 0)

	audio, err := m.net.infer(tokens, style.Row(refIdx), style.Cols, speed)
	if err != nil {
		return nil, err
	}

	// Trim trailing silence (last 5000 samples, matching the reference model).
	return audio[:len(audio)-min(trailingSilence, len(audio))], nil
}

// Voices returns the friendly names of every built-in voice.
func (m *Model) Voices() []string { return VoiceNames }

// Close releases the underlying ONNX session.
func (m *Model) Close() error {
	if m.net != nil {
		return m.net.close()
	}
	return nil
}

// defaultPhonemizer is used when no [WithPhonemizer] option is supplied.
func defaultPhonemizer() Phonemizer { return &ESpeak{Voice: "en-us"} }

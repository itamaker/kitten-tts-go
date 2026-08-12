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
	"context"
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

// ContextPhonemizer is an optional extension of [Phonemizer] for phonemizers
// that can honor cancellation and deadlines, such as one backed by a
// subprocess. When the configured [Phonemizer] implements this,
// [Model.GenerateContext] and [Model.GenerateChunkContext] call
// PhonemizeContext instead of Phonemize. The default espeak-ng phonemizer
// ([ESpeak]) implements it.
type ContextPhonemizer interface {
	PhonemizeContext(ctx context.Context, text string) (string, error)
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
	phonemizer     Phonemizer
	intraOpThreads int
}

// WithPhonemizer overrides the default espeak-ng phonemizer. It is primarily
// useful for testing or for plugging in an alternative phoneme source.
func WithPhonemizer(p Phonemizer) Option {
	return func(o *options) { o.phonemizer = p }
}

// DefaultIntraOpThreads is the intra-op thread count [New] uses when
// [WithIntraOpThreads] isn't supplied.
//
// Measured by synthesizing the same sentence with kitten-tts-nano-int8 on a
// 6-physical/12-logical-core x86_64 Mac, two independent sweeps (median of
// 3-5 runs per count, run at different times on an otherwise-busy shared dev
// machine — not an isolated benchmark box):
//
//	threads    1      2      4      6      8     12
//	sweep 1  20.5s  15.1s  14.8s  16.3s  17.5s  34.3s
//	sweep 2  12.3s  13.1s   9.1s   7.6s   8.1s  15.0s
//
// The absolute numbers moved a lot between sweeps (ambient system load, not
// this code, since nothing else changed) — treat them as approximate, not a
// lab-grade benchmark. But the *shape* reproduced both times: 1-2 threads are
// clearly worse than the 4-8 range, and matching or exceeding the logical
// core count (12) is clearly worse again, consistent with a model this small
// having ops too fine-grained to profitably parallelize that widely. Within
// the 4-8 plateau the two sweeps disagree on the exact optimum (4 vs 6), so 4
// is picked as the conservative choice — it's solidly in the good range in
// both sweeps, and biases toward leaving more headroom on a machine (e.g. the
// server) doing other work concurrently. [WithIntraOpThreads] exists so a
// different deployment can retune this.
const DefaultIntraOpThreads = 4

// WithIntraOpThreads overrides [DefaultIntraOpThreads], the number of threads
// onnxruntime uses to parallelize work within a single graph node. A value of
// 0 uses onnxruntime's own default instead (unmeasured here; ORT's own docs
// describe it as roughly one thread per physical core, which the measurements
// behind [DefaultIntraOpThreads] suggest is too many for this model's op
// sizes — see its doc comment for the numbers).
func WithIntraOpThreads(n int) Option {
	return func(o *options) { o.intraOpThreads = n }
}

const (
	maxChunkLen     = 400
	trailingSilence = 5000
)

// MinSpeed and MaxSpeed bound the speed multiplier accepted by [Model.Generate]
// and [Model.GenerateChunk]. They are exported so every caller — the CLI, the
// server, and library users — validates against the same range instead of
// each hardcoding its own copy.
const (
	MinSpeed = 0.25
	MaxSpeed = 4.0
)

// ValidateSpeed reports an error if speed falls outside [MinSpeed, MaxSpeed].
// Generate and GenerateChunk do not call this themselves (a caller may have
// its own reason to allow a different range), but the CLI and server both do.
func ValidateSpeed(speed float32) error {
	if speed < MinSpeed || speed > MaxSpeed {
		return fmt.Errorf("%g is out of range [%g, %g]", speed, MinSpeed, MaxSpeed)
	}
	return nil
}

// Generate synthesizes speech for text using the named voice and speed
// multiplier, returning f32 samples at 24 kHz. When clean is true the text is
// run through [Normalize] first.
//
// Long inputs are split on sentence boundaries and synthesized chunk by chunk.
//
// Generate never gives up early: it runs with [context.Background]. Use
// [Model.GenerateContext] for a version that can be cancelled or bounded by a
// deadline, e.g. so a disconnected HTTP client's request stops synthesizing.
func (m *Model) Generate(text, voice string, speed float32, clean bool) ([]float32, error) {
	return m.GenerateContext(context.Background(), text, voice, speed, clean)
}

// GenerateContext is [Model.Generate] with cancellation: ctx is checked
// between chunks (so a cancelled request stops before the next chunk starts),
// and is threaded through to the phonemizer when it implements
// [ContextPhonemizer].
//
// Note this only bounds waiting between chunks and inside phonemization; a
// single chunk's ONNX inference (typically tens of milliseconds) always runs
// to completion once started.
func (m *Model) GenerateContext(ctx context.Context, text, voice string, speed float32, clean bool) ([]float32, error) {
	if clean {
		text = Normalize(text)
	}

	var out []float32
	for _, chunk := range ChunkText(text, maxChunkLen) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		audio, err := m.GenerateChunkContext(ctx, chunk, voice, speed)
		if err != nil {
			return nil, err
		}
		out = append(out, audio...)
	}
	return out, nil
}

// GenerateChunk synthesizes a single chunk of text (no further splitting) and
// returns f32 samples at 24 kHz. Like [Model.Generate], it runs with
// [context.Background]; see [Model.GenerateChunkContext] to bound it.
func (m *Model) GenerateChunk(text, voice string, speed float32) ([]float32, error) {
	return m.GenerateChunkContext(context.Background(), text, voice, speed)
}

// GenerateChunkContext is [Model.GenerateChunk] with cancellation; see
// [Model.GenerateContext].
func (m *Model) GenerateChunkContext(ctx context.Context, text, voice string, speed float32) ([]float32, error) {
	internal, ok := ResolveVoiceName(voice, m.aliases)
	if !ok {
		return nil, fmt.Errorf("%w %q (available: %v)", ErrUnknownVoice, voice, VoiceNames)
	}
	if prior, ok := m.speedPriors[internal]; ok {
		speed *= prior
	}

	phonemes, err := m.phonemize(ctx, text)
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

	return trimTrailingSilence(audio), nil
}

// phonemize calls the configured [Phonemizer], preferring PhonemizeContext
// when the phonemizer implements [ContextPhonemizer].
func (m *Model) phonemize(ctx context.Context, text string) (string, error) {
	if cp, ok := m.phonemizer.(ContextPhonemizer); ok {
		return cp.PhonemizeContext(ctx, text)
	}
	return m.phonemizer.Phonemize(text)
}

// trimTrailingSilence removes the silence the model pads after the final word.
// It may attenuate the tail of audio in place.
//
// The reference model cuts a fixed 5000 samples, but that cut lands mid-decay:
// the padding is only that long after sentence-final punctuation, and even
// there the word's fade-out extends into the cut region — comma-terminated
// chunks (short inputs, streaming splits) pad far less and lose real speech.
// So trim at most trailingSilence samples, stop a decay margin after the last
// audible sample, and fade the kept quiet tail so the ending doesn't click.
func trimTrailingSilence(audio []float32) []float32 {
	const (
		threshold = 0.02 // peak amplitude below this counts as trailing silence
		margin    = 1200 // keep ~50 ms of decay after the last audible sample
		fadeLen   = 240  // ~10 ms fade to zero at the cut to avoid a click
	)
	quiet := 0
	for quiet < len(audio) {
		s := audio[len(audio)-1-quiet]
		if s > threshold || s < -threshold {
			break
		}
		quiet++
	}
	out := audio[:len(audio)-min(trailingSilence, max(quiet-margin, 0))]

	// Fade only within the quiet tail — never soften audible speech.
	if n := min(fadeLen, quiet, len(out)); n > 0 {
		for i := 0; i < n; i++ {
			out[len(out)-1-i] *= float32(i+1) / float32(n+1)
		}
	}
	return out
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

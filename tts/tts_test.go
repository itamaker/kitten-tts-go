package tts

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNumberToWords(t *testing.T) {
	cases := map[int64]string{
		0:       "zero",
		42:      "forty-two",
		1200:    "twelve hundred",
		1000:    "one thousand",
		1000000: "one million",
	}
	for n, want := range cases {
		if got := NumberToWords(n); got != want {
			t.Errorf("NumberToWords(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	got := Normalize("It costs $42.50  today")
	want := "It costs forty-two dollars and fifty cents today"
	if got != want {
		t.Errorf("Normalize = %q, want %q", got, want)
	}
}

func TestSymbolTableSize(t *testing.T) {
	if n := len(buildSymbolTable()); n <= 100 {
		t.Errorf("symbol table too small: %d", n)
	}
}

func TestTokenIDsFraming(t *testing.T) {
	ids := TokenIDs("hello")
	if ids[0] != 0 {
		t.Errorf("expected start token 0, got %d", ids[0])
	}
	if ids[len(ids)-1] != 0 {
		t.Errorf("expected end padding 0, got %d", ids[len(ids)-1])
	}
	if ids[len(ids)-2] != 10 {
		t.Errorf("expected end token 10, got %d", ids[len(ids)-2])
	}
}

func TestEnsurePunctuation(t *testing.T) {
	if got := ensurePunctuation("hello"); got != "hello," {
		t.Errorf("got %q", got)
	}
	if got := ensurePunctuation("hello."); got != "hello." {
		t.Errorf("got %q", got)
	}
}

func TestChunkText(t *testing.T) {
	chunks := ChunkText("Hello world. This is a test.", 400)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
}

func TestResolveVoiceName(t *testing.T) {
	if v, ok := ResolveVoiceName("Bruno", nil); !ok || v != "expr-voice-3-m" {
		t.Errorf("Bruno -> %q (%v)", v, ok)
	}
	if v, ok := ResolveVoiceName("expr-voice-2-f", nil); !ok || v != "expr-voice-2-f" {
		t.Errorf("internal name -> %q (%v)", v, ok)
	}
	if _, ok := ResolveVoiceName("Nonexistent", nil); ok {
		t.Errorf("expected unknown voice to fail")
	}
}

// fakePhonemizer demonstrates that the Phonemizer interface can be substituted
// without espeak-ng installed.
type fakePhonemizer struct{ calls int }

func (f *fakePhonemizer) Phonemize(text string) (string, error) {
	f.calls++
	return strings.ToLower(text), nil
}

func TestPhonemizerInterface(t *testing.T) {
	var p Phonemizer = &fakePhonemizer{}
	out, err := p.Phonemize("HeLLo")
	if err != nil || out != "hello" {
		t.Fatalf("fake phonemizer: out=%q err=%v", out, err)
	}
	if ids := TokenIDs(out); len(ids) < 3 {
		t.Errorf("expected framed token IDs, got %v", ids)
	}
}

func TestChunkTextKeepsSentencePunctuation(t *testing.T) {
	chunks := ChunkText("Hello, world! This is a test.", 400)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2: %q", len(chunks), chunks)
	}
	// Terminal punctuation must survive (not be replaced by a comma), or the
	// model produces comma prosody and the trailing-silence trim clips the end.
	if !strings.HasSuffix(chunks[0], "!") {
		t.Errorf("chunk[0] = %q, want it to end with '!'", chunks[0])
	}
	if !strings.HasSuffix(chunks[1], ".") {
		t.Errorf("chunk[1] = %q, want it to end with '.'", chunks[1])
	}
}

func TestChunkTextAppendsCommaWhenUnpunctuated(t *testing.T) {
	// A trailing fragment with no sentence punctuation still gets a comma cue.
	chunks := ChunkText("just some words", 400)
	if len(chunks) != 1 || !strings.HasSuffix(chunks[0], ",") {
		t.Fatalf("got %q, want a single comma-terminated chunk", chunks)
	}
}

// TestIndexAfterDelimDash is a regression test for a bug in indexAfterDelim:
// pos+1 is correct for a single-byte delimiter like ", " (splits just past
// the comma), but " — " and " - " are multi-byte/padded, so pos+1 used to
// land just past the leading *space*, leaving the dash as the first
// character of the next chunk instead of the last character of this one.
func TestIndexAfterDelimDash(t *testing.T) {
	cases := []struct {
		region, delim, wantPrefix string
	}{
		{"hello — world", " — ", "hello —"}, // multi-byte delimiter
		{"hello - world", " - ", "hello -"}, // padded single-byte delimiter
		{"hello, world", ", ", "hello,"},    // unpadded: pos+1 was already correct
	}
	for _, c := range cases {
		got := indexAfterDelim(c.region, c.delim)
		if got < 0 {
			t.Fatalf("indexAfterDelim(%q, %q) = -1, want a split point", c.region, c.delim)
		}
		if prefix := c.region[:got]; prefix != c.wantPrefix {
			t.Errorf("indexAfterDelim(%q, %q): region[:%d] = %q, want %q",
				c.region, c.delim, got, prefix, c.wantPrefix)
		}
	}
}

// TestChunkTextStreamingKeepsDashWithFirstChunk is the end-to-end version of
// TestIndexAfterDelimDash: it drives the bug through ChunkTextStreaming's
// public API rather than calling the unexported helper directly.
func TestChunkTextStreamingKeepsDashWithFirstChunk(t *testing.T) {
	const intro = "Intro — "
	text := intro + strings.Repeat("word ", 100)
	chunks := ChunkTextStreaming(text, len(intro), 400)
	if len(chunks) < 2 {
		t.Fatalf("expected a split, got %d chunk(s): %q", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0], "—") {
		t.Errorf("chunks[0] = %q, want it to contain the dash", chunks[0])
	}
	if strings.HasPrefix(strings.TrimSpace(chunks[1]), "—") {
		t.Errorf("chunks[1] = %q, starts with the dash (should have ended chunks[0])", chunks[1])
	}
}

// TestValidateSpeed exercises the bounds the CLI and server both defer to, so
// they can't silently drift apart.
func TestValidateSpeed(t *testing.T) {
	for _, s := range []float32{MinSpeed, 1.0, MaxSpeed} {
		if err := ValidateSpeed(s); err != nil {
			t.Errorf("ValidateSpeed(%v) = %v, want nil", s, err)
		}
	}
	for _, s := range []float32{0, MinSpeed - 0.01, MaxSpeed + 0.01, -1} {
		if err := ValidateSpeed(s); err == nil {
			t.Errorf("ValidateSpeed(%v) = nil, want an error", s)
		}
	}
}

// slowPhonemizer implements both Phonemizer and ContextPhonemizer with an
// artificial delay, so tests can drive GenerateContext's cancellation path
// without espeak-ng or a loaded model.
type slowPhonemizer struct{ delay time.Duration }

func (p *slowPhonemizer) Phonemize(text string) (string, error) {
	time.Sleep(p.delay)
	return text, nil
}

func (p *slowPhonemizer) PhonemizeContext(ctx context.Context, text string) (string, error) {
	select {
	case <-time.After(p.delay):
		return text, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// TestGenerateChunkContextCancellation is the regression test for the
// deadlock this session's B1/B2 fix closes: a hung phonemizer used to be able
// to block a caller (and, on the server, the shared model mutex) forever.
// ResolveVoiceName and the speed-prior lookup both tolerate a Model with no
// loaded net/voices (nil aliases/speedPriors/voices resolve through the
// hardcoded voice table and empty-map reads), so GenerateChunkContext reaches
// PhonemizeContext — where cancellation is actually exercised — without
// needing a real model.
func TestGenerateChunkContextCancellation(t *testing.T) {
	m := &Model{phonemizer: &slowPhonemizer{delay: 10 * time.Second}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := m.GenerateChunkContext(ctx, "hello,", "Bruno", 1.0)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	// Generous upper bound: in a working implementation this returns within
	// the 50ms deadline. A regression back to the old unbounded Phonemize
	// call would instead block for the full 10s delay.
	if elapsed > 2*time.Second {
		t.Errorf("GenerateChunkContext took %v to return after a 50ms deadline "+
			"(want ~50ms); cancellation isn't propagating", elapsed)
	}
}

// TestGenerateChunkContextFallsBackToPlainPhonemizer confirms a Phonemizer
// that does *not* implement ContextPhonemizer still works through
// GenerateChunkContext, via the fallback to plain Phonemize.
func TestGenerateChunkContextFallsBackToPlainPhonemizer(t *testing.T) {
	m := &Model{phonemizer: &fakePhonemizer{}}
	_, err := m.GenerateChunkContext(context.Background(), "hello", "Bruno", 1.0)
	// This Model has no loaded voices, so the call fails at the voice-table
	// lookup that runs right after phonemization — reaching that error (and
	// not some phonemizer-shaped error) is exactly what proves phonemization
	// itself already succeeded via the fallback.
	if !errors.Is(err, ErrUnknownVoice) {
		t.Fatalf("err = %v, want ErrUnknownVoice (proves phonemize succeeded via the Phonemize fallback)", err)
	}
}

// npyF32 builds a minimal valid NPY v1 payload for a rows×cols float32 array.
func npyF32(rows, cols int) []byte {
	header := fmt.Sprintf("{'descr': '<f4', 'fortran_order': False, 'shape': (%d, %d), }", rows, cols)
	buf := []byte("\x93NUMPY\x01\x00")
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(header)))
	buf = append(buf, header...)
	return append(buf, make([]byte, rows*cols*4)...)
}

func TestParseNpyF32(t *testing.T) {
	m, err := parseNpyF32(npyF32(3, 2))
	if err != nil {
		t.Fatalf("valid NPY: %v", err)
	}
	if m.Rows != 3 || m.Cols != 2 || len(m.Data) != 6 {
		t.Errorf("got %dx%d (%d floats), want 3x2 (6)", m.Rows, m.Cols, len(m.Data))
	}
}

// Malformed NPY input must produce an error, never a panic or a bogus Matrix.
func TestParseNpyF32Malformed(t *testing.T) {
	valid := npyF32(2, 2)
	cases := map[string][]byte{
		"empty":            nil,
		"bad magic":        []byte("not an npy file"),
		"truncated header": valid[:12],
		"truncated data":   valid[:len(valid)-4],
		"v2 too short":     []byte("\x93NUMPY\x02\x00\xff\xff"),
		"wrong dtype": func() []byte {
			b := bytes.Replace(valid, []byte("<f4"), []byte("<f8"), 1)
			return append(b, make([]byte, 16)...) // pad to f8 size
		}(),
		"zero shape": bytes.Replace(valid, []byte("(2, 2)"), []byte("(0, 2)"), 1),
	}
	for name, data := range cases {
		if _, err := parseNpyF32(data); err == nil {
			t.Errorf("%s: expected an error, got none", name)
		}
	}
}

// synthTail builds a signal: n loud samples followed by m quiet ones.
func synthTail(loud, quiet int) []float32 {
	s := make([]float32, loud+quiet)
	for i := 0; i < loud; i++ {
		s[i] = 0.5
	}
	for i := loud; i < len(s); i++ {
		s[i] = 0.001
	}
	return s
}

func TestTrimTrailingSilence(t *testing.T) {
	cases := []struct {
		name        string
		loud, quiet int
		want        int // expected output length
	}{
		// Long padding (sentence prosody): trim capped at trailingSilence.
		{"long padding capped at 5000", 24000, 8000, 24000 + 8000 - trailingSilence},
		// Short padding (comma prosody): everything within the decay margin is
		// kept — audible samples must never be cut.
		{"padding within margin kept", 24000, 1000, 24000 + 1000},
		{"long padding keeps decay margin", 24000, 3000, 24000 + 1200},
		{"no padding trims nothing", 24000, 0, 24000},
		{"all silence", 0, 3000, 1200},
	}
	for _, c := range cases {
		got := trimTrailingSilence(synthTail(c.loud, c.quiet))
		if len(got) != c.want {
			t.Errorf("%s: got len %d, want %d", c.name, len(got), c.want)
		}
		// The last loud sample must always survive at full amplitude (the
		// fade must stay inside the quiet tail).
		if c.loud > 0 {
			if len(got) < c.loud {
				t.Errorf("%s: trimmed into audible audio (len %d < %d)", c.name, len(got), c.loud)
			} else if got[c.loud-1] != 0.5 {
				t.Errorf("%s: faded audible audio (sample = %v)", c.name, got[c.loud-1])
			}
		}
		// A kept quiet tail must fade out to avoid a click at the cut.
		if c.quiet > 0 && got[len(got)-1] >= 0.001 {
			t.Errorf("%s: tail not faded (last sample = %v)", c.name, got[len(got)-1])
		}
	}
}

// TestGenerateChunkKeepsAudibleTail is an end-to-end regression for the bug
// where the fixed 5000-sample trim chopped the end of comma-terminated speech
// ("Hello world," pads only ~400 quiet samples). Needs a local model.
func TestGenerateChunkKeepsAudibleTail(t *testing.T) {
	dir := "../models/kitten-tts-nano-int8"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no local model")
	}
	m, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	audio, err := m.GenerateChunk("Hello world,", "Bruno", 1.0)
	if err != nil {
		t.Fatal(err)
	}
	// The final 100 ms must still contain audible speech/decay; the old fixed
	// trim left a tail that was already cut mid-word.
	tail := audio[len(audio)-2400:]
	var peak float32
	for _, s := range tail {
		if s > peak {
			peak = s
		}
		if -s > peak {
			peak = -s
		}
	}
	if peak < 0.01 {
		t.Errorf("tail peak %v: end of speech appears trimmed away", peak)
	}
}

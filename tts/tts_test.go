package tts

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
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
		// Short padding (comma prosody): only the quiet tail goes, minus the
		// decay margin — audible samples must never be cut.
		{"short padding trims only silence", 24000, 1000, 24000 + 240},
		{"no padding trims nothing", 24000, 0, 24000},
		{"padding shorter than margin", 24000, 100, 24000 + 100},
		{"all silence", 0, 3000, 240},
	}
	for _, c := range cases {
		got := trimTrailingSilence(synthTail(c.loud, c.quiet))
		if len(got) != c.want {
			t.Errorf("%s: got len %d, want %d", c.name, len(got), c.want)
		}
		// The last loud sample must always survive.
		if c.loud > 0 && len(got) < c.loud {
			t.Errorf("%s: trimmed into audible audio (len %d < %d)", c.name, len(got), c.loud)
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

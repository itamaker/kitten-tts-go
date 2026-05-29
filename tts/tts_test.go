package tts

import (
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

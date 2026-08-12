package tts

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// DefaultESpeakTimeout bounds how long the espeak-ng subprocess may run when
// an [ESpeak] doesn't set Timeout. It applies even to calls made through
// [ESpeak.Phonemize] or via a context with no deadline (e.g.
// [context.Background]), so a wedged or hung espeak-ng process can never
// block a caller — or, on the server, the shared model mutex — forever.
const DefaultESpeakTimeout = 30 * time.Second

// ESpeak is a [Phonemizer] (and [ContextPhonemizer]) backed by the espeak-ng
// command-line tool. The zero value works, defaulting to the "espeak-ng"
// binary on PATH, the "en-us" voice, and [DefaultESpeakTimeout].
type ESpeak struct {
	// Voice is the espeak-ng voice (e.g. "en-us"). Empty means "en-us".
	Voice string
	// Binary is the path to espeak-ng. Empty means "espeak-ng" (found on PATH).
	Binary string
	// Timeout bounds how long the espeak-ng subprocess may run before it is
	// killed. Zero means [DefaultESpeakTimeout].
	Timeout time.Duration
}

// Phonemize converts English text to an IPA phoneme string via espeak-ng,
// bounded by [ESpeak.Timeout] (or [DefaultESpeakTimeout]).
func (e *ESpeak) Phonemize(text string) (string, error) {
	return e.PhonemizeContext(context.Background(), text)
}

// PhonemizeContext is [ESpeak.Phonemize] with cancellation. The subprocess is
// killed when ctx is done or when the timeout elapses, whichever comes first —
// so passing a context with no deadline still leaves the call bounded by the
// timeout.
func (e *ESpeak) PhonemizeContext(ctx context.Context, text string) (string, error) {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultESpeakTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin := e.Binary
	if bin == "" {
		bin = "espeak-ng"
	}
	voice := e.Voice
	if voice == "" {
		voice = "en-us"
	}

	out, err := exec.CommandContext(ctx, bin,
		"--ipa", "-q",
		"--sep=", // no separator between phonemes within a word
		"-v", voice,
		"--", // text starting with "-" must not be parsed as a flag
		text,
	).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("espeak-ng did not finish within %s: %w", timeout, ctx.Err())
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("espeak-ng failed: %s", strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("running espeak-ng (install it: brew install espeak-ng / apt install espeak-ng): %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ── Phoneme → token IDs ──

// reTokenize splits phonemes into word-vs-punctuation tokens. It mirrors the
// reference implementation's Unicode-aware `\w+|[^\w\s]` (Go's RE2 `\w` is
// ASCII-only, so the Unicode classes are spelled out).
var reTokenize = regexp.MustCompile(`[\p{L}\p{M}\p{N}_]+|[^\p{L}\p{M}\p{N}_\s]`)

// symbolIndex maps each symbol-table rune to its token ID. Built once at init
// rather than on every call.
var symbolIndex = buildSymbolIndex()

func buildSymbolIndex() map[rune]int64 {
	symbols := buildSymbolTable()
	idx := make(map[rune]int64, len(symbols))
	for i, c := range symbols {
		if _, seen := idx[c]; !seen {
			idx[c] = int64(i)
		}
	}
	return idx
}

// buildSymbolTable returns the ordered symbol table matching the reference
// TextCleaner: index 0 is the pad "$", then punctuation, ASCII letters, and IPA
// symbols. Duplicate characters keep their first index but still occupy a slot,
// so the exact byte sequence matters.
func buildSymbolTable() []rune {
	const (
		pad         = "$"
		punctuation = ";:,.!?¡¿—…\"«»\"\" "
		letters     = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		lettersIPA  = "ɑɐɒæɓʙβɔɕçɗɖðʤəɘɚɛɜɝɞɟʄɡɠɢʛɦɧħɥʜɨɪʝɭɬɫɮʟɱɯɰŋɳɲɴøɵɸθœɶʘɹɺɾɻʀʁɽʂʃʈʧʉʊʋⱱʌɣɤʍχʎʏʑʐʒʔʡʕʢǀǁǂǃˈˌːˑʼʴʰʱʲʷˠˤ˞↓↑→↗↘'̩'ᵻ"
	)
	var symbols []rune
	for _, group := range []string{pad, punctuation, letters, lettersIPA} {
		for _, c := range group {
			symbols = append(symbols, c)
		}
	}
	return symbols
}

// TokenIDs converts a phoneme string to model token IDs, framed with the
// start/end/pad tokens the model expects.
func TokenIDs(phonemes string) []int64 {
	joined := strings.Join(reTokenize.FindAllString(phonemes, -1), " ")

	ids := make([]int64, 0, len(joined)+3)
	ids = append(ids, 0) // start / pad
	for _, c := range joined {
		if id, ok := symbolIndex[c]; ok {
			ids = append(ids, id)
		}
	}
	ids = append(ids, 10, 0) // end token, pad
	return ids
}

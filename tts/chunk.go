package tts

// Text chunking: long inputs are split into model-sized pieces on sentence
// boundaries, with a streaming variant that emits a short first chunk for low
// time-to-first-audio.

import (
	"regexp"
	"strings"
)

var (
	reSentence       = regexp.MustCompile(`[.!?]+`)
	clauseDelimiters = []string{", ", "; ", ": ", " — ", " - "}
)

// ChunkText splits text into chunks no longer than maxLen bytes, breaking on
// sentence boundaries and falling back to word boundaries for over-long
// sentences.
func ChunkText(text string, maxLen int) []string {
	var chunks []string
	for _, sentence := range reSentence.Split(text, -1) {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		if len(sentence) <= maxLen {
			chunks = append(chunks, ensurePunctuation(sentence))
			continue
		}

		var b strings.Builder
		for _, word := range strings.Fields(sentence) {
			if b.Len()+len(word) < maxLen {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(word)
				continue
			}
			if b.Len() > 0 {
				chunks = append(chunks, ensurePunctuation(b.String()))
				b.Reset()
			}
			b.WriteString(word)
		}
		if b.Len() > 0 {
			chunks = append(chunks, ensurePunctuation(b.String()))
		}
	}
	return chunks
}

// ChunkTextStreaming splits text for streaming synthesis: the first chunk ends
// at the earliest clause or sentence boundary within firstMax bytes (for fast
// initial audio), and the remainder is chunked normally with restMax.
func ChunkTextStreaming(text string, firstMax, restMax int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= firstMax {
		return []string{ensurePunctuation(text)}
	}

	region := text[:min(firstMax, len(text))]
	split := -1
	consider := func(pos int) {
		if pos >= 0 && (split < 0 || pos < split) {
			split = pos
		}
	}
	for _, delim := range clauseDelimiters {
		consider(indexAfterDelim(region, delim))
	}
	if loc := reSentence.FindStringIndex(region); loc != nil {
		consider(loc[1])
	}

	if split < 0 {
		return splitOnWord(text, firstMax, restMax)
	}

	chunks := []string{ensurePunctuation(text[:split])}
	if rest := strings.TrimSpace(text[split:]); rest != "" {
		chunks = append(chunks, ChunkText(rest, restMax)...)
	}
	return chunks
}

// indexAfterDelim returns the split position just past a delimiter's punctuation
// character, or -1 if the delimiter is absent.
func indexAfterDelim(region, delim string) int {
	if pos := strings.Index(region, delim); pos >= 0 {
		return pos + 1
	}
	return -1
}

// splitOnWord is the fallback when no clause/sentence boundary is found within
// firstMax: it breaks at the last whole word that fits.
func splitOnWord(text string, firstMax, restMax int) []string {
	words := strings.Fields(text)
	length, count := 0, 0
	for i, w := range words {
		sep := 0
		if i > 0 {
			sep = 1
		}
		if length+len(w)+sep > firstMax {
			break
		}
		length += len(w) + sep
		count = i + 1
	}
	if count == 0 {
		count = 1
	}

	chunks := []string{ensurePunctuation(strings.Join(words[:count], " "))}
	if rest := strings.Join(words[count:], " "); rest != "" {
		chunks = append(chunks, ChunkText(rest, restMax)...)
	}
	return chunks
}

// ensurePunctuation appends a comma when text lacks trailing punctuation, which
// the model uses as a phrasing cue.
func ensurePunctuation(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	r := []rune(text)
	if strings.ContainsRune(".!?,;:", r[len(r)-1]) {
		return text
	}
	return text + ","
}

package audio

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// TestOggCRCMatchesSlowReference cross-checks the table-driven oggCRC against
// the direct bit-by-bit definition it's built from (oggCRCSlow). This matters
// because a bug in the table transform could still produce a value that's
// merely self-consistent (the encoder always both writes and effectively
// "believes" whatever oggCRC returns) rather than one that's wrong relative
// to the standard algorithm every real decoder implements.
func TestOggCRCMatchesSlowReference(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		{0x00},
		{0xFF},
		[]byte("OggS"),
		make([]byte, 27), // a zeroed page header's worth
	}
	for _, c := range cases {
		if got, want := oggCRC(c), oggCRCSlow(c); got != want {
			t.Errorf("oggCRC(%v) = %#x, want %#x (oggCRCSlow)", c, got, want)
		}
	}

	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		buf := make([]byte, r.Intn(600))
		r.Read(buf)
		if got, want := oggCRC(buf), oggCRCSlow(buf); got != want {
			t.Fatalf("random case len=%d: oggCRC = %#x, want %#x (oggCRCSlow)", len(buf), got, want)
		}
	}
}

// TestEncodeOpusPreSkip is a regression test for a hardcoded pre-skip of 0:
// without it, a decoder presents libopus's encoder lookahead as real audio,
// so playback starts audibly late and isn't sample-aligned with the other
// formats.
func TestEncodeOpusPreSkip(t *testing.T) {
	samples := make([]float32, SampleRate) // 1s
	for i := range samples {
		samples[i] = 0.1
	}
	data, err := encodeOpus(samples)
	if err != nil {
		t.Fatalf("encodeOpus: %v", err)
	}

	idx := bytes.Index(data, []byte("OpusHead"))
	if idx < 0 {
		t.Fatalf("OpusHead magic not found in encoded output")
	}
	// RFC 7845 §5.1: 8-byte magic, 1-byte version, 1-byte channel count, then
	// a little-endian uint16 pre-skip.
	preSkipOff := idx + 10
	if len(data) < preSkipOff+2 {
		t.Fatalf("encoded output truncated before the pre-skip field")
	}
	if got := binary.LittleEndian.Uint16(data[preSkipOff : preSkipOff+2]); got != 312 {
		t.Errorf("OpusHead pre-skip = %d, want 312", got)
	}
}

// TestEncodeOpusFinalGranuleIncludesPreSkip checks the other half of the B5
// fix: granule position is a decoder-output sample count that includes
// pre-skip, so the last page's granule must be preSkip + the real (resampled)
// sample count, not just the real sample count.
func TestEncodeOpusFinalGranuleIncludesPreSkip(t *testing.T) {
	const n = 5000 // arbitrary, not a multiple of the 960-sample frame size
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = 0.2
	}
	data, err := encodeOpus(samples)
	if err != nil {
		t.Fatalf("encodeOpus: %v", err)
	}

	lastOggS := bytes.LastIndex(data, []byte("OggS"))
	if lastOggS < 0 {
		t.Fatalf("no OggS page found in encoded output")
	}
	// Ogg page header: 4-byte magic, 1 version, 1 type, then an 8-byte
	// little-endian granule position.
	granule := int64(binary.LittleEndian.Uint64(data[lastOggS+6 : lastOggS+14]))

	wantReal := int64(len(Resample(samples, SampleRate, 48000)))
	wantGranule := int64(312) + wantReal
	if granule != wantGranule {
		t.Errorf("final granule = %d, want %d (312 pre-skip + %d real samples)", granule, wantGranule, wantReal)
	}
}

package audio

import (
	"math"
	"testing"
)

// TestFloatToI16NaN is a regression test: NaN fails both of floatToI16's
// clamp comparisons (NaN > x and NaN < x are always false), so it used to
// fall through to int16(NaN), whose result Go leaves implementation-defined.
// NaN should become silence instead.
func TestFloatToI16NaN(t *testing.T) {
	if got := floatToI16(float32(math.NaN())); got != 0 {
		t.Errorf("floatToI16(NaN) = %d, want 0", got)
	}
}

func TestFloatToI16Clamps(t *testing.T) {
	cases := []struct {
		in   float32
		want int16
	}{
		{0, 0},
		{1.0, 32767},
		{-1.0, -32767},
		{2.0, 32767},   // clamp above range
		{-2.0, -32768}, // clamp below range
	}
	for _, c := range cases {
		if got := floatToI16(c.in); got != c.want {
			t.Errorf("floatToI16(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestFloatToI16RoundsToNearest is a regression test for plain truncation:
// int16(100.6) truncates to 100, discarding a fraction that should round up.
func TestFloatToI16RoundsToNearest(t *testing.T) {
	s := float32(100.6) / 32767.0 // s*32767 ≈ 100.6
	if got, want := floatToI16(s), int16(101); got != want {
		t.Errorf("floatToI16(%v) = %d, want %d (round-to-nearest, not truncation)", s, got, want)
	}
}

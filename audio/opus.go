// Opus encoding (OGG container) via libopus.
//
// This requires cgo and libopus installed on the system at build time:
//
//	# macOS
//	brew install opus pkg-config
//	# Debian/Ubuntu
//	sudo apt-get install -y libopus-dev pkg-config
package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"

	opus "gopkg.in/hraban/opus.v2"
)

func init() {
	register("opus", func() Encoder { return opusEncoder{} })
}

// opusEncoder emits OGG Opus audio using libopus (cgo) and a hand-written OGG
// container.
type opusEncoder struct{}

func (opusEncoder) Name() string                       { return "opus" }
func (opusEncoder) ContentType() string                { return "audio/ogg" }
func (opusEncoder) Encode(s []float32) ([]byte, error) { return encodeOpus(s) }

// encodeOpus encodes f32 samples as OGG Opus (48 kHz mono): resample to 48 kHz,
// 20 ms (960-sample) frames, VoIP mode, with OpusHead + OpusTags headers per
// RFC 7845.
func encodeOpus(samples []float32) ([]byte, error) {
	const (
		opusRate  = 48000
		frameSize = 960 // 20 ms at 48 kHz
		serial    = uint32(1)
		// preSkip is libopus's typical total algorithmic delay at 48 kHz for
		// the encoder settings used here. hraban/opus does not expose
		// OPUS_GET_LOOKAHEAD to read the encoder's actual value, so this is
		// hardcoded rather than queried. Without it (pre-skip 0), a decoder
		// presents the encoder's priming delay as real audio, so playback
		// starts ~6.5 ms late and is not sample-aligned with the other
		// formats (RFC 7845 §4.2).
		preSkip = 312
	)

	samples48k := Resample(samples, SampleRate, opusRate)

	enc, err := opus.NewEncoder(opusRate, 1, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("opus encoder init error: %w", err)
	}

	var buf bytes.Buffer
	seq := uint32(0)

	// OpusHead identification header (RFC 7845)
	head := make([]byte, 0, 19)
	head = append(head, []byte("OpusHead")...)
	head = append(head, 1)                                  // version
	head = append(head, 1)                                  // channel count (mono)
	head = binary.LittleEndian.AppendUint16(head, preSkip)  // pre-skip
	head = binary.LittleEndian.AppendUint32(head, opusRate) // input sample rate
	head = binary.LittleEndian.AppendUint16(head, 0)        // output gain
	head = append(head, 0)                                  // channel mapping family 0
	writeOggPage(&buf, 0x02, 0, serial, seq, head)          // BOS
	seq++

	// OpusTags comment header
	vendor := []byte("kitten-tts-go")
	tags := make([]byte, 0, 8+4+len(vendor)+4)
	tags = append(tags, []byte("OpusTags")...)
	tags = binary.LittleEndian.AppendUint32(tags, uint32(len(vendor)))
	tags = append(tags, vendor...)
	tags = binary.LittleEndian.AppendUint32(tags, 0) // user comment count
	writeOggPage(&buf, 0x00, 0, serial, seq, tags)
	seq++

	// Encode audio in 20 ms frames, batching opus packets into OGG pages.
	opusOut := make([]byte, 4000)
	var fed int64 // cumulative real (non-padding) 48 kHz samples fed, capped at len(samples48k)
	var pageSegs []byte
	var pageBody []byte
	var pageGranule int64

	flushPage := func(headerType byte) {
		writeOggPageRaw(&buf, headerType, pageGranule, serial, seq, pageSegs, pageBody)
		seq++
		pageSegs = pageSegs[:0]
		pageBody = pageBody[:0]
	}

	nFrames := (len(samples48k) + frameSize - 1) / frameSize
	for i := 0; i < nFrames; i++ {
		start := i * frameSize
		end := start + frameSize
		var frame []float32
		if end > len(samples48k) {
			frame = make([]float32, frameSize)
			copy(frame, samples48k[start:])
		} else {
			frame = samples48k[start:end]
		}

		n, err := enc.EncodeFloat32(frame, opusOut)
		if err != nil {
			return nil, fmt.Errorf("opus encode error: %w", err)
		}
		packet := opusOut[:n]
		// The final frame is zero-padded to frameSize; cap at the real sample
		// count so players trim the padding (RFC 7845 §4.5 end trimming).
		fed = min(fed+frameSize, int64(len(samples48k)))
		// Granule position is a decoder-output sample count from the very
		// start of the stream, before pre-skip discarding — so it always
		// includes preSkip, even though a compliant player then discards
		// that many samples from the front (RFC 7845 §4.5).
		granule := preSkip + fed

		segs := lacingValues(len(packet))
		// A page holds at most 255 segments; flush before overflowing.
		if len(pageSegs) > 0 && len(pageSegs)+len(segs) > 255 {
			flushPage(0x00)
		}
		pageSegs = append(pageSegs, segs...)
		pageBody = append(pageBody, packet...)
		pageGranule = granule
	}
	flushPage(0x04) // EOS

	return buf.Bytes(), nil
}

// lacingValues returns the OGG segment-table lacing values for a packet of the
// given byte length.
func lacingValues(n int) []byte {
	segs := make([]byte, 0, n/255+1)
	for n >= 255 {
		segs = append(segs, 255)
		n -= 255
	}
	segs = append(segs, byte(n))
	return segs
}

// writeOggPage writes a single OGG page containing exactly one packet.
func writeOggPage(buf *bytes.Buffer, headerType byte, granule int64, serial, seq uint32, packet []byte) {
	writeOggPageRaw(buf, headerType, granule, serial, seq, lacingValues(len(packet)), packet)
}

// writeOggPageRaw writes an OGG page given a precomputed segment table and the
// concatenated packet bodies for that page, computing and patching in its CRC.
//
// The page is written straight into buf (rather than assembled in a separate
// header buffer and appended) so framing an N-page stream costs one copy of
// each page's bytes into buf, not two.
func writeOggPageRaw(buf *bytes.Buffer, headerType byte, granule int64, serial, seq uint32, segTable, body []byte) {
	pageStart := buf.Len()

	buf.WriteString("OggS")
	buf.WriteByte(0) // stream structure version
	buf.WriteByte(headerType)
	binary.Write(buf, binary.LittleEndian, granule)
	binary.Write(buf, binary.LittleEndian, serial)
	binary.Write(buf, binary.LittleEndian, seq)
	binary.Write(buf, binary.LittleEndian, uint32(0)) // CRC placeholder, patched below
	buf.WriteByte(byte(len(segTable)))
	buf.Write(segTable)
	buf.Write(body)

	// CRC covers the whole page with the CRC field zeroed (as just written
	// above); page is a view into buf's own backing array, so this reads what
	// was just written and then patches it in place — no separate assembly.
	page := buf.Bytes()[pageStart:]
	crc := oggCRC(page)
	binary.LittleEndian.PutUint32(page[22:26], crc)
}

// oggCRCTable is oggCRCSlow's per-byte result table: processing byte i as the
// top byte of an otherwise-zero register through the same 8 shift/xor steps.
// Precomputing it once turns oggCRC's per-byte cost from 8 conditional
// shifts into one table lookup, which matters here because it runs over
// every byte of every OGG page written.
var oggCRCTable = func() (t [256]uint32) {
	for i := range t {
		t[i] = oggCRCSlow([]byte{byte(i)})
	}
	return t
}()

// oggCRC computes the OGG page CRC: CRC-32 with polynomial 0x04c11db7,
// initial value 0, no input/output reflection, no final XOR. Equivalent to
// (and cross-checked in tests against) the direct bit-by-bit definition in
// oggCRCSlow, using the standard table-driven transform of that recurrence.
func oggCRC(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ oggCRCTable[byte(crc>>24)^b]
	}
	return crc
}

// oggCRCSlow is the direct, unoptimized definition of the OGG page CRC (see
// oggCRC): 8 conditional shift-xors per byte, no table. Kept only as the
// reference oggCRCTable is built from and is cross-checked against in tests.
func oggCRCSlow(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04c11db7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// Voice loading from NPZ files.
//
// KittenTTS voices are stored as NumPy .npz archives. Each voice is a 2D
// float32 array (style embeddings indexed by text length).
package tts

import (
	"archive/zip"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// VoiceAlias maps a friendly voice name to its internal key.
type VoiceAlias struct {
	Friendly string
	Internal string
}

// VoiceAliases maps friendly → internal voice names.
var VoiceAliases = []VoiceAlias{
	{"Bella", "expr-voice-2-f"},
	{"Jasper", "expr-voice-2-m"},
	{"Luna", "expr-voice-3-f"},
	{"Bruno", "expr-voice-3-m"},
	{"Rosie", "expr-voice-4-f"},
	{"Hugo", "expr-voice-4-m"},
	{"Kiki", "expr-voice-5-f"},
	{"Leo", "expr-voice-5-m"},
}

// VoiceNames lists all available friendly voice names.
var VoiceNames = []string{
	"Bella", "Jasper", "Luna", "Bruno", "Rosie", "Hugo", "Kiki", "Leo",
}

var internalVoiceNames = map[string]bool{
	"expr-voice-2-m": true,
	"expr-voice-2-f": true,
	"expr-voice-3-m": true,
	"expr-voice-3-f": true,
	"expr-voice-4-m": true,
	"expr-voice-4-f": true,
	"expr-voice-5-m": true,
	"expr-voice-5-f": true,
}

// Matrix is a 2D row-major float32 array.
type Matrix struct {
	Rows int
	Cols int
	Data []float32
}

// Row returns a copy of the i-th row.
func (m *Matrix) Row(i int) []float32 {
	start := i * m.Cols
	row := make([]float32, m.Cols)
	copy(row, m.Data[start:start+m.Cols])
	return row
}

// ResolveVoiceName resolves a voice name (friendly or internal) to the
// internal key, consulting config aliases first.
func ResolveVoiceName(name string, configAliases map[string]string) (string, bool) {
	if internal, ok := configAliases[name]; ok {
		return internal, true
	}
	for _, a := range VoiceAliases {
		if strings.EqualFold(a.Friendly, name) {
			return a.Internal, true
		}
	}
	if internalVoiceNames[name] {
		return name, true
	}
	return "", false
}

// LoadVoices loads voices from an NPZ file (a ZIP archive of .npy files).
// Returns a map of voice_key → Matrix.
func LoadVoices(path string) (map[string]*Matrix, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read NPZ as ZIP: %w", err)
	}
	defer r.Close()

	voices := make(map[string]*Matrix)
	for _, f := range r.File {
		name := strings.TrimSuffix(f.Name, ".npy")

		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		buf, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}

		m, err := parseNpyF32(buf)
		if err != nil {
			return nil, fmt.Errorf("failed to parse NPY for voice %q: %w", name, err)
		}
		voices[name] = m
	}
	return voices, nil
}

var reShape = regexp.MustCompile(`'shape':\s*\((\d+),\s*(\d+)\)`)

// parseNpyF32 is a minimal NPY parser for 2D float32 arrays (little-endian).
func parseNpyF32(data []byte) (*Matrix, error) {
	// NPY format: magic "\x93NUMPY", major, minor, header_len, header, data
	if len(data) < 10 {
		return nil, fmt.Errorf("NPY data too short")
	}
	if string(data[:6]) != "\x93NUMPY" {
		return nil, fmt.Errorf("invalid NPY magic")
	}

	major := data[6]
	var headerLen, headerStart int
	if major == 1 {
		headerLen = int(binary.LittleEndian.Uint16(data[8:10]))
		headerStart = 10
	} else {
		headerLen = int(binary.LittleEndian.Uint32(data[8:12]))
		headerStart = 12
	}

	header := string(data[headerStart : headerStart+headerLen])

	caps := reShape.FindStringSubmatch(header)
	if caps == nil {
		return nil, fmt.Errorf("could not parse shape from NPY header")
	}
	rows, _ := strconv.Atoi(caps[1])
	cols, _ := strconv.Atoi(caps[2])

	dataStart := headerStart + headerLen
	expectedBytes := rows * cols * 4
	if len(data) < dataStart+expectedBytes {
		return nil, fmt.Errorf("NPY data truncated")
	}

	floats := make([]float32, rows*cols)
	for i := 0; i < rows*cols; i++ {
		off := dataStart + i*4
		bits := binary.LittleEndian.Uint32(data[off : off+4])
		floats[i] = math.Float32frombits(bits)
	}

	return &Matrix{Rows: rows, Cols: cols, Data: floats}, nil
}

# kitten-tts-go 🐱🐹

[![Open In Colab](https://colab.research.google.com/assets/colab-badge.svg)](https://colab.research.google.com/github/itamaker/kitten-tts-go/blob/main/examples/kitten-tts-go-colab.ipynb)

Go implementation of [KittenTTS](https://github.com/KittenML/KittenTTS) — an ultra-lightweight ONNX-based text-to-speech engine. Self-contained binaries with no Python dependency.

> **Try it now:** the [Colab notebook](examples/kitten-tts-go-colab.ipynb) builds the project and synthesizes speech in three clicks — no local setup, no GPU.

It produces two binaries:

* `kitten-tts` — a CLI tool for one-off speech generation. Ideally suited for AI agent skills.
* `kitten-tts-server` — an OpenAI-compatible API server with [SSE streaming](#sse-streaming) support.

> **Adapted from:** [KittenML/KittenTTS](https://github.com/KittenML/KittenTTS) (Apache-2.0). All model weights are from the original project.

## Key Features

- **Ultra-lightweight** — 15M to 80M parameter models; smallest is just 25 MB (int8)
- **CPU-optimized** — ONNX-based inference runs efficiently without a GPU
- **8 built-in voices** — Bella, Jasper, Luna, Bruno, Rosie, Hugo, Kiki, and Leo
- **Adjustable speech speed** — control playback rate via `-speed`
- **Text normalization** — built-in pipeline handles numbers and currencies
- **24 kHz output** — high-quality audio at a standard sample rate
- **Multiple audio formats** — MP3, FLAC, WAV, and PCM (pure Go) plus OGG Opus (via libopus)

## Dependencies

This port relies on three system dependencies:

### 1. ONNX Runtime shared library

Go inference uses [`yalue/onnxruntime_go`](https://github.com/yalue/onnxruntime_go), which loads the ONNX Runtime shared library dynamically at runtime.

```bash
# macOS
brew install onnxruntime

# Ubuntu/Debian — download a release from
# https://github.com/microsoft/onnxruntime/releases and place
# libonnxruntime.so on your library path
```

The library is auto-detected at common locations (`/usr/local/lib`, `/opt/homebrew/lib`, `/usr/lib`, …). To point at a specific file, set the `ONNXRUNTIME_LIB_PATH` environment variable:

```bash
export ONNXRUNTIME_LIB_PATH=/path/to/libonnxruntime.dylib
```

### 2. espeak-ng (for phonemization)

```bash
# macOS
brew install espeak-ng

# Ubuntu/Debian
sudo apt-get install -y espeak-ng
```

### 3. libopus + libopusfile (for Opus encoding)

Opus output is encoded with libopus via cgo. The [`hraban/opus`](https://gopkg.in/hraban/opus.v2) binding links both **libopus** and **libopusfile** (`#cgo pkg-config: opus opusfile`), so both are **build-time** dependencies (and must be on the library path at runtime). Building therefore requires a C compiler and `CGO_ENABLED=1` (the default for native builds).

```bash
# macOS
brew install opus opusfile pkg-config

# Ubuntu/Debian
sudo apt-get install -y libopus-dev libopusfile-dev pkg-config
```

> The libopus/libopusfile/pkg-config packages above are only needed when
> **building from source**. The **released binaries statically link** libopus and
> libopusfile, so the target machine needs only the ONNX Runtime shared library
> (`dlopen`'d at runtime) and espeak-ng — no opus libraries required.

## Available Models

| Model | Parameters | Size | Download |
|---|---|---|---|
| kitten-tts-mini | 80M | 80 MB | [KittenML/kitten-tts-mini-0.8](https://huggingface.co/KittenML/kitten-tts-mini-0.8) |
| kitten-tts-micro | 40M | 41 MB | [KittenML/kitten-tts-micro-0.8](https://huggingface.co/KittenML/kitten-tts-micro-0.8) |
| kitten-tts-nano | 15M | 56 MB | [KittenML/kitten-tts-nano-0.8](https://huggingface.co/KittenML/kitten-tts-nano-0.8-fp32) |
| kitten-tts-nano (int8) | 15M | 25 MB | [KittenML/kitten-tts-nano-0.8-int8](https://huggingface.co/KittenML/kitten-tts-nano-0.8-int8) |

### Downloading a model

Models are not vendored in this repository. Fetch one into `./models` with the
helper script:

```bash
scripts/fetch_model.sh nano-int8   # also: nano, micro, mini (default: nano-int8)
```

It reads the ONNX/voices filenames from the model's `config.json`, so it works
for every model above. (`./models` is git-ignored.) Equivalent manual download:

```bash
mkdir -p models/kitten-tts-nano-int8
for FILE in config.json kitten_tts_nano_v0_8.onnx voices.npz; do
  curl -L -o "models/kitten-tts-nano-int8/$FILE" \
    "https://huggingface.co/KittenML/kitten-tts-nano-0.8-int8/resolve/main/$FILE"
done
```

## Build

```bash
go build -o bin/ ./...
# Binaries at: bin/kitten-tts and bin/kitten-tts-server
```

> Building requires a C compiler and libopus/libopusfile (see [Dependencies](#dependencies)),
> since Opus encoding is compiled in via cgo.

### Releases

Pushing a `v*` tag triggers [`.github/workflows/release.yml`](.github/workflows/release.yml),
which builds binaries on native runners (Linux amd64/arm64, macOS amd64/arm64) with
[GoReleaser](https://goreleaser.com) and publishes a GitHub Release with one
`.tar.gz` per platform plus `checksums.txt`:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Because the project links libopus via cgo, each platform is built **natively**
(`goreleaser build --single-target`) rather than cross-compiled. You can dry-run the
build locally with:

```bash
goreleaser build --single-target --snapshot --clean
```

### End-to-end smoke test

[`scripts/smoke_test.sh`](scripts/smoke_test.sh) builds the binaries and exercises
every audio format plus SSE streaming, fully offline, against a local model:

```bash
# Pass a model dir (or set KITTEN_MODEL_DIR, or place one at models/kitten-tts-nano-int8)
scripts/smoke_test.sh /path/to/models/kitten-tts-nano-int8

# Test prebuilt/release binaries instead of building:
KITTEN_BIN_DIR=dist/... scripts/smoke_test.sh /path/to/model
```

It checks the CLI (wav/mp3/flac/opus/pcm + `-list-voices`) and the server
(`/health`, `/v1/models`, all formats, streaming, and 400 validation), printing a
pass/fail summary and exiting non-zero on failure. Needs espeak-ng and the ONNX
Runtime library; uses `ffprobe` for codec checks when available.

## Generate Speech (CLI)

Following Go convention, **flags come before the positional arguments**
(`<model_dir> <text> [voice]`):

```bash
# Basic usage (outputs output.wav)
./bin/kitten-tts ./models/kitten-tts-nano-int8 'Hello, world!' Bruno

# Specify voice, speed, and output (flags first)
./bin/kitten-tts -voice Luna -speed 1.2 -output hello.wav ./models/kitten-tts-nano-int8 'Hello, world!'

# Encode directly to another format with -format
./bin/kitten-tts -format mp3 -output hello.mp3 ./models/kitten-tts-nano-int8 'Hello, world!'

# List available voices
./bin/kitten-tts -list-voices ./models/kitten-tts-nano-int8
```

CLI flags (single or double dash both work):

| Flag | Default | Description |
|---|---|---|
| `-voice`, `-v` | `Bruno` | Voice name (overrides the positional voice) |
| `-speed`, `-s` | `1.0` | Speech speed multiplier |
| `-output`, `-o` | `output.wav` | Output file path |
| `-format` | `wav` | Output format: `wav`, `mp3`, `flac`, `opus`, `pcm` |
| `-no-clean` | | Disable text normalization (numbers, currency) |
| `-list-voices` | | List available voices and exit |

> Because the CLI uses Go's standard `flag` package, any flag placed *after* a
> positional argument is treated as a positional. Put flags first.

## Run the API Server

Flags first, then the model directory:

```bash
./bin/kitten-tts-server -host 0.0.0.0 -port 8080 ./models/kitten-tts-nano-int8
```

The server exposes an OpenAI-compatible `/v1/audio/speech` endpoint:

```bash
curl -X POST http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "kitten-tts",
    "input": "Hello, world! This is KittenTTS running as an API server.",
    "voice": "alloy"
  }' \
  --output speech.mp3
```

**Request body:**

| Field | Type | Default | Description |
|---|---|---|---|
| `input` | string | *(required)* | Text to synthesize |
| `voice` | string | *(required)* | Voice name (OpenAI or KittenTTS names) |
| `model` | string | `""` | Accepted for compatibility; ignored |
| `response_format` | string | `"mp3"` | Output audio format (see below) |
| `speed` | float | `1.0` | Speech speed multiplier (0.25–4.0) |
| `stream` | bool | `false` | Enable SSE streaming (requires `"pcm"` format) |

**Supported audio formats:**

| Format | Content-Type | Description |
|---|---|---|
| `mp3` | `audio/mpeg` | MP3 (resampled to 44.1 kHz, pure-Go [shine](https://github.com/braheezy/shine-mp3) encoder) |
| `flac` | `audio/flac` | FLAC lossless (24 kHz native, pure-Go [mewkiz/flac](https://github.com/mewkiz/flac)) |
| `wav` | `audio/wav` | WAV 16-bit PCM (24 kHz native) |
| `pcm` | `audio/pcm` | Raw 16-bit signed little-endian PCM (24 kHz) |
| `opus` | `audio/ogg` | Opus in OGG container (resampled to 48 kHz) |
| `aac` | — | Not supported (returns error) |

> **Note on Opus:** there is no pure-Go Opus encoder, so Opus uses [libopus](https://opus-codec.org/)
> via cgo ([`hraban/opus`](https://gopkg.in/hraban/opus.v2)) plus a hand-written RFC 7845 OGG
> writer. libopus and libopusfile must be installed at build time (see [Dependencies](#dependencies)).

**API endpoints:**

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/audio/speech` | Generate speech from text |
| `GET` | `/v1/models` | List loaded model |
| `GET` | `/health` | Health check |

**Voice mapping (OpenAI → KittenTTS):**

| OpenAI | KittenTTS | Gender |
|---|---|---|
| alloy | Bella | Female |
| echo | Jasper | Male |
| fable | Luna | Female |
| onyx | Bruno | Male |
| nova | Rosie | Female |
| shimmer | Hugo | Male |

All 8 KittenTTS voices (Bella, Jasper, Luna, Bruno, Rosie, Hugo, Kiki, Leo) can also be used directly by name.

### SSE Streaming

For lower time-to-first-audio on longer texts, set `"stream": true` with `"response_format": "pcm"`. The server returns [Server-Sent Events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events) with base64-encoded PCM audio chunks, compatible with the OpenAI streaming TTS format:

```bash
curl -N -X POST http://localhost:8080/v1/audio/speech \
  -H "Content-Type: application/json" \
  -d '{
    "model": "kitten-tts",
    "input": "Hello, this is a streaming test. Each sentence is sent as a separate audio chunk.",
    "voice": "alloy",
    "response_format": "pcm",
    "stream": true
  }'
```

Each event is a JSON object on a `data:` line:

```
data: {"type":"speech.audio.delta","delta":"<base64-encoded-pcm>"}
data: {"type":"speech.audio.delta","delta":"<base64-encoded-pcm>"}
data: {"type":"speech.audio.done"}
```

The `delta` field contains 16-bit signed little-endian PCM at 24 kHz, base64-encoded. The first chunk is split at the earliest clause boundary for fast initial playback.

## Use as a Library

The `tts` package is a self-contained engine. `New` returns a `*tts.Model`;
encoders live in the `audio` package behind a small interface.

```go
package main

import (
	"os"

	"github.com/itamaker/kitten-tts-go/audio"
	"github.com/itamaker/kitten-tts-go/tts"
)

func main() {
	model, err := tts.New("models/kitten-tts-nano-int8")
	if err != nil {
		panic(err)
	}
	defer model.Close()

	samples, err := model.Generate("Hello, world!", "Bruno", 1.0, true)
	if err != nil {
		panic(err)
	}

	enc, _ := audio.NewEncoder("mp3") // or wav, flac, opus, pcm
	data, _ := enc.Encode(samples)
	os.WriteFile("hello.mp3", data, 0o644)
}
```

The phonemizer is an interface, so you can swap espeak-ng for your own (handy in
tests) via a functional option:

```go
model, err := tts.New(dir, tts.WithPhonemizer(myPhonemizer))
// myPhonemizer implements tts.Phonemizer: Phonemize(string) (string, error)
```

## Architecture

```
kitten-tts-go/
├── tts/                 # Core TTS engine (importable library)
│   ├── tts.go           # Model type, Generate/GenerateChunk, options
│   ├── load.go          # New: read config.json and load a model directory
│   ├── onnx.go          # ONNX session, isolated behind one file
│   ├── phonemes.go      # Phonemizer interface, espeak-ng impl, token IDs
│   ├── normalize.go     # Number/currency/whitespace normalization
│   ├── chunk.go         # Sentence/streaming text chunking
│   └── voices.go        # NPZ voice embedding loader
├── audio/               # Encoder interface + one file per format
│   ├── audio.go         # Encoder interface, registry, NewEncoder, resampling
│   ├── wav.go           # WAV + PCM
│   ├── mp3.go           # MP3 (shine)
│   ├── flac.go          # FLAC (mewkiz)
│   └── opus.go          # OGG Opus (libopus) + hand-written OGG container
└── cmd/
    ├── kitten-tts/        # CLI (stdlib flag)
    └── kitten-tts-server/ # OpenAI-compatible API server (net/http)
```

### How It Works

1. **Normalization** — Expands numbers ("42" → "forty-two"), currencies ("$10.50" → "ten dollars and fifty cents"), and collapses whitespace
2. **Phonemization** — Converts English text to IPA phonemes via a `Phonemizer` (espeak-ng by default)
3. **Token encoding** — Maps IPA phonemes to integer token IDs using a symbol table matching the original Python implementation
4. **Voice selection** — Loads style embeddings from the NPZ voice file
5. **ONNX inference** — Runs the model with input tokens, voice style, and speed parameters
6. **Audio encoding** — An `audio.Encoder` produces MP3 (default), FLAC, WAV, Opus, or raw PCM

## Design Notes

A few idiomatic-Go choices worth knowing:

- **`tts.Model`** is the core type, constructed with **`tts.New`**.
- **Audio formats** are an **`Encoder` interface** resolved by name from a registry (`audio.NewEncoder`), one file per format — not a single switch statement.
- **Phonemization** is the **`Phonemizer` interface**; the default is espeak-ng and `tts.WithPhonemizer` lets you swap it (e.g. in tests).
- The **CLI** uses the standard-library `flag` package (flags before positionals); the **server** is plain `net/http`.
- **ONNX inference** uses [`yalue/onnxruntime_go`](https://github.com/yalue/onnxruntime_go), which `dlopen`s the runtime — there is no cgo link to onnxruntime.
- **Encoders**: MP3 ([`shine-mp3`](https://github.com/braheezy/shine-mp3)) and FLAC ([`mewkiz/flac`](https://github.com/mewkiz/flac)) are pure Go; Opus uses [`hraban/opus`](https://gopkg.in/hraban/opus.v2) (libopus via cgo). AAC is not supported.

## License

MIT — see [LICENSE](LICENSE).

This is an independent Go implementation. The upstream KittenTTS project and its
model weights are Apache-2.0 (see [Acknowledgments](#acknowledgments)); those
weights are downloaded separately and are not part of this repository.

## Acknowledgments

- [KittenML](https://kittenml.com) for the original KittenTTS models and Python library
- [yalue/onnxruntime_go](https://github.com/yalue/onnxruntime_go) for the Go ONNX Runtime bindings
- [braheezy/shine-mp3](https://github.com/braheezy/shine-mp3) and [mewkiz/flac](https://github.com/mewkiz/flac) for pure-Go audio encoding
- [hraban/opus](https://gopkg.in/hraban/opus.v2) and [libopus](https://opus-codec.org/) for Opus encoding
- [espeak-ng](https://github.com/espeak-ng/espeak-ng) for phonemization

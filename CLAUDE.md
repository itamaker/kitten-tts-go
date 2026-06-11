# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go port of [KittenTTS](https://github.com/KittenML/KittenTTS): an ONNX-based, CPU-only text-to-speech engine. The repo builds two binaries from `cmd/` — `kitten-tts` (one-shot CLI) and `kitten-tts-server` (OpenAI-compatible HTTP API) — on top of an importable `tts` engine package and an `audio` encoder package. Model weights are **not** vendored (Apache-2.0, downloaded separately); the Go code here is MIT.

## Build, test, lint

```bash
go build -o bin/ ./...          # produces bin/kitten-tts and bin/kitten-tts-server
go test -race ./...             # full suite; CI runs with -race
go test -run TestNormalize ./tts/   # a single test
gofmt -l .                      # must print nothing — CI fails on any unformatted file
go vet ./...
```

CI (`.github/workflows/ci.yml`) runs gofmt → vet → build → `go test -race` on Ubuntu and macOS. Match that locally before pushing.

### CGO is required to build

`CGO_ENABLED=1` (the native default) and a C compiler are mandatory — Opus encoding links **libopus + libopusfile** via cgo (`hraban/opus`, `#cgo pkg-config: opus opusfile`). Install build deps first or the build fails:

```bash
# macOS
brew install opus opusfile pkg-config espeak-ng onnxruntime
# Ubuntu/Debian
sudo apt-get install -y libopus-dev libopusfile-dev pkg-config espeak-ng
```

Three system dependencies, with **different binding times**:
- **ONNX Runtime** — `dlopen`'d at *runtime* (no cgo link). Auto-located, or set `ONNXRUNTIME_LIB_PATH`. See `tts/onnx.go:findRuntimeLib`.
- **espeak-ng** — invoked at *runtime* as the default phonemizer.
- **libopus/libopusfile** — linked at *build* time. Released binaries static-link these, so deployed machines only need ONNX Runtime + espeak-ng.

### Tests do not need a model or GPU

The unit tests (`tts/tts_test.go`, `audio/mp3_test.go`) cover pure functions — normalization, chunking, token framing, voice resolution, MP3 frame integrity — and substitute a `fakePhonemizer` for the `Phonemizer` interface. They run with no model files and no espeak-ng. For real end-to-end coverage against a downloaded model, use the smoke test instead:

```bash
scripts/fetch_model.sh nano-int8        # downloads into ./models (git-ignored)
scripts/smoke_test.sh ./models/kitten-tts-nano-int8   # builds + exercises every format, SSE, server validation, offline
```

`smoke_test.sh` also honors `KITTEN_MODEL_DIR` and `KITTEN_BIN_DIR` (test prebuilt binaries instead of building).

## Architecture

The synthesis pipeline, end to end: **normalize → phonemize (espeak-ng) → token IDs → voice style embedding → ONNX inference → trim trailing silence → encode**. The engine produces 24 kHz mono `[]float32`; encoders consume that.

Two packages do the work; `cmd/` is thin glue.

- **`tts/`** — the engine. `tts.New(dir)` reads `config.json` (`load.go`), loads the ONNX model and NPZ voices, returns `*tts.Model`. `Generate` splits long text on sentence boundaries and calls `GenerateChunk` per piece. A `Model` is **not goroutine-safe** — the server guards a single shared session with a mutex (`cmd/kitten-tts-server/main.go`).
- **`audio/`** — encoders behind the `Encoder` interface, looked up by name via `audio.NewEncoder`. Native rate is 24 kHz; MP3 resamples to 44.1 kHz, Opus to 48 kHz.

### Key seams (extend along these, not by adding switch statements)

- **Encoders self-register.** Each format is one file (`wav.go`, `mp3.go`, `flac.go`, `opus.go`) that calls `register(name, ctor)` in its `init`. To add a format, add a file that registers an `Encoder` — do not edit a central dispatch. MP3/FLAC are pure Go; Opus is the only cgo encoder; AAC is intentionally unsupported.
- **Phonemization is an interface.** `Phonemizer` (default: `ESpeak{Voice:"en-us"}`) is swappable via `tts.WithPhonemizer(...)` — this is how tests avoid espeak-ng.
- **ONNX is quarantined.** `tts/onnx.go` is the *only* file importing the `ort` package; swapping inference backends is a local change there. Model graph **input order is irrelevant** — `loadNetwork` resolves each input's role (tokens / style / speed) from its element type and rank, so it works across both `ONNX1` and `ONNX2` model types.

### Conventions worth knowing

- **CLI flags go before positional args.** Both binaries use the stdlib `flag` package; the positional `<model_dir> <text> [voice]` must come *after* all flags, or flags are swallowed as positionals.
- **The `tts` library never logs.** `New` stays silent by design (`load.go`); wire your own logging around it. The `cmd/` binaries do the logging.
- **Voice names are mapped twice.** The server maps OpenAI names (alloy/echo/…) to KittenTTS names (Bella/Jasper/…) in `handlers.go`; the engine then resolves friendly names to internal `expr-voice-*` IDs via `ResolveVoiceName`. Unknown names pass through to let the engine resolve them directly.
- **Trailing silence is trimmed** — up to 5000 samples (the reference model's fixed cut), but never past the last audible sample: comma-terminated chunks pad far less than 5000, and a fixed cut chops speech (see `trimTrailingSilence` in `tts.go`). Chunking deliberately *preserves* terminal sentence punctuation so prosody isn't clipped (see `TestChunkTextKeepsSentencePunctuation` for the why).
- **SSE streaming** requires `response_format: "pcm"`; it uses `ChunkTextStreaming` (small first chunk for fast time-to-first-audio) rather than `ChunkText`.

## Releases

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which builds with `go build` (not GoReleaser) on per-platform native runners — libopus is static-linked from source, so each target builds natively, except darwin/amd64 which cross-compiles on the Apple Silicon runner.

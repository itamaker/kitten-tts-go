#!/usr/bin/env bash
#
# smoke_test.sh — end-to-end offline test for the kitten-tts binaries.
#
# It builds (or reuses) the CLI and server, then exercises every audio format
# plus SSE streaming against a local model. No network access is required at
# runtime; it only uses the local model, espeak-ng, libopus, and the ONNX
# Runtime shared library.
#
# Usage:
#   scripts/smoke_test.sh [MODEL_DIR]
#
# Model dir resolution (first that exists):
#   1. $1 (CLI argument)
#   2. $KITTEN_MODEL_DIR
#   3. ./models/kitten-tts-nano-int8
#
# Environment:
#   KITTEN_BIN_DIR   Use prebuilt binaries from this dir instead of `go build`
#                    (e.g. point it at an extracted release tarball).
#   KEEP=1           Keep the temporary output directory for inspection.
#   PORT=NNNN        Server port (default: 8137).
#
set -euo pipefail

# ── Locate repo + resolve inputs ──────────────────────────────────────────────
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

MODEL_DIR="${1:-${KITTEN_MODEL_DIR:-models/kitten-tts-nano-int8}}"
PORT="${PORT:-8137}"

# ── Pretty output + pass/fail tracking ────────────────────────────────────────
if [[ -t 1 ]]; then
  GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RST=$'\033[0m'
else
  GREEN=""; RED=""; DIM=""; BOLD=""; RST=""
fi
PASS=0
FAIL=0
ok()   { echo "  ${GREEN}✓${RST} $1"; PASS=$((PASS + 1)); }
bad()  { echo "  ${RED}✗${RST} $1"; FAIL=$((FAIL + 1)); }
head() { echo; echo "${BOLD}$1${RST}"; }

# ── Preconditions ─────────────────────────────────────────────────────────────
if [[ ! -f "$MODEL_DIR/config.json" ]]; then
  echo "${RED}Model not found at:${RST} $MODEL_DIR"
  echo "Fetch the smallest model into ./models with:"
  echo "  scripts/fetch_model.sh nano-int8"
  echo "Then re-run, or pass a model dir: scripts/smoke_test.sh /path/to/model"
  exit 1
fi
command -v espeak-ng >/dev/null || { echo "${RED}espeak-ng not found${RST} (brew install espeak-ng / apt install espeak-ng)"; exit 1; }

HAVE_FFPROBE=0
command -v ffprobe >/dev/null && HAVE_FFPROBE=1

# ── Build or locate binaries ──────────────────────────────────────────────────
if [[ -n "${KITTEN_BIN_DIR:-}" ]]; then
  CLI="$KITTEN_BIN_DIR/kitten-tts"
  SERVER="$KITTEN_BIN_DIR/kitten-tts-server"
  echo "Using prebuilt binaries from $KITTEN_BIN_DIR"
else
  echo "Building binaries (go build)..."
  go build -o bin/ ./...
  CLI="bin/kitten-tts"
  SERVER="bin/kitten-tts-server"
fi
[[ -x "$CLI" ]] || { echo "${RED}missing $CLI${RST}"; exit 1; }
[[ -x "$SERVER" ]] || { echo "${RED}missing $SERVER${RST}"; exit 1; }

# ── Temp workspace + cleanup ──────────────────────────────────────────────────
TMP="$(mktemp -d "${TMPDIR:-/tmp}/kitten-smoke.XXXXXX")"
SERVER_PID=""
cleanup() {
  [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null || true
  if [[ "${KEEP:-0}" == "1" ]]; then
    echo; echo "${DIM}Kept outputs in $TMP${RST}"
  else
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT

echo "Model:   $MODEL_DIR"
echo "Output:  $TMP"

# expected ffprobe codec per format (pcm is headerless → size check only)
codec_for() {
  case "$1" in
    mp3) echo mp3 ;; flac) echo flac ;; wav) echo pcm_s16le ;; opus) echo opus ;; *) echo "" ;;
  esac
}

check_audio() { # <format> <file>
  local fmt="$1" file="$2"
  if [[ ! -s "$file" ]]; then bad "$fmt: empty/missing output"; return; fi
  if [[ "$HAVE_FFPROBE" == "1" && "$fmt" != "pcm" ]]; then
    local want got
    want="$(codec_for "$fmt")"
    got="$(ffprobe -v error -select_streams a:0 -show_entries stream=codec_name -of csv=p=0 "$file" 2>/dev/null || true)"
    if [[ "$got" == "$want" ]]; then ok "$fmt: $(wc -c <"$file" | tr -d ' ') bytes, codec=$got"
    else bad "$fmt: expected codec $want, got '${got:-none}'"; fi
  else
    ok "$fmt: $(wc -c <"$file" | tr -d ' ') bytes"
  fi
}

# ── CLI tests ─────────────────────────────────────────────────────────────────
head "CLI ($CLI)"
for fmt in wav mp3 flac opus pcm; do
  out="$TMP/cli.$fmt"
  if "$CLI" -format "$fmt" -o "$out" "$MODEL_DIR" "Smoke test, $fmt output." Bruno >/dev/null 2>"$TMP/cli.err"; then
    check_audio "$fmt" "$out"
  else
    bad "$fmt: CLI exited nonzero ($(tail -1 "$TMP/cli.err"))"
  fi
done
if "$CLI" -list-voices "$MODEL_DIR" 2>/dev/null | grep -q Bruno; then ok "list-voices lists Bruno"; else bad "list-voices"; fi

# ── Server tests ──────────────────────────────────────────────────────────────
head "Server ($SERVER, port $PORT)"
"$SERVER" -port "$PORT" "$MODEL_DIR" >"$TMP/server.log" 2>&1 &
SERVER_PID=$!
BASE="http://127.0.0.1:$PORT"
for _ in $(seq 1 60); do
  curl -fsS "$BASE/health" >/dev/null 2>&1 && break
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then bad "server exited during startup"; cat "$TMP/server.log"; break; fi
  sleep 0.5
done

if curl -fsS "$BASE/health" 2>/dev/null | grep -q '"status":"ok"'; then ok "GET /health"; else bad "GET /health"; fi
if curl -fsS "$BASE/v1/models" 2>/dev/null | grep -q 'kitten-tts'; then ok "GET /v1/models"; else bad "GET /v1/models"; fi

for fmt in mp3 flac wav opus pcm; do
  out="$TMP/api.$fmt"
  curl -fsS -X POST "$BASE/v1/audio/speech" -H 'Content-Type: application/json' \
    -d "{\"input\":\"Server smoke test, $fmt.\",\"voice\":\"alloy\",\"response_format\":\"$fmt\"}" -o "$out" 2>/dev/null \
    && check_audio "$fmt" "$out" || bad "$fmt: request failed"
done

# SSE streaming: expect at least one delta and a done event
stream="$(curl -fsS -N -X POST "$BASE/v1/audio/speech" -H 'Content-Type: application/json' \
  -d '{"input":"One. Two. Three.","voice":"alloy","response_format":"pcm","stream":true}' 2>/dev/null || true)"
if grep -q 'speech.audio.delta' <<<"$stream" && grep -q 'speech.audio.done' <<<"$stream"; then
  ok "SSE streaming (delta + done)"
else
  bad "SSE streaming"
fi

# Validation: bad speed and unknown format should be rejected (HTTP 400)
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/v1/audio/speech" -H 'Content-Type: application/json' -d '{"input":"x","voice":"alloy","speed":9}')"
[[ "$code" == "400" ]] && ok "rejects speed out of range (400)" || bad "bad speed returned $code, want 400"
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/v1/audio/speech" -H 'Content-Type: application/json' -d '{"input":"x","voice":"alloy","response_format":"zzz"}')"
[[ "$code" == "400" ]] && ok "rejects unknown format (400)" || bad "unknown format returned $code, want 400"

# ── Summary ───────────────────────────────────────────────────────────────────
head "Result"
echo "  ${GREEN}$PASS passed${RST}, $([[ $FAIL -gt 0 ]] && echo "${RED}$FAIL failed${RST}" || echo "0 failed")"
[[ $FAIL -eq 0 ]]

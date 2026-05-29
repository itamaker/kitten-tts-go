#!/usr/bin/env bash
#
# fetch_model.sh — download a KittenTTS model from Hugging Face into ./models.
#
# The models are not vendored in this repository; this script pulls one so the
# project is self-contained for local runs and the smoke test.
#
# Usage:
#   scripts/fetch_model.sh [name]
#
#   name ∈ { nano-int8 (default), nano, micro, mini }
#
# Environment:
#   FORCE=1   Re-download even if the model already exists.
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

NAME="${1:-nano-int8}"

# name → Hugging Face repo. The local directory is models/kitten-tts-<name>.
case "$NAME" in
  nano-int8) HF_REPO="KittenML/kitten-tts-nano-0.8-int8" ;;
  nano)      HF_REPO="KittenML/kitten-tts-nano-0.8-fp32" ;;
  micro)     HF_REPO="KittenML/kitten-tts-micro-0.8" ;;
  mini)      HF_REPO="KittenML/kitten-tts-mini-0.8" ;;
  *)
    echo "Unknown model '$NAME'. Choose: nano-int8, nano, micro, mini" >&2
    exit 1
    ;;
esac

DEST="models/kitten-tts-${NAME}"
BASE="https://huggingface.co/${HF_REPO}/resolve/main"

if [[ -f "$DEST/config.json" && "${FORCE:-0}" != "1" ]]; then
  echo "Model already present at $DEST (set FORCE=1 to re-download)."
  exit 0
fi

command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
mkdir -p "$DEST"

fetch() { # <remote-name> <local-path>
  echo "  downloading $1 ..."
  curl -fSL --retry 3 -o "$2" "$BASE/$1"
}

# config.json names the ONNX model and voices files, so fetch it first and read
# those names from it (portable grep/sed, no jq/python needed).
fetch "config.json" "$DEST/config.json"

read_field() { # <field>
  grep -o "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" "$DEST/config.json" \
    | head -1 | sed 's/.*"\([^"]*\)"$/\1/'
}
MODEL_FILE="$(read_field model_file)"
VOICES_FILE="$(read_field voices)"

[[ -n "$MODEL_FILE" ]]  || { echo "could not read model_file from config.json" >&2; exit 1; }
[[ -n "$VOICES_FILE" ]] || { echo "could not read voices from config.json" >&2; exit 1; }

fetch "$MODEL_FILE"  "$DEST/$MODEL_FILE"
fetch "$VOICES_FILE" "$DEST/$VOICES_FILE"

echo "Done. Model at: $DEST"
ls -la "$DEST"

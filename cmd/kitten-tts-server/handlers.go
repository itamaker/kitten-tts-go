package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/itamaker/kitten-tts-go/audio"
	"github.com/itamaker/kitten-tts-go/tts"
)

// maxRequestBody caps the request body size; speech inputs are short text, so
// 1 MiB is generous while keeping oversized payloads from tying up the server.
const maxRequestBody = 1 << 20

// writeError renders an OpenAI-style JSON error body.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    nil,
		},
	})
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleListModels(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "kitten-tts", "object": "model", "owned_by": "kittenml"},
		},
	})
}

// speechRequest is the OpenAI-compatible speech request body.
type speechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice"`
	ResponseFormat string  `json:"response_format"`
	Speed          float32 `json:"speed"`
	Stream         bool    `json:"stream"`
}

// openAIVoices maps OpenAI voice names to KittenTTS voices. Unknown names pass
// through so the engine can resolve them directly.
var openAIVoices = map[string]string{
	"alloy":   "Bella",
	"echo":    "Jasper",
	"fable":   "Luna",
	"onyx":    "Bruno",
	"nova":    "Rosie",
	"shimmer": "Hugo",
}

func mapVoice(voice string) string {
	if mapped, ok := openAIVoices[strings.ToLower(voice)]; ok {
		return mapped
	}
	return voice
}

func (s *server) handleSpeech(w http.ResponseWriter, r *http.Request) {
	req := speechRequest{ResponseFormat: "mp3", Speed: 1.0}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.ResponseFormat == "" {
		req.ResponseFormat = "mp3"
	}
	if req.Speed == 0 {
		req.Speed = 1.0
	}

	if req.Input == "" {
		writeError(w, http.StatusBadRequest, "'input' must not be empty")
		return
	}
	if err := tts.ValidateSpeed(req.Speed); err != nil {
		writeError(w, http.StatusBadRequest, "'speed' "+err.Error())
		return
	}

	enc, err := audio.NewEncoder(req.ResponseFormat)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Stream && enc.Name() != "pcm" {
		writeError(w, http.StatusBadRequest, "streaming only supports response_format 'pcm'")
		return
	}

	voice := mapVoice(req.Voice)
	log.Printf("speech: voice=%s format=%s speed=%g len=%d stream=%v",
		voice, enc.Name(), req.Speed, len(req.Input), req.Stream)

	if req.Stream {
		s.speechStream(r.Context(), w, req.Input, voice, req.Speed)
	} else {
		s.speechFull(r.Context(), w, req.Input, voice, req.Speed, enc)
	}
}

// speechFull synthesizes the whole input and returns it in the requested
// format. ctx is the request context, so a client that disconnects mid-request
// stops occupying the shared model rather than running to completion unread.
func (s *server) speechFull(ctx context.Context, w http.ResponseWriter, input, voice string, speed float32, enc audio.Encoder) {
	s.mu.Lock()
	samples, err := s.model.GenerateContext(ctx, input, voice, speed, true)
	s.mu.Unlock()
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, tts.ErrUnknownVoice) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Client is gone; nothing left to write to.
			return
		}
		writeError(w, status, err.Error())
		return
	}

	data, err := enc.Encode(samples)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", enc.ContentType())
	w.Write(data)
}

// speechStream synthesizes chunk-by-chunk and emits each as an SSE event. ctx
// is the request context: it's checked before each chunk so a disconnected
// client stops the stream instead of synthesizing chunks nobody will read.
func (s *server) speechStream(ctx context.Context, w http.ResponseWriter, input, voice string, speed float32) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported by server")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sendEvent := func(v any) bool {
		b, _ := json.Marshal(v)
		if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	chunks := tts.ChunkTextStreaming(tts.Normalize(input), 100, 400)
	log.Printf("streaming %d chunks", len(chunks))

	for i, chunk := range chunks {
		if ctx.Err() != nil {
			log.Printf("client disconnected before chunk %d", i)
			return
		}

		// Lock per chunk (not for the whole stream) so other requests can
		// interleave with a long-running stream; the session itself is the
		// only shared state.
		s.mu.Lock()
		samples, err := s.model.GenerateChunkContext(ctx, chunk, voice, speed)
		s.mu.Unlock()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			sendEvent(map[string]any{
				"type":  "error",
				"error": map[string]any{"message": err.Error()},
			})
			return
		}

		delta := base64.StdEncoding.EncodeToString(audio.EncodePCM(samples))
		log.Printf("chunk %d: text_len=%d samples=%d", i, len(chunk), len(samples))

		if !sendEvent(map[string]any{"type": "speech.audio.delta", "delta": delta}) {
			log.Printf("client disconnected during streaming")
			return
		}
	}

	sendEvent(map[string]any{"type": "speech.audio.done"})
	log.Printf("streaming complete")
}

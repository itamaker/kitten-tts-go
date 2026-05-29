// Command kitten-tts-server runs an OpenAI-compatible TTS API server.
//
// It loads a KittenTTS model at startup and serves an OpenAI-style
// /v1/audio/speech endpoint (plus /v1/models and /health).
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/itamaker/kitten-tts-go/tts"
)

// server holds the loaded model behind a mutex so a single ONNX session can be
// shared safely across concurrent HTTP handlers.
type server struct {
	mu    sync.Mutex
	model *tts.Model
}

func main() {
	host := flag.String("host", "127.0.0.1", "server host address")
	port := flag.Int("port", 8080, "server port")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() < 1 {
		usage()
		log.Fatal("model directory is required")
	}

	model, err := tts.New(flag.Arg(0))
	if err != nil {
		log.Fatalf("loading model: %v", err)
	}
	defer model.Close()
	log.Printf("model loaded from %s", flag.Arg(0))

	srv := &server{model: model}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /v1/models", handleListModels)
	mux.HandleFunc("POST /v1/audio/speech", srv.handleSpeech)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("listening on http://%s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `kitten-tts-server — OpenAI-compatible TTS API server

Usage:
  kitten-tts-server [flags] <model_dir>

Flags:
`)
	flag.PrintDefaults()
}

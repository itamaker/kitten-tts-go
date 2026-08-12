// Command kitten-tts-server runs an OpenAI-compatible TTS API server.
//
// It loads a KittenTTS model at startup and serves an OpenAI-style
// /v1/audio/speech endpoint (plus /v1/models and /health).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

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
	threads := flag.Int("threads", tts.DefaultIntraOpThreads, "ONNX intra-op thread count (0 = onnxruntime's own default)")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() < 1 {
		usage()
		log.Fatal("model directory is required")
	}

	model, err := tts.New(flag.Arg(0), tts.WithIntraOpThreads(*threads))
	if err != nil {
		log.Fatalf("loading model: %v", err)
	}
	// log.Fatal below the ListenAndServe call would skip this defer entirely
	// (it calls os.Exit), which is why shutdown is handled explicitly rather
	// than by letting the process die on SIGINT/SIGTERM: the ONNX session
	// should release its native resources on the way out.
	defer model.Close()
	log.Printf("model loaded from %s", flag.Arg(0))

	srv := &server{model: model}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /v1/models", handleListModels)
	mux.HandleFunc("POST /v1/audio/speech", srv.handleSpeech)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	// No WriteTimeout: synthesis of long inputs and SSE streams are
	// intentionally long-lived responses.
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("listening on http://%s", addr)
		serveErr <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	case <-ctx.Done():
		stop() // restore default signal behavior so a second signal force-quits
		log.Print("shutting down (signal received)...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed, forcing close: %v", err)
			httpSrv.Close()
		}
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

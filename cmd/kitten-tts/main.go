// Command kitten-tts synthesizes speech from text on the command line.
//
// Usage:
//
//	kitten-tts [flags] <model_dir> <text> [voice]
//
// Flags come before the positional arguments (Go's flag convention). The
// optional third positional is a voice name shorthand for -voice.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/itamaker/kitten-tts-go/audio"
	"github.com/itamaker/kitten-tts-go/tts"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "kitten-tts: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		voice      = flag.String("voice", "", "voice name (overrides the positional voice)")
		speed      = flag.Float64("speed", 1.0, "speech speed multiplier")
		output     = flag.String("output", "output.wav", "output file path")
		format     = flag.String("format", "wav", "output format: "+strings.Join(audio.Formats(), ", "))
		noClean    = flag.Bool("no-clean", false, "disable text normalization (numbers, currency)")
		listVoices = flag.Bool("list-voices", false, "list available voices and exit")
	)
	// Short aliases sharing the same destinations.
	flag.Float64Var(speed, "s", 1.0, "shorthand for -speed")
	flag.StringVar(output, "o", "output.wav", "shorthand for -output")
	flag.StringVar(voice, "v", "", "shorthand for -voice")

	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	// The voice list is static; don't require (or load) a model for it.
	if *listVoices {
		fmt.Println("Available voices:")
		for _, v := range tts.VoiceNames {
			fmt.Printf("  %s\n", v)
		}
		return nil
	}

	if len(args) < 1 {
		usage()
		return fmt.Errorf("missing model directory")
	}

	model, err := tts.New(args[0])
	if err != nil {
		return err
	}
	defer model.Close()

	if len(args) < 2 {
		usage()
		return fmt.Errorf("missing text to synthesize")
	}
	text := args[1]

	name := *voice
	if name == "" && len(args) >= 3 {
		name = args[2]
	}
	if name == "" {
		name = "Bruno"
	}

	enc, err := audio.NewEncoder(*format)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Synthesizing (voice=%s, speed=%g, format=%s)...\n", name, *speed, enc.Name())
	samples, err := model.Generate(text, name, float32(*speed), !*noClean)
	if err != nil {
		return err
	}

	data, err := enc.Encode(samples)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Done: %d samples (%.2fs) -> %s\n",
		len(samples), float64(len(samples))/audio.SampleRate, *output)
	return nil
}

func usage() {
	fmt.Fprint(os.Stderr, `kitten-tts — ultra-lightweight ONNX text-to-speech

Usage:
  kitten-tts [flags] <model_dir> <text> [voice]

Flags:
`)
	flag.PrintDefaults()
}

package tts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// modelConfig mirrors the config.json shipped alongside each model.
type modelConfig struct {
	Type        string             `json:"type"`
	ModelFile   string             `json:"model_file"`
	Voices      string             `json:"voices"`
	SpeedPriors map[string]float32 `json:"speed_priors"`
	Aliases     map[string]string  `json:"voice_aliases"`
}

// New loads a model from a directory containing config.json, the ONNX model
// file, and the voices NPZ archive.
//
// Unlike a verbose loader, New stays quiet: a library should not print to
// stderr. Wire up your own logging around it if you want progress output.
func New(dir string, opts ...Option) (*Model, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, fmt.Errorf("tts: reading config: %w", err)
	}

	var cfg modelConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("tts: parsing config: %w", err)
	}
	if cfg.Type != "ONNX1" && cfg.Type != "ONNX2" {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedModel, cfg.Type)
	}

	return load(
		filepath.Join(dir, cfg.ModelFile),
		filepath.Join(dir, cfg.Voices),
		cfg.SpeedPriors,
		cfg.Aliases,
		opts,
	)
}

func load(modelPath, voicesPath string, priors map[string]float32, aliases map[string]string, opts []Option) (*Model, error) {
	o := options{phonemizer: defaultPhonemizer(), intraOpThreads: DefaultIntraOpThreads}
	for _, opt := range opts {
		opt(&o)
	}

	net, err := loadNetwork(modelPath, o.intraOpThreads)
	if err != nil {
		return nil, err
	}

	voices, err := LoadVoices(voicesPath)
	if err != nil {
		_ = net.close()
		return nil, err
	}

	if priors == nil {
		priors = map[string]float32{}
	}
	if aliases == nil {
		aliases = map[string]string{}
	}

	return &Model{
		net:         net,
		voices:      voices,
		speedPriors: priors,
		aliases:     aliases,
		phonemizer:  o.phonemizer,
	}, nil
}

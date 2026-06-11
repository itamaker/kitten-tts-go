package tts

// This file isolates the only dependency on the ONNX Runtime (cgo-free; the
// shared library is dlopen'd at runtime). Nothing outside this file imports
// the ort package, so swapping inference backends would be a local change.

import (
	"fmt"
	"os"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	runtimeOnce sync.Once
	runtimeErr  error
)

// initRuntime initializes the ONNX Runtime environment exactly once. The shared
// library is located via $ONNXRUNTIME_LIB_PATH, then a few common install
// paths.
func initRuntime() error {
	runtimeOnce.Do(func() {
		if p := os.Getenv("ONNXRUNTIME_LIB_PATH"); p != "" {
			ort.SetSharedLibraryPath(p)
		} else if p := findRuntimeLib(); p != "" {
			ort.SetSharedLibraryPath(p)
		}
		runtimeErr = ort.InitializeEnvironment()
	})
	return runtimeErr
}

func findRuntimeLib() string {
	for _, p := range []string{
		"/opt/homebrew/lib/libonnxruntime.dylib", // macOS (Apple Silicon)
		"/usr/local/lib/libonnxruntime.dylib",    // macOS (Intel)
		"/usr/lib/libonnxruntime.so",             // Linux
		"/usr/local/lib/libonnxruntime.so",
		"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
		"/usr/lib/aarch64-linux-gnu/libonnxruntime.so",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// inputRole identifies which tensor a model input expects, resolved once at load
// time from the input's element type and rank so graph input order is irrelevant.
type inputRole int

const (
	roleTokens inputRole = iota // int64 [1, n]
	roleStyle                   // float32 [1, cols]
	roleSpeed                   // float32 [1]
)

// network wraps a loaded ONNX session and the resolved role of each input.
type network struct {
	session *ort.DynamicAdvancedSession
	roles   []inputRole
	outputs int
}

func loadNetwork(path string) (*network, error) {
	if err := initRuntime(); err != nil {
		return nil, fmt.Errorf("tts: onnx runtime unavailable: %w", err)
	}

	inInfo, outInfo, err := ort.GetInputOutputInfo(path)
	if err != nil {
		return nil, fmt.Errorf("tts: reading model I/O: %w", err)
	}

	inNames := make([]string, len(inInfo))
	roles := make([]inputRole, len(inInfo))
	for i, info := range inInfo {
		inNames[i] = info.Name
		switch {
		case info.DataType == ort.TensorElementDataTypeInt64:
			roles[i] = roleTokens
		case len(info.Dimensions) <= 1:
			roles[i] = roleSpeed
		default:
			roles[i] = roleStyle
		}
	}
	if len(outInfo) == 0 {
		return nil, fmt.Errorf("tts: model has no outputs")
	}
	outNames := make([]string, len(outInfo))
	for i, info := range outInfo {
		outNames[i] = info.Name
	}

	session, err := ort.NewDynamicAdvancedSession(path, inNames, outNames, nil)
	if err != nil {
		return nil, fmt.Errorf("tts: loading model: %w", err)
	}
	return &network{session: session, roles: roles, outputs: len(outNames)}, nil
}

// infer runs the model and returns the first output as f32 samples.
func (n *network) infer(tokens []int64, style []float32, styleCols int, speed float32) ([]float32, error) {
	idsT, err := ort.NewTensor(ort.NewShape(1, int64(len(tokens))), tokens)
	if err != nil {
		return nil, err
	}
	defer idsT.Destroy()

	styleT, err := ort.NewTensor(ort.NewShape(1, int64(styleCols)), style)
	if err != nil {
		return nil, err
	}
	defer styleT.Destroy()

	speedT, err := ort.NewTensor(ort.NewShape(1), []float32{speed})
	if err != nil {
		return nil, err
	}
	defer speedT.Destroy()

	inputs := make([]ort.Value, len(n.roles))
	for i, role := range n.roles {
		switch role {
		case roleTokens:
			inputs[i] = idsT
		case roleSpeed:
			inputs[i] = speedT
		default:
			inputs[i] = styleT
		}
	}

	outputs := make([]ort.Value, n.outputs)
	if err := n.session.Run(inputs, outputs); err != nil {
		return nil, err
	}
	defer func() {
		for _, o := range outputs {
			if o != nil {
				o.Destroy()
			}
		}
	}()

	tensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("tts: unexpected output tensor type %T", outputs[0])
	}
	data := tensor.GetData()
	audio := make([]float32, len(data))
	copy(audio, data)
	return audio, nil
}

func (n *network) close() error {
	if n.session != nil {
		return n.session.Destroy()
	}
	return nil
}

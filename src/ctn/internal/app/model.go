package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	ml "github.com/webong/ext/res/int/model"
)

// modelCommand lists, inspects, or runs on-device models through the
// portable res/int/model contracts. The model store and provider backends are
// caller-configured; ctn itself selects no model, device, or provider.
func modelCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctn: model needs one of list, inspect, install, remove, verify, or run")
		return 2
	}
	switch args[0] {
	case "list":
		return modelListCommand(args[1:], stdout, stderr)
	case "inspect":
		return modelInspectCommand(args[1:], stdout, stderr)
	case "install":
		return modelInstallCommand(args[1:], stdout, stderr)
	case "remove":
		return modelRemoveCommand(args[1:], stdout, stderr)
	case "verify":
		return modelVerifyCommand(args[1:], stdout, stderr)
	case "run":
		return modelRunCommand(args[1:], stdout, stderr)
	case "provider":
		return modelProviderCommand(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "ctn: unknown model action %s\n", args[0])
		return 2
	}
}

func modelListCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "ctn: model list needs <root-dir>")
		return 2
	}
	store, err := ml.OpenStore(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	defer store.Close()
	models, err := store.List(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	for _, m := range models {
		fmt.Fprintf(stdout, "%s\n", m.Manifest.Provider+"/"+m.Manifest.Name+"@"+m.Manifest.Version)
	}
	return 0
}

func modelInspectCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "ctn: model inspect needs <root-dir> <provider/name@version>")
		return 2
	}
	store, err := ml.OpenStore(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	defer store.Close()
	ref, err := ml.ReferenceFrom(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	model, err := store.Resolve(context.Background(), ref.Provider, ref.Name, ref.Version)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	data, err := json.MarshalIndent(model.Manifest, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s\n", string(data))
	return 0
}

// modelInstallCommand installs a model bundle from a manifest and artifact files.
// The input file is a JSON object with "manifest" and "artifacts" keys.
func modelInstallCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "ctn: model install needs <root-dir> <manifest-file> <artifacts-dir>")
		return 2
	}
	store, err := ml.OpenStore(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	defer store.Close()
	manifestData, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	var manifest ml.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		fmt.Fprintf(stderr, "ctn: invalid manifest: %v\n", err)
		return 1
	}
	artifactsDir := args[2]
	files := make(map[string][]byte)
	for _, artifact := range manifest.Artifacts {
		data, err := os.ReadFile(filepath.Join(artifactsDir, artifact.File))
		if err != nil {
			fmt.Fprintf(stderr, "ctn: cannot read artifact %s: %v\n", artifact.File, err)
			return 1
		}
		files[artifact.File] = data
	}
	installed, err := store.InstallFromBytes(context.Background(), manifest, files)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed %s at %s\n", installed.Manifest.Provider+"/"+installed.Manifest.Name+"@"+installed.Manifest.Version, installed.Directory)
	return 0
}

func modelRemoveCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "ctn: model remove needs <root-dir> <provider/name@version>")
		return 2
	}
	store, err := ml.OpenStore(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	defer store.Close()
	ref, err := ml.ReferenceFrom(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	if err := store.Remove(context.Background(), ref.Provider, ref.Name, ref.Version); err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "removed %s\n", ref.String())
	return 0
}

func modelVerifyCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "ctn: model verify needs <root-dir> <provider/name@version>")
		return 2
	}
	store, err := ml.OpenStore(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	defer store.Close()
	ref, err := ml.ReferenceFrom(args[1])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	model, err := store.Resolve(context.Background(), ref.Provider, ref.Name, ref.Version)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "verified %s (%d artifacts)\n", ref.String(), len(model.Manifest.Artifacts))
	return 0
}

// modelRunInput describes one input tensor for model run.
type modelRunInput struct {
	Tensors []mlTensorInput `json:"tensors"`
}

type mlTensorInput struct {
	Shape  []int  `json:"shape"`
	Format string `json:"format"`
	Data   []byte `json:"data"` // base64-encoded
}

// modelRunCommand resolves a model and calls an explicitly trusted provider
// plugin. The guest verifies the model independently before native inference.
func modelRunCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn model run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider name (required)")
	timeout := flags.Duration("timeout", 30*time.Second, "inference timeout")
	outputPath := flags.String("output", "", "write output tensors as JSON to this file")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	rest := flags.Args()
	if len(rest) != 3 {
		fmt.Fprintln(stderr, "ctn: model run needs <root-dir> <provider/name@version> <input-file>")
		return 2
	}
	if *provider == "" {
		fmt.Fprintln(stderr, "ctn: --provider is required for model run")
		return 2
	}

	store, err := ml.OpenStore(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	defer store.Close()
	ref, err := ml.ReferenceFrom(rest[1])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}

	// Read input file with tensor descriptions.
	file, err := os.Open(rest[2])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	inputData, err := io.ReadAll(io.LimitReader(file, 18<<20))
	_ = file.Close()
	if err != nil || len(inputData) >= 18<<20 {
		fmt.Fprintln(stderr, "ctn: input tensor file exceeds the wire limit")
		return 1
	}
	var input modelRunInput
	if err := json.Unmarshal(inputData, &input); err != nil {
		fmt.Fprintf(stderr, "ctn: invalid input file: %v\n", err)
		return 1
	}
	if len(input.Tensors) == 0 {
		fmt.Fprintln(stderr, "ctn: input file must contain at least one tensor")
		return 2
	}
	tensors := make([]ml.Tensor, 0, len(input.Tensors))
	for _, ti := range input.Tensors {
		tensor := ml.Tensor{Shape: ti.Shape, Data: ti.Data, Format: ti.Format}
		if err := tensor.Validate(); err != nil {
			fmt.Fprintf(stderr, "ctn: invalid tensor: %v\n", err)
			return 2
		}
		tensors = append(tensors, tensor)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if _, err := store.Resolve(ctx, ref.Provider, ref.Name, ref.Version); err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	outputs, err := runTensorProvider(ctx, *provider, rest[0], ref, tensors)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	encoded, err := json.MarshalIndent(struct {
		Tensors []ml.Tensor `json:"tensors"`
	}{Tensors: outputs}, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	encoded = append(encoded, '\n')
	if *outputPath == "" {
		_, _ = stdout.Write(encoded)
		return 0
	}
	file, err = os.OpenFile(*outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	_, writeErr := file.Write(encoded)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(*outputPath)
		fmt.Fprintf(stderr, "ctn: %v\n", errors.Join(writeErr, closeErr))
		return 1
	}
	fmt.Fprintln(stdout, *outputPath)
	return 0
}

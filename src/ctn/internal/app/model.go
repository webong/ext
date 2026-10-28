package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/webong/ext/res/ml"
)

// modelCommand lists, inspects, or runs on-device models through the
// portable res/ml contracts. The model store and provider backends are
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

// modelRunCommand resolves a model, selects a provider from the registry,
// loads a session, runs inference, and closes the session reliably.
func modelRunCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ctn model run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "", "provider name (required)")
	timeout := flags.Duration("timeout", 30*time.Second, "inference timeout")
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
	ref, err := ml.ReferenceFrom(rest[1])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}

	// Read input file with tensor descriptions.
	inputData, err := os.ReadFile(rest[2])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
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

	reg := ml.NewRegistry()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	outputs, err := ml.RunModel(ctx, reg, store, ref, tensors)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	for i, out := range outputs {
		fmt.Fprintf(stdout, "output %d: shape=%v format=%s size=%d bytes\n", i, out.Shape, out.Format, len(out.Data))
	}
	return 0
}

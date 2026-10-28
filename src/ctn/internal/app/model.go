package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/webong/ext/res/ml"
)

// modelCommand lists, inspects, or runs on-device models through the
// portable res/ml contracts. The model store and provider backends are
// caller-configured; ctn itself selects no model, device, or provider.
func modelCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctn: model needs one of list, inspect, or run")
		return 2
	}
	switch args[0] {
	case "list":
		return modelListCommand(args[1:], stdout, stderr)
	case "inspect":
		return modelInspectCommand(args[1:], stdout, stderr)
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

func modelRunCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "ctn: model run needs <root-dir> <provider/name@version> <input-file>")
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
	inputData, err := os.ReadFile(args[2])
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	// Build a 1-D float32 tensor from the raw input bytes.
	values := make([]float32, len(inputData))
	for i, v := range inputData {
		values[i] = float32(v)
	}
	tensor, err := ml.FromFloat32([]int{1, len(values)}, values)
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	// No provider is selected by ctn; the caller must supply one through a
	// product adapter. Report the resolved model instead.
	fmt.Fprintf(stdout, "model %s ready at %s\n", ref.String(), model.Directory)
	fmt.Fprintf(stdout, "inputs: %d bytes, tensor shape: %v\n", len(inputData), tensor.Shape)
	return 0
}

package pkgcmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
)

// packExecutable exposes the shared sealer without selecting a consumer or contract.
func packExecutable(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ext package pack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	descriptorPath := flags.String("descriptor", "", "guest descriptor JSON")
	var options store.ExecutablePackage
	flags.StringVar(&options.ManifestPath, "manifest", storelayout.ExtManifest, "output package-relative manifest path")
	flags.StringVar(&options.Protocol, "protocol", "", "declared contract/version")
	flags.StringVar(&options.Binary, "binary", "", "compiled guest executable")
	flags.StringVar(&options.OutputDir, "output", "", "new sealed package directory")
	flags.StringVar(&options.OS, "os", "", "target operating system (default: current)")
	flags.StringVar(&options.Arch, "arch", "", "target architecture (default: current)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *descriptorPath == "" || options.Protocol == "" || options.Binary == "" || options.OutputDir == "" {
		fmt.Fprintln(stderr, "ext: package pack requires --descriptor, --protocol, --binary and --output")
		return 2
	}
	file, err := os.Open(*descriptorPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var descriptor plugin.Descriptor
	if err := decoder.Decode(&descriptor); err != nil {
		fmt.Fprintln(stderr, "ext: invalid package descriptor:", err)
		return 1
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fmt.Fprintln(stderr, "ext: package descriptor must contain one JSON value")
		return 1
	}
	options.Descriptor = descriptor
	manifest, err := store.SealExecutable(options)
	if err != nil {
		fmt.Fprintln(stderr, "ext: package pack:", err)
		return 1
	}
	fmt.Fprintf(stdout, "packed %s in %s\n", manifest.Name(), options.OutputDir)
	return 0
}

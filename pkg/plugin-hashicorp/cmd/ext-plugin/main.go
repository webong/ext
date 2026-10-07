// ext-plugin inspects portable packages without loading or executing plugins.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin-hashicorp"
	"github.com/webong/ext/pkg/plugin/inprocess"
	"github.com/webong/ext/pkg/plugin/jsonline"
	"github.com/webong/ext/pkg/plugin/packagekit"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: ext-plugin inspect [--root DIR] [--entry NAME] MANIFEST | resolve MANIFEST...")
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	switch args[0] {
	case "inspect":
		flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		root := flags.String("root", "", "verify artifact contents under this directory")
		entry := flags.String("entry", "", "preflight a named entrypoint")
		shared := flags.String("shared", "{}", "JSON object of host-provided dependency versions")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("inspect requires one manifest")
		}
		m, err := read(flags.Arg(0))
		if err != nil {
			return err
		}
		report := struct {
			Valid             bool                        `json:"valid"`
			ArtifactsVerified bool                        `json:"artifactsVerified"`
			Trusted           bool                        `json:"trusted"`
			Manifest          packagekit.Manifest         `json:"manifest"`
			Launch            *packagekit.LaunchSelection `json:"launch,omitempty"`
		}{Valid: true, Manifest: m}
		if *root != "" {
			if err := m.VerifyArtifacts(*root); err != nil {
				return err
			}
			report.ArtifactsVerified = true
		}
		if *entry != "" {
			var versions map[string]string
			if err := plugin.Decode([]byte(*shared), &versions); err != nil {
				return err
			}
			selection, err := m.SelectEntrypoint(*entry, packagekit.Environment{OS: runtime.GOOS, Arch: runtime.GOARCH, SharedVersions: versions, Runtimes: map[string]plugin.BackendProfile{"jsonline": jsonline.Profile(), "hashicorp/grpc": hashicorp.GRPCProfile(), "hashicorp/netrpc": hashicorp.NetRPCProfile(), "inprocess": inprocess.Profile()}})
			if err != nil {
				return err
			}
			report.Launch = &selection
		}
		return encoder.Encode(report)
	case "resolve":
		if len(args) < 2 {
			return errors.New("resolve requires manifests")
		}
		manifests := make([]packagekit.Manifest, 0, len(args)-1)
		for _, name := range args[1:] {
			m, err := read(name)
			if err != nil {
				return err
			}
			manifests = append(manifests, m)
		}
		plan, err := packagekit.Resolve(manifests)
		if err != nil {
			return err
		}
		return encoder.Encode(plan)
	default:
		return fmt.Errorf("unknown plugin command %q", args[0])
	}
}
func read(name string) (packagekit.Manifest, error) {
	file, err := os.Open(name)
	if err != nil {
		return packagekit.Manifest{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, plugin.MaxFrameBytes+1))
	if err != nil {
		return packagekit.Manifest{}, err
	}
	return packagekit.Decode(data)
}

package pkgcmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/plugin/package/author"
)

func (m Manager) author(sub string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "ext: package %s requires a directory or package ID\n", sub)
		return 2
	}
	if sub == "doctor" {
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: ext package doctor <installed-id>")
			return 2
		}
		p, err := m.Store.Load(args[0])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		trusted, err := m.Trust.Trusted(p)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		// A package without artifacts has nothing platform-specific to run.
		compatible := len(p.Manifest.Artifacts) == 0 && len(p.Manifest.Entrypoints) == 0
		if len(p.Manifest.Entrypoints) == 0 {
			for _, a := range p.Manifest.Artifacts {
				if (a.OS == "" || a.OS == runtime.GOOS) && (a.Arch == "" || a.Arch == runtime.GOARCH) {
					compatible = true
				}
			}
		}
		for _, e := range p.Manifest.Entrypoints {
			a, ok := p.Manifest.Artifact(e.Artifact)
			if ok && (a.OS == "" || a.OS == runtime.GOOS) && (a.Arch == "" || a.Arch == runtime.GOARCH) {
				compatible = true
			}
		}
		fmt.Fprintf(stdout, "package %s: integrity=valid trusted=%t target-compatible=%t contracts=%d (no guest executed)\n", p.Name(), trusted, compatible, len(p.Manifest.Descriptor.Contracts))
		if !trusted || !compatible {
			return 1
		}
		return 0
	}
	o := author.Options{Source: args[0], OS: runtime.GOOS, Arch: runtime.GOARCH}
	flags := flag.NewFlagSet("ext package "+sub, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&o.ManifestPath, "manifest", "ext.json", "package-relative manifest path")
	flags.StringVar(&o.Output, "output", "", "new package ZIP")
	flags.StringVar(&o.OS, "os", o.OS, "target operating system")
	flags.StringVar(&o.Arch, "arch", o.Arch, "target architecture")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if sub != "build" && flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected package arguments")
		return 2
	}
	if (sub == "pack" || sub == "build") && o.Output == "" {
		fmt.Fprintln(stderr, "ext: --output is required")
		return 2
	}
	var err error
	switch sub {
	case "pack":
		err = author.Pack(o)
	case "build":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err = author.Build(ctx, o, flags.Args())
	case "test":
		stage, manifest, checkErr := author.Prepare(o)
		err = checkErr
		if err == nil {
			os.RemoveAll(stage)
			fmt.Fprintf(stdout, "package %s: manifest and declared artifacts valid for %s/%s (no guest executed)\n", manifest.Name(), o.OS, o.Arch)
			return 0
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "ext: package %s: %v\n", sub, err)
		return 1
	}
	verb := "packed"
	if sub == "build" {
		verb = "built"
	}
	fmt.Fprintf(stdout, "%s package %s\n", verb, o.Output)
	return 0
}

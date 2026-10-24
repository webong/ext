// Command ext runs ctx, ctn and any other registered command.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/webong/ext/cmd/ext/internal/app"
	"github.com/webong/ext/cmd/ext/internal/config"
	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	"github.com/webong/ext/cmd/ext/internal/registry"
	"github.com/webong/ext/ctn"
	"github.com/webong/ext/ctx"
	"github.com/webong/ext/pkg/plugin-go/contract/host"
	"github.com/webong/ext/pkg/plugin-go/contract/host/adapter"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
)

func main() {
	reg := registry.New()
	for _, c := range []registry.Command{
		{Name: "ctx", Source: registry.Builtin, Version: ctx.Version(), Run: ctx.Run},
		{Name: "ctn", Source: registry.Builtin, Version: ctn.Version(), Run: ctn.Run},
	} {
		if err := reg.Register(c); err != nil {
			fmt.Fprintln(os.Stderr, "ext:", err)
			os.Exit(1)
		}
	}
	home := adapter.ConfigHome()
	manage := discoverPackages(reg, home)
	cfg, err := config.Load(home)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ext:", err)
		os.Exit(1)
	}
	for name, source := range cfg.Sources {
		if err := reg.Pin(name, registry.Source(source)); err != nil {
			fmt.Fprintln(os.Stderr, "ext:", err)
			os.Exit(1)
		}
	}
	os.Exit(app.RunWith(app.Options{Registry: reg, Package: manage}, os.Args[1:], os.Stdout, os.Stderr))
}

// discoverPackages registers the commands of the trusted packages installed under
// home/commands and returns the handler for "ext package". Nothing is created in
// home until a package is installed.
func discoverPackages(reg *registry.Registry, home string) func([]string, io.Writer, io.Writer) int {
	dir := filepath.Join(home, "commands")
	fail := func(err error) func([]string, io.Writer, io.Writer) int {
		reg.Note(err.Error())
		return func(_ []string, _, stderr io.Writer) int {
			fmt.Fprintln(stderr, "ext:", err)
			return 1
		}
	}
	trust, err := pkgcmd.LoadTrust(home)
	if err != nil {
		return fail(err)
	}
	packages, err := store.New(dir, store.Limits{})
	if err != nil {
		return fail(err)
	}
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		pkgcmd.Discover(reg, packages, trust, host.Options{})
	}
	return pkgcmd.Manager{Store: packages, Trust: trust}.Run
}

// Command ext runs ctx, ctn and any other registered command.
package main

import (
	"fmt"
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
	discoverPackages(reg, home)
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
	os.Exit(app.Run(reg, os.Args[1:], os.Stdout, os.Stderr))
}

// discoverPackages registers the commands of the trusted packages installed under
// home/commands. Without that directory there is nothing to discover and nothing is
// created.
func discoverPackages(reg *registry.Registry, home string) {
	dir := filepath.Join(home, "commands")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return
	}
	trust, err := pkgcmd.LoadTrust(home)
	if err != nil {
		reg.Note(err.Error())
		return
	}
	packages, err := store.New(dir, store.Limits{})
	if err != nil {
		reg.Note(err.Error())
		return
	}
	pkgcmd.Discover(reg, packages, trust, host.Options{})
}

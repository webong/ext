// Command ext runs ctx, ctn and any other registered command.
package main

import (
	"fmt"
	"os"

	"github.com/webong/ext/cmd/ext/internal/app"
	"github.com/webong/ext/cmd/ext/internal/config"
	"github.com/webong/ext/cmd/ext/internal/registry"
	"github.com/webong/ext/ctn"
	"github.com/webong/ext/ctx"
	"github.com/webong/ext/pkg/plugin-go/contract/host/adapter"
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
	cfg, err := config.Load(adapter.ConfigHome())
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

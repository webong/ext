// Command ext manages packages and runs dynamically registered commands.
//
// A Go project that uses ext-go adds ext as a tool for the build-time steps (go get -tool
// github.com/webong/ext/cmd/ext) and runs "go tool ext bundle fetch" and "go tool ext bundle bind" from go
// generate; ext-go itself has no command.
package main

import (
	"context"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cgoengine/plugin/host/commandscope"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/webong/ext/cmd/ext/internal/app"
	"github.com/webong/ext/cmd/ext/internal/bridgecmd"
	"github.com/webong/ext/cmd/ext/internal/bundlecmd"
	"github.com/webong/ext/cmd/ext/internal/config"
	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	"github.com/webong/ext/cmd/ext/internal/registry"
	"github.com/webong/ext/cmd/ext/internal/resourcecmd"
	"github.com/webong/ext/cmd/ext/internal/sourcecmd"
	"github.com/webong/ext/pkg/ext-go/cengine/plugin/host"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
)

func main() {
	if os.Getenv(commandscope.AddressEnv) != "" || os.Getenv(commandscope.TokenEnv) != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		code, err := commandscope.Relay(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ext:", err)
			code = 1
		}
		os.Exit(code)
	}

	// Protocol startup must not depend on installed packages or user configuration.
	if len(os.Args) > 1 && os.Args[1] == "bridge" {
		os.Exit(bridgecmd.Run(os.Args[2:]))
	}

	reg := registry.New()
	home := adapter.ConfigHome()
	for _, c := range []registry.Command{
		{Name: "resource", Source: registry.Builtin, Version: app.Version, Run: (resourcecmd.Manager{Home: home}).Run},
		{Name: "source", Source: registry.Builtin, Version: app.Version, Run: (sourcecmd.Manager{Home: home}).Run},
		{Name: "bundle", Source: registry.Builtin, Version: app.Version, Run: (bundlecmd.Manager{Home: home}).Run},
	} {
		if err := reg.Register(c); err != nil {
			fmt.Fprintln(os.Stderr, "ext:", err)
			os.Exit(1)
		}
	}
	if executable, err := os.Executable(); err == nil {
		pkgcmd.DiscoverBundled(reg, filepath.Join(filepath.Dir(executable), "ext-packages"))
	}
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
	if err := pkgcmd.RequireMigration(home); err != nil && len(os.Args) > 1 {
		switch os.Args[1] {
		case "package", "source", "bundle", "commands", "which", "version", "help", "--help", "-h":
		default:
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(app.RunWith(app.Options{Registry: reg, Package: manage}, os.Args[1:], os.Stdout, os.Stderr))
}

// discoverPackages registers the commands of the trusted packages installed under
// home/packages and returns the handler for "ext package". Nothing is created in
// home until a package is installed.
func discoverPackages(reg *registry.Registry, home string) func([]string, io.Writer, io.Writer) int {
	dir := filepath.Join(home, "packages")
	fail := func(err error) func([]string, io.Writer, io.Writer) int {
		reg.Note(err.Error())
		return func(_ []string, _, stderr io.Writer) int {
			fmt.Fprintln(stderr, "ext:", err)
			return 1
		}
	}

	if err := pkgcmd.RequireMigration(home); err != nil {
		reg.Note(err.Error())
		return func(args []string, stdout, stderr io.Writer) int {
			if len(args) == 1 && args[0] == "migrate" {
				return pkgcmd.RunMigration(home, stdout, stderr)
			}
			fmt.Fprintln(stderr, err)
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
	manager := pkgcmd.Manager{Store: packages, Trust: trust}
	return func(args []string, stdout, stderr io.Writer) int {
		if len(args) == 1 && args[0] == "migrate" {
			return pkgcmd.RunMigration(home, stdout, stderr)
		}
		return manager.Run(args, stdout, stderr)
	}
}

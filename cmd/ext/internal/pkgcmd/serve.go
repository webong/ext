package pkgcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/webong/ext/cmd/ext/internal/nativeplugin"
	"github.com/webong/ext/cmd/ext/internal/packagelock"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

const serveUsage = "ext package serve <installed-package-id> <contract>@<version>"

func (m Manager) serve(args []string, out, stderr io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(stderr, "usage:", serveUsage)
		return 2
	}
	name, version, _ := strings.Cut(args[1], "@")
	ref := plugin.ContractRef{Name: name, Version: version}
	if err := ref.Validate(); err != nil {
		fmt.Fprintln(stderr, "ext: package serve:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := m.servePeer(ctx, args[0], ref, out); err != nil {
		fmt.Fprintln(stderr, "ext: package serve:", err)
		return 1
	}
	return 0
}

func (m Manager) servePeer(ctx context.Context, id string, contract plugin.ContractRef, out io.Writer) (resultErr error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return plugin.ErrUnsupported
	}
	root, layout := m.Store.Root(), m.Store.Layout()
	home := filepath.Dir(root)
	release, err := packagelock.Acquire(home)
	if err != nil {
		return err
	}
	// The graph owns reviewed copies after startup. Service lifetime must not
	// hold the lock, since another host can share this package home.
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()
	if err := m.CheckInvocation(id); err != nil {
		return err
	}
	lib, err := nativeplugin.Open()
	if err != nil {
		return err
	}
	packages, err := lib.Open("store", map[string]any{"root": root, "layout": layout}, nil)
	if err != nil {
		return err
	}
	defer packages.Close()
	var manifest packagekit.Manifest
	if err := packages.Call("load", map[string]string{"id": id}, &manifest); err != nil {
		return err
	}
	reviewed := map[string]string{}
	graph, err := nativeplugin.OpenGraph(ctx, lib, packages, nativeplugin.GraphOptions{
		RootID: id, Contract: contract, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Layout: layout, LedgerPath: nativeplugin.LedgerPath(home),
		Review: func(manifest packagekit.Manifest) (string, error) {
			digest, err := nativeplugin.ReviewInstalled(lib, root, layout, manifest.Name())
			if err == nil {
				reviewed[manifest.Name()] = digest
			}
			return digest, err
		},
	})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, graph.Close()) }()
	release()
	locked = false
	return graph.ServePeer(ctx, manifest.Descriptor, contract, func() error {
		for id, approved := range reviewed {
			digest, err := nativeplugin.ReviewInstalled(lib, root, layout, id)
			if err != nil {
				return err
			}
			if approved != digest {
				return fmt.Errorf("reviewed package %s changed", id)
			}
		}
		return nil
	}, func() error {
		_, err := fmt.Fprintf(out, "serving %s %s@%s\n", id, contract.Name, contract.Version)
		return err
	})
}

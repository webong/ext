package pkgcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/webong/ext/cmd/ext/internal/packagelock"
	"github.com/webong/ext/cmd/ext/internal/sourcecmd"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/adapter/adaptercmd"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
)

// Manager installs, trusts and removes plugin packages, whether or not they provide commands.
type Manager struct {
	Store *store.Store
	Trust *Trust
}

const manageUsage = `usage: ext package <install|upgrade> <dir-or-archive> [--sha256 digest] | trust <id> | untrust <id> | remove <id> [--force] | list | inspect <id> | verify <id>
       ext package install|upgrade <directory> --manifest <package-relative-path>
       ext package plan [--without <id> | --with <sealed-package-dir>]
       ext package install|upgrade --source <name>
       ext package build|pack|index|test|doctor ...`

// Run executes "ext package ..." with args (without "package") and returns the
// exit code. Installing and upgrading never trust a package: the user trusts the
// installed content explicitly. Command registration is a separate consumer.
func (m Manager) Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, manageUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "install", "upgrade", "trust", "untrust", "remove", "add", "refresh":
		release, err := packagelock.Acquire(filepath.Dir(m.Store.Root()))
		if err != nil {
			fmt.Fprintln(stderr, "ext:", err)
			return 1
		}
		defer release()
	}
	if sub == "plan" {
		return m.plan(rest, stdout, stderr)
	}
	switch sub {
	case "available", "add", "refresh":
		fmt.Fprintln(stderr, "ext: legacy catalog activation has moved: use ext source import-catalog <directory>, ext source list, then ext package install --source <name> and explicit ext package trust <id>; source refresh never installs or trusts")
		return 2
	case "index":
		if len(rest) < 2 {
			fmt.Fprintln(stderr, "usage: ext package index <output> <archive>...")
			return 2
		}
		digest, err := writeIndex(rest[0], rest[1:])
		if err != nil {
			fmt.Fprintln(stderr, "ext: package index:", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote package index %s\nsha256: %s\n", rest[0], digest)
		return 0
	case "build", "pack", "test", "doctor":
		return (adaptercmd.Options{Name: "ext", Command: "package", Store: m.adapterStore()}).Run(args, stdout, stderr)
	}
	if sub == "ls" {
		sub = "list"
	}
	if sub == "list" {
		if len(rest) != 0 {
			fmt.Fprintln(stderr, manageUsage)
			return 2
		}
		return m.list(stdout, stderr)
	}
	if (sub == "install" || sub == "upgrade") && len(rest) == 3 && rest[1] == "--manifest" {
		if err := m.installWithManifest(sub == "upgrade", rest[0], rest[2], stdout); err != nil {
			fmt.Fprintf(stderr, "ext: package %s: %v\n", sub, err)
			return 1
		}
		return 0
	}
	if (sub == "install" || sub == "upgrade") && len(rest) == 2 && rest[0] == "--source" {
		archive, err := (sourcecmd.Manager{Home: filepath.Dir(m.Store.Root())}).Resolve(rest[1])
		if err != nil {
			fmt.Fprintln(stderr, "ext: package source:", err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		p, err := m.installArchive(ctx, archive)
		if err != nil {
			fmt.Fprintln(stderr, "ext: package source:", err)
			return 1
		}
		fmt.Fprintf(stdout, "installed %s from source %s; review it, then run: ext package trust %s\n", p.Name(), rest[1], p.Name())
		return 0
	}
	if (sub == "install" || sub == "upgrade") && len(rest) == 3 && rest[1] == "--sha256" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		p, err := m.installArchive(ctx, store.Archive{Source: rest[0], SHA256: rest[2]})
		if err != nil {
			fmt.Fprintf(stderr, "ext: package %s: %v\n", sub, err)
			return 1
		}
		fmt.Fprintf(stdout, "installed %s (untrusted); review it, then run: ext package trust %s\n", p.Name(), p.Name())
		return 0
	}
	if sub == "remove" && len(rest) > 0 {
		if _, err := m.adapterStore().Load(rest[0]); err == nil {
			if len(rest) != 1 && !(len(rest) == 2 && rest[1] == "--force") {
				fmt.Fprintln(stderr, manageUsage)
				return 2
			}
			if len(rest) == 1 {
				if err := m.checkRemoval(rest[0]); err != nil {
					fmt.Fprintln(stderr, "ext:", err)
					return 1
				}
			}
			code := (adaptercmd.Options{Name: "ext", Command: "package", Store: m.adapterStore()}).Run(args, stdout, stderr)
			if code == 0 {
				if err := m.Trust.Revoke(rest[0]); err != nil {
					fmt.Fprintln(stderr, err)
					return 1
				}
			}
			return code
		}
	}
	forceRemove := sub == "remove" && len(rest) == 2 && rest[1] == "--force"
	if forceRemove {
		rest = rest[:1]
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, manageUsage)
		return 2
	}
	var err error
	switch sub {
	case "install", "upgrade":
		err = m.install(sub == "upgrade", rest[0], stdout)
	case "inspect":
		var p store.Installed
		p, err = m.Store.Load(rest[0])
		if err == nil {
			err = json.NewEncoder(stdout).Encode(p.Manifest)
		}
	case "verify":
		var p store.Installed
		p, err = m.Store.Load(rest[0])
		if err == nil {
			var trusted bool
			trusted, err = m.Trust.Trusted(p)
			if err == nil {
				fmt.Fprintf(stdout, "verified %s (trusted=%t)\n", p.Name(), trusted)
			}
		}
	case "trust":
		err = m.trust(rest[0], stdout)
	case "untrust":
		if err = m.Trust.Revoke(rest[0]); err == nil {
			fmt.Fprintf(stdout, "%s is no longer trusted\n", rest[0])
		}
	case "remove":
		err = m.remove(rest[0], forceRemove, stdout)
	default:
		fmt.Fprintf(stderr, "ext: unknown package command %q\n%s\n", sub, manageUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "ext: package %s %s: %v\n", sub, rest[0], err)
		return 1
	}
	return 0
}

func (m Manager) install(upgrade bool, dir string, stdout io.Writer) error {
	var (
		p   store.Installed
		err error
	)
	if !upgrade {
		if source, adapterErr := adapter.LoadSource(dir, runtime.GOOS); adapterErr == nil {
			_, err = m.adapterStore().Install(dir)
			if err != nil {
				return err
			}
			p, err = m.Store.Load(source.Manifest.Name)
		} else {
			p, err = m.Store.Install(dir)
		}
	} else {
		if err := m.checkReplacement(dir); err != nil {
			return err
		}
		p, err = m.Store.UpgradeWithOptions(dir, store.CopyOptions{PreserveExecutableArtifacts: true})
	}
	if err != nil {
		return err
	}
	declared, declarationErr := Declare(p)
	fmt.Fprintf(stdout, "installed %s %s (not trusted)\n", p.Name(), p.Version())
	if declarationErr != nil {
		fmt.Fprintf(stdout, "commands unavailable: %v\n", declarationErr)
	} else if len(declared) > 0 {
		fmt.Fprintf(stdout, "commands: %s\n", names(declared))
	}
	fmt.Fprintf(stdout, "to trust this package: ext package trust %s\n", p.Name())
	return nil
}

func (m Manager) trust(id string, stdout io.Writer) error {
	if err := m.checkRequirements(); err != nil {
		return err
	}
	p, err := m.Store.Load(id)
	if err != nil {
		return err
	}
	if _, err := m.adapterStore().Load(id); err == nil {
		var diagnostic strings.Builder
		if code := (adaptercmd.Options{Name: "ext", Command: "package", Store: m.adapterStore()}).Run([]string{"trust", id}, stdout, &diagnostic); code != 0 {
			return fmt.Errorf("adapter activation: %s", strings.TrimSpace(diagnostic.String()))
		}
	}
	if err := m.Trust.Trust(p); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "trusted %s %s\n", p.Name(), p.Version())
	return nil
}

func (m Manager) checkRemoval(id string) error {
	installed, _, err := m.Store.ListUnverified()
	if err != nil {
		return err
	}
	for _, p := range installed {
		if p.Name() != id && hasRequirements(p.Manifest) {
			report, err := m.resolvePlan(id, "")
			if err != nil {
				return err
			}
			if report.Status != "ok" {
				return fmt.Errorf("removal leaves unresolved dependencies (%s); inspect ext package plan --without %s, or remove --force", report.Status, id)
			}
			break
		}
	}
	return nil
}

func (m Manager) remove(id string, force bool, stdout io.Writer) error {
	if !force {
		if err := m.checkRemoval(id); err != nil {
			return err
		}
	}

	if err := m.Store.Remove(id); err != nil {
		return err
	}
	if err := m.Trust.Revoke(id); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "removed %s\n", id)
	return nil
}

func (m Manager) list(stdout, stderr io.Writer) int {
	installed, problems, err := m.Store.ListUnverified()
	if err != nil {
		fmt.Fprintf(stderr, "ext: package list: %v\n", err)
		return 1
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PACKAGE\tVERSION\tTRUST\tCOMMANDS")
	for _, p := range installed {
		declared, derr := Declare(p)
		state := "untrusted"
		switch ok, terr := m.Trust.Trusted(p); {
		case terr != nil:
			state = "error"
		case ok:
			state = "trusted"
		case m.Trust.Packages[p.Name()] != "":
			state = "changed"
		}
		list := names(declared)
		if derr != nil {
			list = "invalid: " + derr.Error()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.Name(), p.Version(), state, list)
	}
	w.Flush()
	for _, c := range problems {
		if !c.OK() {
			fmt.Fprintf(stderr, "ext: package %s failed verification: %v\n", c.ID, c.Err)
		}
	}
	return 0
}

func names(declared []Declared) string {
	out := make([]string, len(declared))
	for i, d := range declared {
		out[i] = d.Name
	}
	return strings.Join(out, ", ")
}

func (m Manager) adapterStore() *adapter.Store {
	s := adapter.NewStore(m.Store.Root())
	s.TrustFile = m.Trust.file.Path
	return s
}

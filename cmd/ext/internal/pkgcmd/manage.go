package pkgcmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
)

// Manager installs, trusts and removes the packages that provide commands.
type Manager struct {
	Store *store.Store
	Trust *Trust
}

const manageUsage = `usage: ext package <install|upgrade> <dir> | trust <id> | untrust <id> | remove <id> | list`

// Run executes "ext package ..." with args (without "package") and returns the
// exit code. Installing and upgrading never trust a package: the user trusts the
// installed content explicitly, after seeing which commands it declares.
func (m Manager) Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, manageUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	if sub == "list" {
		if len(rest) != 0 {
			fmt.Fprintln(stderr, manageUsage)
			return 2
		}
		return m.list(stdout, stderr)
	}
	if len(rest) != 1 {
		fmt.Fprintln(stderr, manageUsage)
		return 2
	}
	var err error
	switch sub {
	case "install", "upgrade":
		err = m.install(sub == "upgrade", rest[0], stdout)
	case "trust":
		err = m.trust(rest[0], stdout)
	case "untrust":
		if err = m.Trust.Revoke(rest[0]); err == nil {
			fmt.Fprintf(stdout, "%s is no longer trusted\n", rest[0])
		}
	case "remove":
		err = m.remove(rest[0], stdout)
	default:
		fmt.Fprintf(stderr, "ext: unknown package command %q\n%s\n", sub, manageUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "ext: package %s: %v\n", sub, err)
		return 1
	}
	return 0
}

func (m Manager) install(upgrade bool, dir string, stdout io.Writer) error {
	var (
		p   store.Installed
		err error
	)
	if upgrade {
		p, err = m.Store.Upgrade(dir)
	} else {
		p, err = m.Store.Install(dir)
	}
	if err != nil {
		return err
	}
	declared, err := Declare(p)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "installed %s %s (not trusted)\n", p.Name(), p.Version())
	if len(declared) == 0 {
		fmt.Fprintln(stdout, "it declares no commands")
		return nil
	}
	fmt.Fprintf(stdout, "commands: %s\nto allow them to run: ext package trust %s\n", names(declared), p.Name())
	return nil
}

func (m Manager) trust(id string, stdout io.Writer) error {
	p, err := m.Store.Load(id)
	if err != nil {
		return err
	}
	declared, err := Declare(p)
	if err != nil {
		return err
	}
	if len(declared) == 0 {
		return fmt.Errorf("%s declares no commands, so there is nothing to trust", id)
	}
	if err := m.Trust.Trust(p); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "trusted %s %s; commands: %s\n", p.Name(), p.Version(), names(declared))
	return nil
}

func (m Manager) remove(id string, stdout io.Writer) error {
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

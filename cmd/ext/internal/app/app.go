// Package app is the ext command: it runs the command named by its first argument
// through the registry and answers questions about the registry itself.
package app

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/webong/ext/cmd/ext/internal/registry"
)

// Version is the version of ext itself; the release build sets it with -ldflags -X.
var Version = "0.1.0-dev"

// Run executes ext with args (without the program name) and returns the exit code.
func Run(reg *registry.Registry, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(reg, stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		usage(reg, stdout)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "ext %s\n", Version)
		for _, c := range reg.List() {
			fmt.Fprintf(stdout, "%s %s (%s)\n", c.Name, c.Version, c.Source)
		}
		return 0
	case "commands":
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "COMMAND\tSOURCE\tVERSION")
		for _, c := range reg.List() {
			fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, c.Source, c.Version)
		}
		w.Flush()
		return 0
	case "which":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ext: usage: ext which <command>")
			return 2
		}
		c, ok := reg.Lookup(args[1])
		if !ok {
			return unknown(reg, args[1], stderr)
		}
		fmt.Fprintf(stdout, "%s: %s %s\n", c.Name, c.Source, c.Version)
		return 0
	}
	c, ok := reg.Lookup(args[0])
	if !ok {
		return unknown(reg, args[0], stderr)
	}
	return c.Run(args[1:], stdout, stderr)
}

func unknown(reg *registry.Registry, name string, stderr io.Writer) int {
	if near, ok := reg.Closest(name); ok {
		fmt.Fprintf(stderr, "ext: unknown command %q; did you mean %q?\n", name, near)
	} else {
		fmt.Fprintf(stderr, "ext: unknown command %q; run \"ext commands\" to list them\n", name)
	}
	return 2
}

func usage(reg *registry.Registry, w io.Writer) {
	fmt.Fprintln(w, "usage: ext <command> [args]")
	fmt.Fprintln(w, "       ext commands | which <command> | version | help")
	fmt.Fprintln(w, "\ncommands:")
	for _, c := range reg.List() {
		fmt.Fprintf(w, "  %s\n", c.Name)
	}
}

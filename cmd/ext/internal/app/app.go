// Package app is the ext command: it runs the command named by its first argument
// through the registry and answers questions about the registry itself.
package app

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/webong/ext/cmd/ext/internal/registry"
)

// Version is the version of ext itself; the release build sets it with -ldflags -X.
var Version = "0.1.0-dev"

// Run executes ext with args (without the program name) and returns the exit code.
func Run(reg *registry.Registry, args []string, stdout, stderr io.Writer) int {
	source, args, err := splitSource(args)
	if err != nil {
		fmt.Fprintln(stderr, "ext:", err)
		return 2
	}
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
		for _, e := range reg.List() {
			fmt.Fprintf(stdout, "%s %s (%s)\n", e.Name, e.Version, e.Source)
		}
		return 0
	case "commands":
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "COMMAND\tSOURCE\tVERSION\tRUNS")
		for _, e := range reg.List() {
			runs := "shadowed"
			if e.Active {
				runs = "default"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, e.Source, e.Version, runs)
		}
		w.Flush()
		for _, n := range reg.Notes() {
			fmt.Fprintf(stderr, "ext: %s\n", n)
		}
		return 0
	case "which":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "ext: usage: ext [--source=builtin|package] which <command>")
			return 2
		}
		c, why, err := reg.Resolve(args[1], source)
		if err != nil {
			return failed(reg, args[1], err, stderr)
		}
		fmt.Fprintf(stdout, "%s: %s %s (chosen by %s)\n", c.Name, c.Source, c.Version, why)
		for _, e := range reg.List() {
			if e.Name == c.Name && e.Source != c.Source {
				fmt.Fprintf(stdout, "also: %s %s\n", e.Source, e.Version)
			}
		}
		return 0
	}
	c, _, err := reg.Resolve(args[0], source)
	if err != nil {
		return failed(reg, args[0], err, stderr)
	}
	return c.Run(args[1:], stdout, stderr)
}

// splitSource takes a leading --source=<s> or --source <s> off args.
func splitSource(args []string) (registry.Source, []string, error) {
	if len(args) == 0 {
		return "", args, nil
	}
	switch {
	case strings.HasPrefix(args[0], "--source="):
		return registry.Source(strings.TrimPrefix(args[0], "--source=")), args[1:], nil
	case args[0] == "--source":
		if len(args) < 2 {
			return "", nil, errors.New("--source needs a value")
		}
		return registry.Source(args[1]), args[2:], nil
	}
	return "", args, nil
}

func failed(reg *registry.Registry, name string, err error, stderr io.Writer) int {
	if !errors.Is(err, registry.ErrUnknown) {
		fmt.Fprintln(stderr, "ext:", err)
		return 2
	}
	if near, ok := reg.Closest(name); ok {
		fmt.Fprintf(stderr, "ext: unknown command %q; did you mean %q?\n", name, near)
	} else {
		fmt.Fprintf(stderr, "ext: unknown command %q; run \"ext commands\" to list them\n", name)
	}
	return 2
}

func usage(reg *registry.Registry, w io.Writer) {
	fmt.Fprintln(w, "usage: ext [--source=builtin|package] <command> [args]")
	fmt.Fprintln(w, "       ext commands | which <command> | version | help")
	fmt.Fprintln(w, "\ncommands:")
	seen := map[string]bool{}
	for _, e := range reg.List() {
		if !seen[e.Name] {
			seen[e.Name] = true
			fmt.Fprintf(w, "  %s\n", e.Name)
		}
	}
}

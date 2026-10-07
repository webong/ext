// Package app is the ctn command: content management.
package app

import (
	"fmt"
	"io"
)

var Version = "0.0.0-dev"

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-V" {
		fmt.Fprintf(stdout, "ctn %s\n", Version)
		return 0
	}
	if args[0] == "adapter" {
		return adapterCommand(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "ctn: unknown command %q\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "ctn manages content.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  ctn adapter ls")
	fmt.Fprintln(w, "  ctn help")
	fmt.Fprintln(w, "  ctn version")
}

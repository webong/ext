// Package app is the ctn command: content management.
package app

import (
	"fmt"
	"io"
)

var Version = "0.2.0-dev"

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
	if args[0] == "verify" {
		return verifyCommand(args[1:], stdout, stderr)
	}
	if args[0] == "model" {
		return modelCommand(args[1:], stdout, stderr)
	}
	if args[0] == "content" {
		return contentCommand(args[1:], stdout, stderr)
	}
	if args[0] == "inference" {
		return inferenceCommand(args[1:], stdout, stderr)
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
	fmt.Fprintln(w, "  ctn verify [--engine NAME] [--selection SEL] [--timeout D] [--json] FILE [ARGUMENTS...]")
	fmt.Fprintln(w, "  ctn content init ROOT")
	fmt.Fprintln(w, "  ctn content import ROOT FILE")
	fmt.Fprintln(w, "  ctn content list|inspect|verify|remove ROOT [ID]")
	fmt.Fprintln(w, "  ctn model list <root-dir>")
	fmt.Fprintln(w, "  ctn model inspect <root-dir> <provider/name@version>")
	fmt.Fprintln(w, "  ctn model install <root-dir> <manifest-file> <artifacts-dir>")
	fmt.Fprintln(w, "  ctn model remove <root-dir> <provider/name@version>")
	fmt.Fprintln(w, "  ctn model verify <root-dir> <provider/name@version>")
	fmt.Fprintln(w, "  ctn model provider package [--os OS --arch ARCH] ID VERSION BINARY OUTPUT_DIR")
	fmt.Fprintln(w, "  ctn model provider install <sealed-package-dir>")
	fmt.Fprintln(w, "  ctn model provider list")
	fmt.Fprintln(w, "  ctn model provider trust|untrust ID")
	fmt.Fprintln(w, "  ctn model run --provider ID [--timeout D] [--output FILE] <root-dir> <provider/name@version> <input-file>")
	fmt.Fprintln(w, "  ctn inference package [--os OS --arch ARCH] <id> <version> <binary> <output-dir>")
	fmt.Fprintln(w, "  ctn inference install <sealed-package-dir>")
	fmt.Fprintln(w, "  ctn inference trust|untrust ID")
	fmt.Fprintln(w, "  ctn inference providers")
	fmt.Fprintln(w, "  ctn inference capabilities --provider ID")
	fmt.Fprintln(w, "  ctn inference health --provider ID")
	fmt.Fprintln(w, "  ctn inference generate --provider ID [--model M] [--max-tokens N] [--input FILE]")
	fmt.Fprintln(w, "  ctn inference stream --provider ID [--model M] [--input FILE]")
	fmt.Fprintln(w, "  ctn inference embed --provider ID --model M --input FILE")
	fmt.Fprintln(w, "  ctn inference transcribe --provider ID --model M --input FILE")
	fmt.Fprintln(w, "  ctn inference synthesize --provider ID --model M --input FILE")
	fmt.Fprintln(w, "  ctn inference models --provider ID")
	fmt.Fprintln(w, "  ctn help")
	fmt.Fprintln(w, "  ctn version")
}

package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/webong/ext/src/ctx/internal/config"
)

// computerHook bridges a computer-side native hook to the adapter that owns
// its event schema and response contract.
func computerHook(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[1] == "" || strings.ContainsAny(args[1], "\r\n") {
		fmt.Fprintln(stderr, "ctx: hook computer needs an adapter name and event")
		return 2
	}
	resolver, err := newResolver()
	if err != nil {
		return reportError(stderr, err)
	}
	candidate, err := adapterStore().Load(args[0])
	if err != nil || !candidate.IsComputerEndpoint() {
		fmt.Fprintf(stderr, "ctx: %s has no computer endpoint\n", args[0])
		return 2
	}
	if !candidate.HasComputerCapability("hook") {
		fmt.Fprintf(stderr, "ctx: computer endpoint %s does not provide hooks\n", candidate.Manifest.Name)
		return 2
	}
	return invokeAdapterIO(resolver, candidate, "hook", "", []string{args[1]}, "", os.Stdin, stdout, stderr)
}

func computerPlugin(resolver *config.Resolver, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "ctx: plugin computer needs an adapter name")
		return 2
	}
	candidate, err := adapterStore().Load(args[0])
	if err != nil || !candidate.IsComputerEndpoint() {
		fmt.Fprintf(stderr, "ctx: %s has no computer endpoint\n", args[0])
		return 2
	}
	if !candidate.HasComputerCapability("plugin") {
		fmt.Fprintf(stderr, "ctx: computer endpoint %s does not manage plugins\n", candidate.Manifest.Name)
		return 2
	}
	return invokeAdapter(resolver, candidate, "plugin", "", args[1:], "", stdout, stderr)
}

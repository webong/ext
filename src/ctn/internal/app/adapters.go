package app

import (
	"fmt"
	"io"

	"github.com/webong/ext/pkg/plugin/adapter"
)

// adapterCommand lists the adapters installed for this user. The store and the
// trust decisions are shared with every other ext product, so an adapter that
// was installed and trusted through ctx is already available here.
func adapterCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "ls" {
		fmt.Fprintln(stderr, "ctn: usage: ctn adapter ls")
		return 2
	}
	store := adapter.NewStore(adapter.Home())
	installed, err := store.List()
	if err != nil {
		fmt.Fprintf(stderr, "ctn: %v\n", err)
		return 1
	}
	for _, a := range installed {
		state := "untrusted"
		if ok, err := store.IsTrusted(a); err == nil && ok {
			state = "trusted"
		}
		known := ""
		if !a.IsKnownRuntime() {
			known = " (runtime unknown to this host)"
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s%s\n", a.Manifest.Name, a.Manifest.Runtime, state, known)
	}
	return 0
}

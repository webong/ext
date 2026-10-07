package main

import (
	"fmt"
	"os"

	"github.com/webong/ext/adapters/keychain/store"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/res/credential/adapterkit"
)

func main() {
	request, err := plugin.ParseAdapterInvocation(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx: %v\n", err)
		os.Exit(2)
	}
	os.Exit(adapterkit.Run(adapterkit.Invocation{Operation: request.Operation, Selection: request.Selection, Arguments: request.Arguments}, os.Stdin, os.Stdout, os.Stderr, store.Keychain{Path: os.Getenv("CTX_KEYCHAIN_PATH")}))
}

package main

import (
	"fmt"
	"os"

	"github.com/webong/ctx/adapters/credman/store"
	"github.com/webong/ctx/pkg/adapter"
	"github.com/webong/ctx/res/credential/adapterkit"
)

func main() {
	request, err := adapter.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx: %v\n", err)
		os.Exit(2)
	}
	os.Exit(adapterkit.Run(adapterkit.Invocation{Operation: request.Operation, Selection: request.Selection, Arguments: request.Arguments}, os.Stdin, os.Stdout, os.Stderr, store.CredentialManager{}))
}

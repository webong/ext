package main

import (
	"os"

	"github.com/webong/ctx/adapters/keychain/store"
	"github.com/webong/ctx/res/credential/adapterkit"
)

func main() {
	os.Exit(adapterkit.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, store.Keychain{Path: os.Getenv("CTX_KEYCHAIN_PATH")}))
}

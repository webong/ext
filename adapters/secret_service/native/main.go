package main

import (
	"os"

	"github.com/webong/ctx/adapters/secret_service/store"
	"github.com/webong/ctx/res/credential/adapterkit"
)

func main() {
	os.Exit(adapterkit.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, store.SecretService{}))
}

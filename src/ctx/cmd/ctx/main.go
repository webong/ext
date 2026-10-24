package main

import (
	"os"

	"github.com/webong/ext/ctx"
)

func main() {
	os.Exit(ctx.Run(os.Args[1:], os.Stdout, os.Stderr))
}

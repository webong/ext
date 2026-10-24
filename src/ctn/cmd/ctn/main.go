package main

import (
	"os"

	"github.com/webong/ext/ctn"
)

func main() {
	os.Exit(ctn.Run(os.Args[1:], os.Stdout, os.Stderr))
}

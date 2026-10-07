package main

import (
	firefox "github.com/webong/ext/adapters/firefox/engine"
	"os"
)

func main() {
	config := firefox.Config{
		Name: "zen", MacProfileRoot: "zen", WindowsProfileRoot: "zen", LinuxProfileRoot: ".zen",
	}
	os.Exit(firefox.Run(config, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

package main

import (
	"io"
	"os"

	firefox "github.com/webong/ext/adapters/firefox/engine"
	"github.com/webong/ext/res/browser/discovery"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, input io.Reader, stdout, stderr io.Writer) int {
	return firefox.Run(firefox.Config{
		Name: "firefox", MacProfileRoot: "Firefox", WindowsProfileRoot: "Mozilla/Firefox",
		LinuxProfileRoot: ".mozilla/firefox", LinuxFallbackProfileRoot: "snap/firefox/common/.mozilla/firefox",
		NativeExtensions: true,
		ExtensionExecutables: discovery.ExecutableLocations{
			Darwin:  []string{"Firefox.app/Contents/MacOS/firefox"},
			Linux:   []string{"firefox"},
			Windows: []string{"Mozilla Firefox/firefox.exe"},
		},
	}, args, input, stdout, stderr)
}

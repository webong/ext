// Command gen extracts guest exports and normalizes aliases sharing a package name.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"regexp"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func generate() error {
	packages := []string{"core/plugin", "engine/plugin", "engine/plugin/guest/author", "engine/plugin/guest/capability", "engine/plugin/guest/instance", "engine/plugin/guest/stream", "engine/plugin/transport/inprocess", "engine/plugin/validate/schema"}
	args := []string{"run", "github.com/traefik/yaegi/cmd/yaegi", "extract", "-name", "symbols"}
	for _, p := range packages {
		args = append(args, "github.com/webong/ext/pkg/ext-go/"+p)
	}
	command := exec.Command("go", args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return err
	}
	path := "github_com-webong-ext-pkg-ext-go-engine-plugin.go"
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// The extractor imports both packages as plugin for type aliases. Every core
	// type it uses here is re-exported by engine/plugin, so retain that one import.
	data = bytes.Replace(data, []byte("\t\"github.com/webong/ext/pkg/ext-go/core/plugin\"\n"), nil, 1)
	// An interpreted guest gets the guest SDK, never the legacy host oracle.
	data = regexp.MustCompile(`(?m)^\s*"(?:Open|Session)":[^\n]*\n`).ReplaceAll(data, nil)
	data, err = format.Source(data)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

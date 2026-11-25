// package-product writes the manifest for one first-party command package.
// Usage: package-product PRODUCT VERSION GOOS GOARCH EXECUTABLE
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webong/ext/cmd/ext/internal/pkgcmd"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/store"
	"github.com/webong/ext/pkg/ext-go/engine/plugin/host/storelayout"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

func main() {
	if len(os.Args) != 6 {
		fmt.Fprintln(os.Stderr, "usage: package-product PRODUCT VERSION GOOS GOARCH EXECUTABLE")
		os.Exit(2)
	}
	product, version, goos, goarch, executable := os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5]
	if product != "ctx" && product != "ctn" && product != "cto" {
		fmt.Fprintln(os.Stderr, "unknown first-party product", product)
		os.Exit(2)
	}
	m, err := packagekit.ExecutableManifest("ext/"+product, version, []plugin.Contract{pkgcmd.RunContract}, executable)
	if err == nil {
		m.Descriptor.Identity.Revision = "command-v1"
		m.Artifacts[0].OS, m.Artifacts[0].Arch = goos, goarch
		commands := []pkgcmd.Declared{{Name: product, Transport: "socket"}}
		if product == "cto" {
			commands[0].FDFlags = []string{"--launch-fd", "--status-fd"}
			commands = append(commands, pkgcmd.Declared{Name: "sandbox", Transport: "socket", FDFlags: []string{"--launch-fd", "--status-fd"}})
		}
		var data []byte
		data, err = json.Marshal(map[string]any{"commands": commands})
		if err == nil {
			m.Metadata = map[string]json.RawMessage{pkgcmd.MetadataKey: data}
			err = store.WriteManifestAt(filepath.Dir(executable), storelayout.ExtManifest, m)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

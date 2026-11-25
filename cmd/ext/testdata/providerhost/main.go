// A software consumer of a non-command package through the described C engine.
package main

import (
	"encoding/json"
	"fmt"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
	"os"
	"path/filepath"
	"runtime"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 4 && (os.Args[3] == "chain" || os.Args[3] == "chain-closed" || os.Args[3] == "chain-denied") {
		return runChain()
	}
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: providerhost library-path ext-home")
	}
	native, err := library.Open(os.Args[1])
	if err != nil {
		return err
	}
	api, err := library.BindInterface(native, "ext_plugin", 1)
	if err != nil {
		return err
	}
	lib, err := library.Describe(api)
	if err != nil {
		return err
	}
	writer, err := lib.Open("package_writer", map[string]any{"root": os.Args[2]}, nil)
	if err != nil {
		return err
	}
	defer writer.Close()
	root := filepath.Join(os.Args[2], "packages")
	id := map[string]any{"id": "example/provider"}
	var digest string
	if err := lib.Library().Call("store_digest", map[string]any{"root": root, "id": "example/provider"}, &digest); err != nil {
		return err
	}
	var state string
	if err := lib.Library().Call("trust_state", map[string]any{"path": filepath.Join(root, ".package-trust.json"), "scope": "ext.command/v1", "id": "example/provider", "digest": digest}, &state); err != nil {
		return err
	}
	// This test consumer explicitly accepts package host approval. Domain consumers
	// can require their own scope; installing a package never supplies this grant.
	if state != "trusted" {
		return fmt.Errorf("provider not trusted: %s", state)
	}
	store, err := lib.Open("store", map[string]any{"root": root}, nil)
	if err != nil {
		return err
	}
	defer store.Close()
	var manifest struct {
		Descriptor json.RawMessage `json:"descriptor"`
	}
	if err := store.Call("load", id, &manifest); err != nil {
		return err
	}
	var entry struct {
		Path string `json:"path"`
	}
	if err := store.Call("resolve_entrypoint", map[string]any{"id": "example/provider", "contract": "example.value", "version": "v1", "protocol": "ext.plugin/v1", "reviewed_digest": digest, "os": runtime.GOOS, "arch": runtime.GOARCH}, &entry); err != nil {
		return err
	}
	host, err := lib.Open("host", map[string]any{"descriptor": manifest.Descriptor, "backend": map[string]any{"kind": "process", "executable": entry.Path}, "verify": "allow", "authorize": "allow"}, nil)
	if err != nil {
		return err
	}
	defer host.Close()
	if err := host.Call("start", map[string]any{"timeout_ms": 5000}, nil); err != nil {
		return err
	}
	var result json.RawMessage
	if err := host.Call("call", map[string]any{"call": map[string]any{"contract": map[string]string{"name": "example.value", "version": "v1"}, "operation": "read"}, "timeout_ms": 5000}, &result); err != nil {
		return err
	}
	fmt.Println(string(result))
	return nil
}

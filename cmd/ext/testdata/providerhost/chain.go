package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/plugin/package"
)

// This is an acceptance consumer, not a general routing implementation. The
// application explicitly grants one declared operation, after resolving it in C.
// The native registry forwards it, retaining deadline and cancellation state.
func runChain() error {
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
	store, err := lib.Open("store", map[string]any{"root": root}, nil)
	if err != nil {
		return err
	}
	defer store.Close()
	var rows []struct {
		Manifest packagekit.Manifest `json:"manifest"`
	}
	if err := store.Call("list", nil, &rows); err != nil {
		return err
	}
	manifests := make([]packagekit.Manifest, 0, len(rows))
	byID := map[string]packagekit.Manifest{}
	for _, r := range rows {
		manifests = append(manifests, r.Manifest)
		byID[r.Manifest.Descriptor.Identity.ID] = r.Manifest
	}
	consumer, ok := byID["example/dependent"]
	if !ok {
		return fmt.Errorf("dependent missing")
	}
	ref := plugin.ContractRef{Name: "example.value", Version: "v1"}
	var routes []struct {
		Contract  plugin.ContractRef
		Operation string
		Provider  plugin.Identity
	}
	if err := lib.Library().Call("package.routes", map[string]any{"packages": manifests, "os": runtime.GOOS, "arch": runtime.GOARCH, "consumer": consumer.Descriptor.Identity}, &routes); err != nil {
		return err
	}
	var provider plugin.Identity
	for _, b := range routes {
		if b.Contract == ref && b.Operation == "read" {
			provider = b.Provider
		}
	}
	if provider.ID == "" {
		return fmt.Errorf("declared dependency binding missing")
	}
	// Check the complete two-package authority boundary before either launch.
	digests := map[string]string{}
	for _, id := range []string{consumer.Descriptor.Identity.ID, provider.ID} {
		var digest, state string
		if err := lib.Library().Call("store_digest", map[string]any{"root": root, "id": id}, &digest); err != nil {
			return err
		}
		if err := lib.Library().Call("trust_state", map[string]any{"path": filepath.Join(root, ".package-trust.json"), "scope": "ext.command/v1", "id": id, "digest": digest}, &state); err != nil {
			return err
		}
		if state != "trusted" {
			return fmt.Errorf("%s not trusted: %s", id, state)
		}
		digests[id] = digest
	}
	open := func(id string, contract string, handlers library.Handlers, routes []any) (*library.Object, error) {
		var entry struct {
			Path string `json:"path"`
		}
		if err := store.Call("resolve_entrypoint", map[string]any{"id": id, "contract": contract, "version": "v1", "protocol": "ext.plugin/v1", "reviewed_digest": digests[id], "os": runtime.GOOS, "arch": runtime.GOARCH}, &entry); err != nil {
			return nil, err
		}
		cfg := map[string]any{"descriptor": byID[id].Descriptor, "backend": map[string]any{"kind": "process", "executable": entry.Path}, "verify": "allow", "authorize": "allow"}
		if len(routes) > 0 {
			cfg["routes"] = routes
		}
		if handlers != nil {
			cfg["services"] = map[string]any{"timeout_ms": 5000}
		}
		h, err := lib.Open("host", cfg, handlers)
		if err != nil {
			return nil, err
		}
		if err := h.Call("start", map[string]any{"timeout_ms": 5000}, nil); err != nil {
			h.Close()
			return nil, err
		}
		return h, nil
	}
	target, err := open(provider.ID, ref.Name, nil, nil)
	if err != nil {
		return err
	}
	defer target.Close()
	var calls atomic.Int32
	check := func(data json.RawMessage) (any, error) {
		var req plugin.Request
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		if req.Contract != ref || req.Operation != "read" {
			return nil, fmt.Errorf("undeclared route")
		}
		return nil, nil
	}
	h, err := open(consumer.Descriptor.Identity.ID, "example.dependent", library.Handlers{
		"reverse_authorize": func(data json.RawMessage) (any, error) {
			if os.Args[3] == "chain-denied" {
				return nil, fmt.Errorf("application denied reverse call")
			}
			if _, err := check(data); err != nil {
				return nil, err
			}
			calls.Add(1)
			return nil, nil
		},
	}, []any{map[string]any{"contract": ref, "operation": "read", "target": target.Handle()}})
	if err != nil {
		return err
	}
	defer h.Close()
	if os.Args[3] == "chain-closed" {
		if err := target.Close(); err != nil {
			return err
		}
	}
	var response plugin.Response
	if err := h.Call("call", map[string]any{"call": map[string]any{"contract": map[string]string{"name": "example.dependent", "version": "v1"}, "operation": "read"}, "timeout_ms": 5000}, &response); err != nil {
		return err
	}
	if response.Error != nil {
		return response.Error
	}
	if calls.Load() != 1 {
		return fmt.Errorf("expected one authorized reverse call, got %d", calls.Load())
	}
	fmt.Println(string(response.Payload))
	return nil
}

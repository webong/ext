// Package graphapi supplies CTX's storage and library-location policy. All live
// graph operations use the ext-go binding to the C ledger.
package graphapi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	model "github.com/webong/ext/pkg/ext-go/cengine/ledger"
	"github.com/webong/ext/pkg/ext-go/cengine/library"
)

type Graph struct{ *model.Graph }
type HostSnapshot = model.HostSnapshot
type HostCandidates = model.HostCandidates
type WebviewValidator = model.WebviewValidator
type HostRequirementError = model.HostRequirementError

const Namespace = model.Namespace
const Directory = "ledger"

type AdapterObservation = model.AdapterObservation
type AliasObservation = model.AliasObservation
type InstallerObservation = model.InstallerObservation
type ContextObservation = model.ContextObservation
type ResourceObservation = model.ResourceObservation
type RelationObservation = model.RelationObservation
type ContextCandidate = model.ContextCandidate
type Inventory = model.Inventory
type HostInventory = model.HostInventory
type OperationRequirements = model.OperationRequirements
type ShellRequirement = model.ShellRequirement
type FilesystemRequirement = model.FilesystemRequirement
type WebviewRequirement = model.WebviewRequirement
type PreparedHost = model.PreparedHost

var SafeOrigin = model.SafeOrigin

func Library() (*model.Library, error) {
	var lib *library.Library
	var err error
	if path := os.Getenv("EXT_LEDGER_LIBRARY"); path != "" {
		lib, err = library.Open(path)
	} else if _, ok := library.Provided("ext_ledger"); ok {
		lib, err = library.OpenProvided("ext_ledger", "")
	} else {
		exe, e := os.Executable()
		if e != nil {
			return nil, e
		}
		name := "libext_ledger.so"
		if runtime.GOOS == "darwin" {
			name = "libext_ledger.dylib"
		}
		if runtime.GOOS == "windows" {
			name = "ext_ledger.dll"
		}
		lib, err = library.Open(filepath.Join(filepath.Dir(exe), "ext-lib", name))
	}
	if err != nil {
		return nil, fmt.Errorf("C ledger unavailable (set EXT_LEDGER_LIBRARY): %w", err)
	}
	return model.Bind(lib)
}
func Open(home string) (*Graph, error) {
	lib, err := Library()
	if err != nil {
		return nil, err
	}
	store, err := lib.OpenStore(context.Background(), "file", filepath.Join(home, Directory), model.Options{})
	if err != nil {
		return nil, err
	}
	g, err := model.NewGraph(context.Background(), store)
	if err != nil {
		store.Close()
	}
	if err != nil {
		return nil, err
	}
	return &Graph{Graph: g}, nil
}
func NewMemory() (*Graph, error) {
	lib, err := Library()
	if err != nil {
		return nil, err
	}
	s, err := lib.OpenStore(context.Background(), "memory", "", model.Options{})
	if err != nil {
		return nil, err
	}
	g, err := model.NewGraph(context.Background(), s)
	if err != nil {
		s.Close()
	}
	if err != nil {
		return nil, err
	}
	return &Graph{Graph: g}, nil
}

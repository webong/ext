//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

/*
#include "ext_host.h"
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"math"
	"strings"
	"sync"
	"unsafe"
)

// PackageStore is the C engine's package store: it installs, upgrades, replaces, removes and loads
// reviewed ext.package/v1 packages under a root the caller supplies, the way pkg/ext-go/engine/host/store
// does. A missing root is an empty store, made by the first install. Calls on one store are
// serialized; several processes mutating one root are the caller's to serialize. In a build without
// the C engine the same type is served by the pure-Go store.
type PackageStore struct {
	mu  sync.RWMutex
	ptr *C.ext_store
}

// OpenPackageStore returns a store rooted at root.
func OpenPackageStore(root string, limits PackageStoreLimits) (*PackageStore, error) {
	if root == "" || strings.IndexByte(root, 0) >= 0 || limits.MaxArtifactBytes < 0 || limits.MaxTreeFiles < 0 ||
		limits.MaxTreeBytes < 0 || int64(limits.MaxTreeFiles) > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	croot := C.CString(root)
	defer C.free(unsafe.Pointer(croot))
	cl := C.ext_store_limits{
		max_artifact_bytes: C.uint64_t(limits.MaxArtifactBytes),
		max_tree_files:     C.uint32_t(limits.MaxTreeFiles),
		max_tree_bytes:     C.uint64_t(limits.MaxTreeBytes),
	}
	var ptr *C.ext_store
	if err := status(C.ext_store_open(croot, &cl, &ptr)); err != nil {
		return nil, err
	}
	return &PackageStore{ptr: ptr}, nil
}

// Close releases the store. A closed store refuses every call with plugin.ErrClosed.
func (s *PackageStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptr != nil {
		C.ext_store_close(s.ptr)
		s.ptr = nil
	}
}

func takeBuffer(b *C.ext_buffer) []byte {
	if b.data == nil {
		return nil
	}
	out := C.GoBytes(unsafe.Pointer(b.data), C.int(b.len))
	C.ext_buffer_free(b)
	return out
}

const (
	storeInstall = iota
	storeUpgrade
	storeReplace
)

func (s *PackageStore) put(op int, source string, preserveExecutable bool) ([]byte, error) {
	if source == "" || strings.IndexByte(source, 0) >= 0 {
		return nil, plugin.ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return nil, plugin.ErrClosed
	}
	csource := C.CString(source)
	defer C.free(unsafe.Pointer(csource))
	var flags C.uint32_t
	if preserveExecutable {
		flags = C.EXT_STORE_PRESERVE_EXECUTABLE
	}
	var manifest C.ext_buffer
	var st C.ext_status
	switch op {
	case storeInstall:
		st = C.ext_store_install(s.ptr, csource, flags, &manifest)
	case storeUpgrade:
		st = C.ext_store_upgrade(s.ptr, csource, flags, &manifest)
	default:
		st = C.ext_store_replace(s.ptr, csource, flags, &manifest)
	}
	if err := status(st); err != nil {
		return nil, err
	}
	return takeBuffer(&manifest), nil
}

// Install copies the package in source into the store, never over an installed id, and returns the
// installed plugin.json, byte for byte as the source had it. With preserveExecutable a file
// artifact that is executable in the source stays so (entrypoints and executable files in trees
// always do).
func (s *PackageStore) Install(source string, preserveExecutable bool) ([]byte, error) {
	return s.put(storeInstall, source, preserveExecutable)
}

// Upgrade replaces an installed package with a new revision of the same id and contracts. The
// previous installation survives any failure.
func (s *PackageStore) Upgrade(source string, preserveExecutable bool) ([]byte, error) {
	return s.put(storeUpgrade, source, preserveExecutable)
}

// Replace installs or replaces the package of the source's id, whatever its contracts.
func (s *PackageStore) Replace(source string, preserveExecutable bool) ([]byte, error) {
	return s.put(storeReplace, source, preserveExecutable)
}

// Remove deletes one installed package. It returns plugin.ErrNotFound for an id that is not installed.
func (s *PackageStore) Remove(id string) error {
	if strings.IndexByte(id, 0) >= 0 {
		return plugin.ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return plugin.ErrClosed
	}
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	return status(C.ext_store_remove(s.ptr, cid))
}

// Load reads and verifies one installed package and returns its plugin.json.
func (s *PackageStore) Load(id string) ([]byte, error) {
	if strings.IndexByte(id, 0) >= 0 {
		return nil, plugin.ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return nil, plugin.ErrClosed
	}
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	var manifest C.ext_buffer
	if err := status(C.ext_store_load(s.ptr, cid, &manifest)); err != nil {
		return nil, err
	}
	return takeBuffer(&manifest), nil
}

// IDs lists the installed package ids in byte order, without reading or verifying any package.
// Staging directories are skipped; any other entry that is not a package directory is plugin.ErrInvalid.
func (s *PackageStore) IDs() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return nil, plugin.ErrClosed
	}
	var buf C.ext_buffer
	if err := status(C.ext_store_ids(s.ptr, &buf)); err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal(takeBuffer(&buf), &ids); err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

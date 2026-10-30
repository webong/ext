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
	"path/filepath"
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

// reportLine is one line of a store report: a package (with its plugin.json) or an entry that
// failed (with the engine status it failed with).
type reportLine struct {
	ID        string          `json:"id"`
	Directory string          `json:"directory"`
	Layer     int             `json:"layer"`
	Manifest  json.RawMessage `json:"manifest"`
	Status    int             `json:"status"`
}

func (l reportLine) record() PackageRecord {
	return PackageRecord{ID: l.ID, Directory: l.Directory, Layer: l.Layer, Manifest: append([]byte(nil), l.Manifest...)}
}

func (l reportLine) check() PackageCheck {
	return PackageCheck{ID: l.ID, Directory: l.Directory, Layer: l.Layer, Err: status(C.ext_status(l.Status))}
}

func decodeRecords(raw []byte) ([]PackageRecord, error) {
	var lines []reportLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	out := make([]PackageRecord, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.record())
	}
	return out, nil
}

func decodeChecks(raw []byte) ([]PackageCheck, error) {
	var lines []reportLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	out := make([]PackageCheck, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.check())
	}
	return out, nil
}

func decodeUnverified(raw []byte) ([]PackageRecord, []PackageCheck, error) {
	var listing struct {
		Packages []reportLine `json:"packages"`
		Problems []reportLine `json:"problems"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return nil, nil, err
	}
	records := make([]PackageRecord, 0, len(listing.Packages))
	for _, l := range listing.Packages {
		records = append(records, l.record())
	}
	checks := make([]PackageCheck, 0, len(listing.Problems))
	for _, l := range listing.Problems {
		checks = append(checks, l.check())
	}
	return records, checks, nil
}

// report runs one of the store's whole-root reports and returns its JSON.
func (s *PackageStore) report(call func(*C.ext_store, *C.ext_buffer) C.ext_status) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ptr == nil {
		return nil, plugin.ErrClosed
	}
	var buf C.ext_buffer
	if err := status(call(s.ptr, &buf)); err != nil {
		return nil, err
	}
	return takeBuffer(&buf), nil
}

// List reads and verifies every installed package and returns them sorted by id. It fails on the
// first entry that is not a package directory and on the first package that does not verify.
func (s *PackageStore) List() ([]PackageRecord, error) {
	raw, err := s.report(func(p *C.ext_store, b *C.ext_buffer) C.ext_status { return C.ext_store_list(p, b) })
	if err != nil {
		return nil, err
	}
	return decodeRecords(raw)
}

// Verify checks every entry below the root and reports each one, sorted by id, instead of stopping
// at the first bad one. It runs nothing and changes nothing.
func (s *PackageStore) Verify() ([]PackageCheck, error) {
	raw, err := s.report(func(p *C.ext_store, b *C.ext_buffer) C.ext_status { return C.ext_store_verify(p, b) })
	if err != nil {
		return nil, err
	}
	return decodeChecks(raw)
}

// ListUnverified returns the packages whose manifest reads and checks against its directory, and
// separately the entries that could not be read. No artifact is read or hashed: THE PACKAGES ARE
// UNVERIFIED and must not be launched or trusted until Load or a host verifies them.
func (s *PackageStore) ListUnverified() ([]PackageRecord, []PackageCheck, error) {
	raw, err := s.report(func(p *C.ext_store, b *C.ext_buffer) C.ext_status { return C.ext_store_list_unverified(p, b) })
	if err != nil {
		return nil, nil, err
	}
	return decodeUnverified(raw)
}

// LayeredPackageStore is a read-only view over several package roots in priority order, the way
// pkg/ext-go/engine/host/store.Layered is: an id is served from the first root that holds it, so a
// user root can override a system one, and a damaged winning copy fails closed instead of falling
// back to a lower root. It never writes. In a build without the C engine the same type is served
// by the pure-Go view.
type LayeredPackageStore struct {
	mu    sync.RWMutex
	ptr   *C.ext_layered
	roots []string
}

// OpenLayeredPackageStore returns a view over roots, highest priority first. It needs at least one
// root and rejects an empty root and a repeated one.
func OpenLayeredPackageStore(roots []string, limits PackageStoreLimits) (*LayeredPackageStore, error) {
	if len(roots) == 0 || limits.MaxArtifactBytes < 0 || limits.MaxTreeFiles < 0 || limits.MaxTreeBytes < 0 ||
		int64(limits.MaxTreeFiles) > math.MaxUint32 {
		return nil, plugin.ErrInvalid
	}
	for _, root := range roots {
		if root == "" || strings.IndexByte(root, 0) >= 0 {
			return nil, plugin.ErrInvalid
		}
	}
	array := C.malloc(C.size_t(len(roots)) * C.size_t(unsafe.Sizeof((*C.char)(nil))))
	if array == nil {
		return nil, plugin.ErrInvalid
	}
	defer C.free(array)
	cs := unsafe.Slice((**C.char)(array), len(roots))
	for i, root := range roots {
		cs[i] = C.CString(root)
	}
	defer func() {
		for _, c := range cs {
			C.free(unsafe.Pointer(c))
		}
	}()
	cl := C.ext_store_limits{
		max_artifact_bytes: C.uint64_t(limits.MaxArtifactBytes),
		max_tree_files:     C.uint32_t(limits.MaxTreeFiles),
		max_tree_bytes:     C.uint64_t(limits.MaxTreeBytes),
	}
	var ptr *C.ext_layered
	if err := status(C.ext_layered_open((**C.char)(array), C.size_t(len(roots)), &cl, &ptr)); err != nil {
		return nil, err
	}
	return &LayeredPackageStore{ptr: ptr, roots: append([]string(nil), roots...)}, nil
}

// Close releases the view. A closed view refuses every call with plugin.ErrClosed.
func (l *LayeredPackageStore) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ptr != nil {
		C.ext_layered_close(l.ptr)
		l.ptr = nil
	}
}

// Roots returns the roots in priority order, as they were given.
func (l *LayeredPackageStore) Roots() []string { return append([]string(nil), l.roots...) }

// Load reads and verifies the package from the first root that holds id. plugin.ErrNotFound means
// no root does.
func (l *LayeredPackageStore) Load(id string) (PackageRecord, error) {
	if strings.IndexByte(id, 0) >= 0 {
		return PackageRecord{}, plugin.ErrInvalid
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ptr == nil {
		return PackageRecord{}, plugin.ErrClosed
	}
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	var manifest C.ext_buffer
	layer := C.size_t(0)
	if err := status(C.ext_layered_load(l.ptr, cid, &manifest, &layer)); err != nil {
		return PackageRecord{}, err
	}
	return PackageRecord{
		ID:        id,
		Directory: filepath.Join(l.roots[int(layer)], strings.ReplaceAll(id, "/", "%2f")),
		Layer:     int(layer),
		Manifest:  takeBuffer(&manifest),
	}, nil
}

func (l *LayeredPackageStore) report(call func(*C.ext_layered, *C.ext_buffer) C.ext_status) ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ptr == nil {
		return nil, plugin.ErrClosed
	}
	var buf C.ext_buffer
	if err := status(call(l.ptr, &buf)); err != nil {
		return nil, err
	}
	return takeBuffer(&buf), nil
}

// List verifies and returns one package per id from the highest root that holds it, sorted by id.
// It fails on the first entry that is not a package directory in any root and on the first package
// that does not verify.
func (l *LayeredPackageStore) List() ([]PackageRecord, error) {
	raw, err := l.report(func(p *C.ext_layered, b *C.ext_buffer) C.ext_status { return C.ext_layered_list(p, b) })
	if err != nil {
		return nil, err
	}
	return decodeRecords(raw)
}

// Verify checks what Load and List would serve and reports every failure. A shadowed copy is not
// checked.
func (l *LayeredPackageStore) Verify() ([]PackageCheck, error) {
	raw, err := l.report(func(p *C.ext_layered, b *C.ext_buffer) C.ext_status { return C.ext_layered_verify(p, b) })
	if err != nil {
		return nil, err
	}
	return decodeChecks(raw)
}

// ListUnverified returns the manifests of the packages each root serves, and the entries that could
// not be read, without reading any artifact. THE PACKAGES ARE UNVERIFIED.
func (l *LayeredPackageStore) ListUnverified() ([]PackageRecord, []PackageCheck, error) {
	raw, err := l.report(func(p *C.ext_layered, b *C.ext_buffer) C.ext_status { return C.ext_layered_list_unverified(p, b) })
	if err != nil {
		return nil, nil, err
	}
	return decodeUnverified(raw)
}

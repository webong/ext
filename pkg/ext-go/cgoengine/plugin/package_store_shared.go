//go:build !(ext_cengine && cgo && (darwin || linux || windows))

package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/webong/ext/pkg/ext-go/cengine/library"
	native "github.com/webong/ext/pkg/ext-go/cengine/plugin"
	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/plugin/host/trust"
)

func storeError(err error) error {
	if err == nil {
		return nil
	}
	var code *library.CodeError
	if !errors.As(err, &code) {
		return err
	}
	var cause error
	switch code.Name {
	case "invalid":
		cause = plugin.ErrInvalid
	case "denied":
		cause = plugin.ErrDenied
	case "mismatch":
		cause = plugin.ErrMismatch
	case "unsupported":
		cause = plugin.ErrUnsupported
	case "not_found":
		cause = plugin.ErrNotFound
	case "ambiguous":
		cause = plugin.ErrAmbiguous
	case "closed":
		cause = plugin.ErrClosed
	case "exists":
		cause = ErrPackageExists
	case "missing":
		cause = os.ErrNotExist
	}
	if cause != nil {
		return fmt.Errorf("%w: %w", err, cause)
	}
	return err
}

func storeStatus(code int) error {
	if code == 0 {
		return nil
	}
	lib, err := native.Default()
	if err != nil {
		return err
	}
	var name string
	if err = lib.Objects().Library().Call("code_name", map[string]any{"code": code}, &name); err != nil {
		return storeError(err)
	}
	return storeError(&library.CodeError{Library: "ext_plugin", Op: "store report", Code: int32(code), Name: name})
}

func storeLimits(l PackageStoreLimits) (map[string]any, error) {
	if l.MaxArtifactBytes < 0 || l.MaxTreeFiles < 0 || int64(l.MaxTreeFiles) > math.MaxUint32 || l.MaxTreeBytes < 0 || l.MaxArchiveBytes < 0 {
		return nil, plugin.ErrInvalid
	}
	return map[string]any{"max_artifact_bytes": l.MaxArtifactBytes, "max_tree_files": l.MaxTreeFiles, "max_tree_bytes": l.MaxTreeBytes}, nil
}

func openStoreObject(kind string, config map[string]any, limits PackageStoreLimits) (*storeObject, error) {
	values, err := storeLimits(limits)
	if err != nil {
		return nil, err
	}
	config["limits"] = values
	lib, err := native.Default()
	if err != nil {
		return nil, err
	}
	o, err := lib.Objects().Open(kind, config, nil)
	if err != nil {
		return nil, storeError(err)
	}
	return &storeObject{object: o, api: lib.Objects().Interface}, nil
}

// storeObject owns calls and close together, preserving the store's closed-state
// contract and exact manifest bytes returned by C (JSON decoding trims whitespace).
type storeObject struct {
	mu     sync.RWMutex
	object *library.Object
	api    *library.Interface
	closed bool
}

func (s *storeObject) Call(op string, input, output any) error {
	if fields, ok := input.(map[string]any); ok {
		for _, value := range fields {
			if text, ok := value.(string); ok && strings.ContainsRune(text, 0) {
				return plugin.ErrInvalid
			}
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return plugin.ErrClosed
	}
	if input == nil {
		input = map[string]any{}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	answer, err := s.api.Call(s.object.Handle(), op, raw)
	if err != nil {
		return err
	}
	if out, ok := output.(*json.RawMessage); ok {
		*out = append((*out)[:0], answer...)
		return nil
	}
	if output != nil {
		return json.Unmarshal(answer, output)
	}
	return nil
}
func (s *storeObject) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.object.Close()
}

// PackageStore delegates storage, verification and lifecycle to the shared C engine.
type PackageStore struct {
	object          *storeObject
	maxArchiveBytes int64
}

func OpenPackageStore(root string, limits PackageStoreLimits) (*PackageStore, error) {
	if root == "" || strings.ContainsRune(root, 0) {
		return nil, plugin.ErrInvalid
	}
	o, err := openStoreObject("store", map[string]any{"root": root}, limits)
	if err != nil {
		return nil, err
	}
	return &PackageStore{o, limits.MaxArchiveBytes}, nil
}
func (s *PackageStore) Close() { _ = s.object.Close() }
func (s *PackageStore) call(op string, input any) ([]byte, error) {
	var out json.RawMessage
	err := storeError(s.object.Call(op, input, &out))
	return out, err
}
func (s *PackageStore) put(op, source string, preserve bool) ([]byte, error) {
	return s.call(op, map[string]any{"source": source, "preserve_executable": preserve})
}
func (s *PackageStore) Install(source string, preserve bool) ([]byte, error) {
	return s.put("install", source, preserve)
}
func (s *PackageStore) Upgrade(source string, preserve bool) ([]byte, error) {
	return s.put("upgrade", source, preserve)
}
func (s *PackageStore) Replace(source string, preserve bool) ([]byte, error) {
	return s.put("replace", source, preserve)
}
func (s *PackageStore) InstallArchive(archive, sum, id string, preserve bool) ([]byte, error) {
	if archive == "" || strings.Contains(archive, "://") {
		return nil, plugin.ErrInvalid
	}
	return s.call("install_archive", map[string]any{"archive": archive, "sha256": sum, "id": id, "preserve_executable": preserve, "max_archive_bytes": s.maxArchiveBytes})
}
func (s *PackageStore) Receipt(id string) ([]byte, error) {
	return s.call("receipt", map[string]any{"id": id})
}
func (s *PackageStore) Remove(id string) error {
	return storeError(s.object.Call("remove", map[string]any{"id": id}, nil))
}
func (s *PackageStore) Load(id string) ([]byte, error) {
	return s.call("load", map[string]any{"id": id})
}
func (s *PackageStore) ResolveEntrypoint(id, contract, version, protocol, digest string) (PackageEntrypoint, error) {
	var out PackageEntrypoint
	err := storeError(s.object.Call("resolve_entrypoint", map[string]any{"id": id, "contract": contract, "version": version, "protocol": protocol, "reviewed_digest": digest, "os": runtime.GOOS, "arch": runtime.GOARCH}, &out))
	return out, err
}
func (s *PackageStore) IDs() ([]string, error) {
	var ids []string
	err := storeError(s.object.Call("ids", nil, &ids))
	return ids, err
}
func (s *PackageStore) List() ([]PackageRecord, error) {
	raw, err := s.call("list", nil)
	if err != nil {
		return nil, err
	}
	return decodeRecords(raw)
}
func (s *PackageStore) Verify() ([]PackageCheck, error) {
	raw, err := s.call("verify", nil)
	if err != nil {
		return nil, err
	}
	return decodeChecks(raw)
}
func (s *PackageStore) ListUnverified() ([]PackageRecord, []PackageCheck, error) {
	raw, err := s.call("list_unverified", nil)
	if err != nil {
		return nil, nil, err
	}
	return decodeUnverified(raw)
}

// LayeredPackageStore is the C engine's read-only view of ordered package roots.
type LayeredPackageStore struct {
	object *storeObject
	roots  []string
}

func OpenLayeredPackageStore(roots []string, limits PackageStoreLimits) (*LayeredPackageStore, error) {
	for _, root := range roots {
		if root == "" || strings.ContainsRune(root, 0) {
			return nil, plugin.ErrInvalid
		}
	}
	o, err := openStoreObject("layered", map[string]any{"roots": roots}, limits)
	if err != nil {
		return nil, err
	}
	return &LayeredPackageStore{o, append([]string(nil), roots...)}, nil
}
func (l *LayeredPackageStore) Close()          { _ = l.object.Close() }
func (l *LayeredPackageStore) Roots() []string { return append([]string(nil), l.roots...) }
func (l *LayeredPackageStore) Load(id string) (PackageRecord, error) {
	var out struct {
		Layer    int             `json:"layer"`
		Manifest json.RawMessage `json:"manifest"`
	}
	err := storeError(l.object.Call("load", map[string]any{"id": id}, &out))
	if err != nil {
		return PackageRecord{}, err
	}
	if out.Layer < 0 || out.Layer >= len(l.roots) {
		return PackageRecord{}, plugin.ErrProtocol
	}
	return PackageRecord{ID: id, Directory: filepath.Join(l.roots[out.Layer], strings.ReplaceAll(id, "/", "%2f")), Layer: out.Layer, Manifest: out.Manifest}, nil
}
func (l *LayeredPackageStore) call(op string) ([]byte, error) {
	var out json.RawMessage
	err := storeError(l.object.Call(op, nil, &out))
	return out, err
}
func (l *LayeredPackageStore) List() ([]PackageRecord, error) {
	raw, err := l.call("list")
	if err != nil {
		return nil, err
	}
	return decodeRecords(raw)
}
func (l *LayeredPackageStore) Verify() ([]PackageCheck, error) {
	raw, err := l.call("verify")
	if err != nil {
		return nil, err
	}
	return decodeChecks(raw)
}
func (l *LayeredPackageStore) ListUnverified() ([]PackageRecord, []PackageCheck, error) {
	raw, err := l.call("list_unverified")
	if err != nil {
		return nil, nil, err
	}
	return decodeUnverified(raw)
}

func storeLibraryCall(operation string, input, output any) error {
	lib, err := native.Default()
	if err != nil {
		return err
	}
	return storeError(lib.Objects().Library().Call(operation, input, output))
}
func StoreDigest(root, id string) (string, error) {
	var out string
	err := storeLibraryCall("store_digest", map[string]any{"root": root, "id": id}, &out)
	return out, err
}
func TrustStateOf(path, scope, id, digest string) (hosttrust.State, error) {
	var out string
	err := storeLibraryCall("trust_state", map[string]any{"path": path, "scope": scope, "id": id, "digest": digest}, &out)
	if err != nil {
		return hosttrust.Untrusted, err
	}
	switch out {
	case "trusted":
		return hosttrust.Trusted, nil
	case "changed":
		return hosttrust.Changed, nil
	case "untrusted":
		return hosttrust.Untrusted, nil
	}
	return hosttrust.Untrusted, plugin.ErrProtocol
}
func TrustRecord(path, scope, id, digest string) error {
	return storeLibraryCall("trust_record", map[string]any{"path": path, "scope": scope, "id": id, "digest": digest}, nil)
}
func TrustRevoke(path, scope, id string) error {
	return storeLibraryCall("trust_revoke", map[string]any{"path": path, "scope": scope, "id": id}, nil)
}

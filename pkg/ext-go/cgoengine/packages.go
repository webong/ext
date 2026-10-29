//go:build ext_cengine && cgo && (darwin || linux || windows)

package cgoengine

/*
#include "ext_host.h"
#include <stdlib.h>
*/
import "C"
import (
	"encoding/hex"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/interop"
	hosttrust "github.com/webong/ext/pkg/ext-go/engine/host/trust"
	"github.com/webong/ext/pkg/ext-go/engine/validate/schema"
	"strings"
	"unicode/utf8"
	"unsafe"
)

// SHA256 hashes bytes using the shared engine. It does not establish trust.
func SHA256(input []byte) [32]byte {
	var out [32]byte
	var p *C.uint8_t
	if len(input) > 0 {
		p = (*C.uint8_t)(unsafe.Pointer(&input[0]))
	}
	C.ext_engine_sha256(p, C.size_t(len(input)), (*C.uint8_t)(unsafe.Pointer(&out[0])))
	return out
}

// StoreDigest verifies one installed package in the C engine and returns the
// exact directory digest used by a trust record.
func StoreDigest(root, id string) (string, error) {
	if root == "" || strings.IndexByte(root, 0) >= 0 || strings.IndexByte(id, 0) >= 0 {
		return "", plugin.ErrInvalid
	}
	croot, cid := C.CString(root), C.CString(id)
	defer C.free(unsafe.Pointer(croot))
	defer C.free(unsafe.Pointer(cid))
	var sum [32]byte
	if err := status(C.ext_store_digest(croot, cid, (*C.uint8_t)(unsafe.Pointer(&sum[0])))); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum[:]), nil
}

func trustInputs(path, scope, id, digest string) ([]byte, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || strings.IndexByte(scope, 0) >= 0 || strings.IndexByte(id, 0) >= 0 || len(digest) != 64 || strings.ToLower(digest) != digest {
		return nil, plugin.ErrInvalid
	}
	data, err := hex.DecodeString(digest)
	if err != nil {
		return nil, plugin.ErrInvalid
	}
	return data, nil
}

// TrustStateOf compares one verified digest with a scoped trust record.
func TrustStateOf(path, scope, id, digest string) (hosttrust.State, error) {
	data, err := trustInputs(path, scope, id, digest)
	if err != nil {
		return hosttrust.Untrusted, err
	}
	cp, cs, ci := C.CString(path), C.CString(scope), C.CString(id)
	defer C.free(unsafe.Pointer(cp))
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ci))
	var state C.ext_trust_state
	err = status(C.ext_trust_state_of(cp, cs, ci, (*C.uint8_t)(unsafe.Pointer(&data[0])), &state))
	return hosttrust.State(state), err
}

// TrustRecord persists only a digest the caller has explicitly reviewed.
func TrustRecord(path, scope, id, digest string) error {
	data, err := trustInputs(path, scope, id, digest)
	if err != nil {
		return err
	}
	cp, cs, ci := C.CString(path), C.CString(scope), C.CString(id)
	defer C.free(unsafe.Pointer(cp))
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ci))
	return status(C.ext_trust_record(cp, cs, ci, (*C.uint8_t)(unsafe.Pointer(&data[0]))))
}

// TrustRevoke removes one scope's grant without changing other scopes.
func TrustRevoke(path, scope, id string) error {
	if path == "" || strings.IndexByte(path, 0) >= 0 || strings.IndexByte(scope, 0) >= 0 || strings.IndexByte(id, 0) >= 0 {
		return plugin.ErrInvalid
	}
	cp, cs, ci := C.CString(path), C.CString(scope), C.CString(id)
	defer C.free(unsafe.Pointer(cp))
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(ci))
	return status(C.ext_trust_revoke(cp, cs, ci))
}
func engineValue(operation string, input any, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	data, err = EngineCall(operation, data)
	if err != nil {
		return err
	}
	if output == nil {
		return nil
	}
	return plugin.Decode(data, output)
}
func ResolveRoute(hosts, guests []plugin.BackendProfile, bridges []interop.Bridge, w interop.Requirements) (interop.Route, error) {
	var out interop.Route
	err := engineValue("route.resolve", map[string]any{"hosts": hosts, "guests": guests, "bridges": bridges, "requirements": map[string]bool{"concurrent": w.Concurrent, "nativeStreaming": w.NativeStreaming, "nativeCallbacks": w.NativeCallbacks}}, &out)
	return out, err
}
func ValidateSchema(s schema.Schema) error { return engineValue("schema.validate", s, nil) }
func CheckSchema(s schema.Schema, value json.RawMessage) error {
	if len(value) == 0 {
		return plugin.ErrInvalid
	}
	return engineValue("schema.check", map[string]any{"schema": s, "value": value}, nil)
}

// VerifyPackage validates a package manifest (JSON) and checks the artifacts it
// lists under root, in C. The application must protect reviewed files against
// modification and separately establish trust. Package.VerifyArtifacts in the
// packages subpackage is the typed form.
func VerifyPackage(manifest []byte, root string) error {
	if len(manifest) == 0 || strings.IndexByte(root, 0) >= 0 {
		return plugin.ErrInvalid
	}
	path := C.CString(root)
	defer C.free(unsafe.Pointer(path))
	return status(C.ext_package_verify((*C.uint8_t)(unsafe.Pointer(&manifest[0])), C.size_t(len(manifest)), path))
}

// EngineValue runs a pure engine service with a JSON-encoded input and decodes
// the result into output (nil to discard it). See lib/plugin/services.md for the
// operations.
func EngineValue(operation string, input any, output any) error {
	return engineValue(operation, input, output)
}

func DirectoryDigest(root string) (string, error) {
	if strings.IndexByte(root, 0) >= 0 {
		return "", plugin.ErrInvalid
	}
	path := C.CString(root)
	defer C.free(unsafe.Pointer(path))
	var out [32]byte
	if err := status(C.ext_directory_digest(path, (*C.uint8_t)(unsafe.Pointer(&out[0])))); err != nil {
		return "", err
	}
	return hex.EncodeToString(out[:]), nil
}

// ArtifactDigest returns the digest of one artifact under root, addressed by a
// portable relative path, never following symbolic links: the SHA-256 of a
// file, or for a tree the directory digest of a directory that holds at least
// one file. It computes the digests that a manifest records.
func ArtifactDigest(root, path string, tree bool) (string, error) {
	if strings.IndexByte(root, 0) >= 0 || strings.IndexByte(path, 0) >= 0 || !utf8.ValidString(path) {
		return "", plugin.ErrInvalid
	}
	r, p := C.CString(root), C.CString(path)
	defer C.free(unsafe.Pointer(r))
	defer C.free(unsafe.Pointer(p))
	flag := C.int(0)
	if tree {
		flag = 1
	}
	var out [32]byte
	if err := status(C.ext_artifact_digest(r, p, flag, (*C.uint8_t)(unsafe.Pointer(&out[0])))); err != nil {
		return "", err
	}
	return hex.EncodeToString(out[:]), nil
}

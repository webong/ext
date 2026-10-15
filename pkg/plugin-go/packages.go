//go:build ext_cengine && cgo && (darwin || linux || windows)

package goengine

/*
#include "ext_host.h"
#include <stdlib.h>
*/
import "C"
import (
	"encoding/hex"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/interop"
	"github.com/webong/ext/pkg/plugin-go/schema"
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

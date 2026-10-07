//go:build ctx_cengine && cgo && (darwin || linux)

package goengine

/*
#include "ctx_host.h"
#include <stdlib.h>
*/
import "C"
import (
	"encoding/hex"
	"encoding/json"
	"github.com/webong/ext/pkg/plugin"
	"github.com/webong/ext/pkg/plugin/interop"
	"github.com/webong/ext/pkg/plugin/packagekit"
	"github.com/webong/ext/pkg/plugin/schema"
	"strings"
	"unsafe"
)

// SHA256 hashes bytes using the shared engine. It does not establish trust.
func SHA256(input []byte) [32]byte {
	var out [32]byte
	var p *C.uint8_t
	if len(input) > 0 {
		p = (*C.uint8_t)(unsafe.Pointer(&input[0]))
	}
	C.ctx_engine_sha256(p, C.size_t(len(input)), (*C.uint8_t)(unsafe.Pointer(&out[0])))
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
func ValidateManifest(m packagekit.Manifest) error { return engineValue("package.validate", m, nil) }
func ResolvePackages(manifests []packagekit.Manifest) (packagekit.Plan, error) {
	if manifests == nil {
		manifests = []packagekit.Manifest{}
	}
	var out packagekit.Plan
	err := engineValue("package.resolve", manifests, &out)
	return out, err
}
func SelectEntrypoint(m packagekit.Manifest, name string, env packagekit.Environment) (packagekit.LaunchSelection, error) {
	var out packagekit.LaunchSelection
	err := engineValue("package.select", map[string]any{"manifest": m, "name": name, "environment": map[string]any{"os": env.OS, "arch": env.Arch, "runtimes": env.Runtimes, "sharedVersions": env.SharedVersions}}, &out)
	return out, err
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

// VerifyArtifacts checks the manifest and listed files in C. The application
// must protect reviewed files against modification and separately establish trust.
func VerifyArtifacts(m packagekit.Manifest, root string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if strings.IndexByte(root, 0) >= 0 {
		return plugin.ErrInvalid
	}
	path := C.CString(root)
	defer C.free(unsafe.Pointer(path))
	return status(C.ctx_package_verify((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), path))
}
func DirectoryDigest(root string) (string, error) {
	if strings.IndexByte(root, 0) >= 0 {
		return "", plugin.ErrInvalid
	}
	path := C.CString(root)
	defer C.free(unsafe.Pointer(path))
	var out [32]byte
	if err := status(C.ctx_directory_digest(path, (*C.uint8_t)(unsafe.Pointer(&out[0])))); err != nil {
		return "", err
	}
	return hex.EncodeToString(out[:]), nil
}

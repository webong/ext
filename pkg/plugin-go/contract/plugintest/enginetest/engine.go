// Package enginetest holds the differential checks that hold an engine of the shared C
// implementation against the pure-Go contract, which is the oracle: the same documents,
// requests, schemas and endpoints are judged by both, and the answers must agree. An engine is
// whatever runs the C engine's services (the cgo binding in pkg/plugin-go/cengine, and any other
// build of the same C), so each engine's tests call these checks with their own Engine and
// nothing else changes.
package enginetest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/guest/instance"
	"github.com/webong/ext/pkg/plugin-go/contract/validate/schema"
)

// Engine is an implementation of the shared engine under test.
type Engine struct {
	// Name says which engine failed, in messages.
	Name string
	// Call runs one of the engine's pure services (descriptor.validate, schema.validate,
	// endpoint.check, ...) on bounded JSON. No plugin is executed.
	Call func(operation string, input json.RawMessage) (json.RawMessage, error)
	// Fixtures is the directory of the shared fixtures, lib/plugin/testdata.
	Fixtures string
	// NewInstances opens the engine's instance manager over string values. Optional: the checks
	// that compare instance managers are skipped without it.
	NewInstances func(instance.Options[string]) (Instances, error)
	// Open admits a plugin through the engine, as plugin.Open does through the oracle. Optional:
	// the checks that compare sessions are skipped without it.
	Open func(ctx context.Context, d plugin.Descriptor, o plugin.Options) (Session, error)
	// OpenProcess is Open with a child process the engine itself starts (launch.Executable is the
	// test binary, which serves as the plugin: see MaybeServeGuest). Optional: the process check is
	// skipped without it.
	OpenProcess func(ctx context.Context, d plugin.Descriptor, launch Launch, o plugin.Options) (Session, error)
}

// fixture reads a shared fixture, such as "v1/descriptor.json".
func (e Engine) fixture(t testing.TB, name string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(filepath.Join(e.Fixtures, filepath.FromSlash(name)))
}

// service marshals input and runs the named service.
func (e Engine) service(t testing.TB, name string, input any) (json.RawMessage, error) {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return e.Call(name, data)
}

// checkSchema asks the engine whether value satisfies s; the empty value is invalid.
func (e Engine) checkSchema(s schema.Schema, value json.RawMessage) error {
	if len(value) == 0 {
		return plugin.ErrInvalid
	}
	input, err := json.Marshal(map[string]any{"schema": s, "value": value})
	if err != nil {
		return err
	}
	_, err = e.Call("schema.check", input)
	return err
}

package plugin

import "errors"

// ErrRegistryExists is returned by a GuestRegistry for a contract that is already
// registered, or a reverse operation that is already routed.
var ErrRegistryExists = errors.New("already registered")

// A GuestRegistry composes plugins through their host: a reverse call one session's
// plugin makes is served by a forward call to another session, along an edge
// declared with Route. In this build of the engine it is the C engine's
// ext_registry (ext_host.h); in the pure-Go build it is the same behavior in
// Go. pkg/ext-go/engine/plugin/host's Registry documents the rules, the refusal codes
// and how cycles are refused; both answer lib/plugin/testdata/v1/registry-cases.json.
//
// A session is a source when it is opened with the registry's OpenProcess, and
// a target once Register names the contract it serves. A target is unregistered
// when its session ends. A GuestRegistry is safe for concurrent use.

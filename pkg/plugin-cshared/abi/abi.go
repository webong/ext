// Package abi defines the language-neutral ctx.plugin C ABI v1 envelope.
// See ../ctx_plugin.h for the C declarations and ownership rules.
package abi

import "time"

const Version uint32 = 1
const (
	Handshake uint32 = 1
	Invoke    uint32 = 2
)
const (
	OK uint32 = iota
	Invalid
	Closed
	Failed
)

// Hello bounds the guest handshake using the same absolute deadline convention
// as plugin.Request. Open creates a handle; Hello declares its domain contract.
type Hello struct {
	Deadline time.Time `json:"deadline"`
}

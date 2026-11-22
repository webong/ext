// Package abi defines the language-neutral ext.plugin C ABI v1 envelope.
// See lib/plugin/include/ext_plugin.h for the C declarations and ownership rules.
package abi

import "time"

const Version uint32 = 1

// MaxFrameBytes is the largest frame either side of the ABI passes, and the exact capacity of the
// response buffer the host hands the guest. It is the plugin contract's frame limit.
const MaxFrameBytes = 24 << 20
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

//go:build !((darwin && amd64) || (darwin && arm64) || (linux && amd64) || (linux && arm64) || (windows && amd64) || (windows && arm64))

package ccgo

import plugin "github.com/webong/ext/pkg/ext-go/engine"

// Available is false where no translation has been generated for the platform.
const Available = false

// Call reports plugin.ErrUnsupported: this platform has no translation.
func Call(string, []byte) ([]byte, error) { return nil, plugin.ErrUnsupported }

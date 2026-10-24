//go:build !((darwin && arm64) || (linux && arm64))

package ccgo

import plugin "github.com/webong/ext/pkg/plugin-go/contract"

// Available is false where no translation has been generated for the platform.
const Available = false

// Call reports plugin.ErrUnsupported: this platform has no translation.
func Call(string, []byte) ([]byte, error) { return nil, plugin.ErrUnsupported }

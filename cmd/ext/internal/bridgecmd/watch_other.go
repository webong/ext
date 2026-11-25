//go:build !darwin && !linux && !freebsd

package bridgecmd

import plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"

func watchParent(config) error { return plugin.ErrUnsupported }

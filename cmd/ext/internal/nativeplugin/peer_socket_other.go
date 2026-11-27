//go:build !darwin && !linux

package nativeplugin

import (
	"net"

	plugin "github.com/webong/ext/pkg/ext-go/engine/plugin"
)

const peerSocketsSupported = false

func checkPeerSocket(string) error { return plugin.ErrUnsupported }

func authenticatePeer(net.Conn) error { return plugin.ErrUnsupported }

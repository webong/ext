//go:build !windows

package app

import "github.com/webong/ext/res/system/capture/clipboard"

func nativeClipboardBackend() (clipboard.Backend, bool) {
	return nil, false
}

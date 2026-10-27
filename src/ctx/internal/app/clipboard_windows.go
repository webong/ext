//go:build windows

package app

import "github.com/webong/ext/res/system/clipboard"

func nativeClipboardBackend() (clipboard.Backend, bool) {
	return clipboard.WindowsBackend(), true
}

//go:build windows

package systemgraph

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

func platformWebviews(ctx context.Context) ([]WebviewInfo, error) {
	// Microsoft's supported Evergreen Runtime detection uses the version
	// registration in both machine and user EdgeUpdate Clients keys.
	const key = `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	result := []WebviewInfo{}
	seen := map[string]bool{}
	for _, root := range []struct {
		handle      syscall.Handle
		name, scope string
	}{{syscall.HKEY_LOCAL_MACHINE, "HKEY_LOCAL_MACHINE", "system"}, {syscall.HKEY_CURRENT_USER, "HKEY_CURRENT_USER", "user"}} {
		views := []struct {
			flag uint32
			name string
		}{{syscall.KEY_WOW64_32KEY, "32"}, {syscall.KEY_WOW64_64KEY, "64"}}
		if root.scope == "user" {
			views[0], views[1] = views[1], views[0]
		}
		for _, view := range views {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			version, err := webviewRegistryVersion(root.handle, key, view.flag)
			if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
				continue
			}
			if err != nil {
				return nil, fmtHostReadError("WebView2 runtime registration", err)
			}
			identity := root.scope + "\x00" + version
			if !positiveWebviewVersion(version) || seen[identity] {
				continue
			}
			seen[identity] = true
			result = append(result, WebviewInfo{Name: "WebView2", Engine: "blink", API: "WebView2", Version: version,
				Location: root.name + `\` + key + " (view " + view.name + ")", Scope: root.scope, Source: "registry"})
		}
	}
	return result, ctx.Err()
}

func webviewRegistryVersion(root syscall.Handle, key string, view uint32) (string, error) {
	name, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return "", err
	}
	var handle syscall.Handle
	if err := syscall.RegOpenKeyEx(root, name, 0, syscall.KEY_QUERY_VALUE|view, &handle); err != nil {
		return "", err
	}
	defer syscall.RegCloseKey(handle)
	value, err := syscall.UTF16PtrFromString("pv")
	if err != nil {
		return "", err
	}
	buffer := make([]byte, 1024)
	for attempt := 0; attempt < 3; attempt++ {
		var kind uint32
		size := uint32(len(buffer))
		err := syscall.RegQueryValueEx(handle, value, nil, &kind, &buffer[0], &size)
		if errors.Is(err, syscall.ERROR_MORE_DATA) && size > uint32(len(buffer)) && size <= 65536 {
			buffer = make([]byte, size)
			continue
		}
		if err != nil {
			return "", err
		}
		if kind != syscall.REG_SZ || size%2 != 0 || size > uint32(len(buffer)) {
			return "", fmt.Errorf("invalid WebView2 version registration")
		}
		characters := make([]uint16, size/2)
		for i := range characters {
			characters[i] = binary.LittleEndian.Uint16(buffer[i*2:])
		}
		return strings.TrimSpace(syscall.UTF16ToString(characters)), nil
	}
	return "", fmt.Errorf("WebView2 version registration changed during discovery")
}

func positiveWebviewVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return false
	}
	positive := false
	for _, part := range parts {
		number, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		positive = positive || number > 0
	}
	return positive
}

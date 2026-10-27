//go:build !unix && !windows

package volume

func systemAlias(string) bool { return false }

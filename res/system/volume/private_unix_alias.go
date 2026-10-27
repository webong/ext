//go:build unix && !darwin

package volume

func systemAlias(string) bool { return false }
